package providers

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"strings"
	"time"

	"github.com/discorgento/ouija/engine"
)

// Anthropic implements engine.Provider via the Messages API, with prompt caching
// on the stable system block (the shared prefix across the N passes).
type Anthropic struct {
	apiKey string
	client *Client
}

func init() {
	Register("anthropic", func(apiKey string) engine.Provider { return NewAnthropic(apiKey) })
}

func NewAnthropic(apiKey string) *Anthropic {
	return &Anthropic{
		apiKey: apiKey,
		client: NewClient(5 * time.Minute),
	}
}

func (a *Anthropic) Name() string { return "anthropic" }

func (a *Anthropic) Capabilities() engine.Capabilities {
	return engine.Capabilities{StructuredOutput: true}
}

func (a *Anthropic) Chat(ctx context.Context, req engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
	return func(yield func(engine.StreamEvent, error) bool) {
		if a.apiKey == "" {
			yield(engine.StreamEvent{}, engine.ErrAuth("anthropic: API key not set — run `ouija setup`"))
			return
		}

		body := map[string]any{
			"model":      req.Model,
			"max_tokens": max(req.MaxTokens, minMaxTokens),
			"messages":   antMessages(req.Messages),
			"stream":     true,
		}
		if req.System != "" {
			body["system"] = []map[string]any{{
				"type":          "text",
				"text":          req.System,
				"cache_control": map[string]any{"type": "ephemeral"},
			}}
		}
		var tools []map[string]any
		for _, t := range req.Tools {
			tools = append(tools, map[string]any{
				"name":         t.Name,
				"description":  t.Description,
				"input_schema": t.Schema,
			})
		}
		if len(tools) > 0 {
			tools[len(tools)-1]["cache_control"] = map[string]any{"type": "ephemeral"}
			body["tools"] = tools
			// "none", not a forced choice: current models reject forced
			// tool_choice ("any"/"tool") with a 400.
			if req.NoToolCalls {
				body["tool_choice"] = map[string]any{"type": "none"}
			}
		}
		// Structured output: JSON constrained to the schema, as text.
		if req.Schema != nil {
			body["output_config"] = map[string]any{
				"format": map[string]any{"type": "json_schema", "schema": outputSchema(req.Schema)},
			}
		}

		raw, _ := json.Marshal(body)
		headers := map[string]string{
			"x-api-key":         a.apiKey,
			"anthropic-version": "2023-06-01",
			"content-type":      "application/json",
		}

		type antTool struct {
			ID    string
			Name  string
			Start []byte // the start block's input: always {} when deltas follow
			Args  []byte // the input_json_delta fragments
		}
		toolBlocks := make(map[int]*antTool)
		var usage engine.Usage
		var stopReason, refusal string

		err := a.client.PostSSE(ctx, "https://api.anthropic.com/v1/messages", headers, raw,
			func(eventType string, data []byte) error {
				switch eventType {
				case "error":
					var errPayload struct {
						Error struct {
							Type    string `json:"type"`
							Message string `json:"message"`
						} `json:"error"`
					}
					_ = json.Unmarshal(data, &errPayload)
					msg := "anthropic: " + cmp.Or(errPayload.Error.Message, "SSE error: "+string(data))
					// Overload and internal errors mid-stream are transient.
					if t := errPayload.Error.Type; t == "overloaded_error" || t == "api_error" {
						return engine.ErrStream(msg)
					}
					return engine.ErrProvider(msg)

				case "message_start":
					var ms struct {
						Message struct {
							Usage struct {
								InputTokens         int `json:"input_tokens"`
								OutputTokens        int `json:"output_tokens"`
								CacheReadTokens     int `json:"cache_read_input_tokens"`
								CacheCreationTokens int `json:"cache_creation_input_tokens"`
							} `json:"usage"`
						} `json:"message"`
					}
					if json.Unmarshal(data, &ms) == nil {
						usage.InputTokens = ms.Message.Usage.InputTokens
						usage.OutputTokens = ms.Message.Usage.OutputTokens
						usage.CacheReadTokens = ms.Message.Usage.CacheReadTokens
						usage.CacheWriteTokens = ms.Message.Usage.CacheCreationTokens
					}

				case "content_block_start":
					var cbs struct {
						Index int `json:"index"`
						Block struct {
							Type  string          `json:"type"`
							ID    string          `json:"id"`
							Name  string          `json:"name"`
							Input json.RawMessage `json:"input"`
						} `json:"content_block"`
					}
					if json.Unmarshal(data, &cbs) != nil {
						return nil
					}
					if cbs.Block.Type == "tool_use" {
						toolBlocks[cbs.Index] = &antTool{
							ID:    cbs.Block.ID,
							Name:  cbs.Block.Name,
							Start: []byte(cbs.Block.Input),
						}
					}

				case "content_block_delta":
					var cbd struct {
						Index int `json:"index"`
						Delta struct {
							Type        string `json:"type"`
							Text        string `json:"text"`
							PartialJSON string `json:"partial_json"`
						} `json:"delta"`
					}
					if json.Unmarshal(data, &cbd) != nil {
						return nil
					}
					switch cbd.Delta.Type {
					case "text_delta":
						if !yield(engine.StreamEvent{Type: engine.EventText, Text: cbd.Delta.Text}, nil) {
							return errStopped
						}
					case "input_json_delta":
						if tb, ok := toolBlocks[cbd.Index]; ok {
							tb.Args = append(tb.Args, cbd.Delta.PartialJSON...)
						}
					}

				case "content_block_stop":
					var cbs struct {
						Index int `json:"index"`
					}
					if json.Unmarshal(data, &cbs) != nil {
						return nil
					}
					if tb, ok := toolBlocks[cbs.Index]; ok {
						raw := tb.Args
						if len(raw) == 0 { // no deltas: the start block held the whole input
							raw = tb.Start
						}
						var input map[string]any
						if err := json.Unmarshal(raw, &input); err != nil {
							input = map[string]any{}
						}
						if !yield(engine.StreamEvent{
							Type:     engine.EventToolCall,
							ToolCall: engine.ToolCall{ID: tb.ID, Name: tb.Name, Input: input},
						}, nil) {
							return errStopped
						}
						delete(toolBlocks, cbs.Index)
					}

				case "message_delta":
					var md struct {
						Delta struct {
							StopReason  string `json:"stop_reason"`
							StopDetails *struct {
								Category    string `json:"category"`
								Explanation string `json:"explanation"`
							} `json:"stop_details"`
						} `json:"delta"`
						Usage struct {
							OutputTokens        int `json:"output_tokens"`
							CacheCreationTokens int `json:"cache_creation_input_tokens"`
						} `json:"usage"`
					}
					if json.Unmarshal(data, &md) == nil {
						stopReason = md.Delta.StopReason
						if d := md.Delta.StopDetails; d != nil {
							refusal = strings.TrimSpace(d.Category + ": " + d.Explanation)
						}
						if md.Usage.OutputTokens > 0 {
							usage.OutputTokens = md.Usage.OutputTokens
						}
						if md.Usage.CacheCreationTokens > 0 {
							usage.CacheWriteTokens = md.Usage.CacheCreationTokens
						}
					}

				case "message_stop":
					if !yield(engine.StreamEvent{Type: engine.EventUsage, Usage: usage}, nil) {
						return errStopped
					}
					if !yield(engine.StreamEvent{Type: engine.EventDone, StopReason: stopReason}, nil) {
						return errStopped
					}

				case "ping":
					// Anthropic sends pings to keep the connection alive.
				}
				return nil
			},
		)
		// A safety decline is HTTP 200 with no usable content; taken as a plain
		// answer it would read as an empty reply. Checked after the stream, not
		// in the callback, so it holds however the stream ends.
		if err == nil && stopReason == "refusal" {
			err = engine.ErrProvider("anthropic: the model declined the request (" +
				strings.Trim(refusal, ": ") + ")")
		}
		if err != nil && !errors.Is(err, errStopped) {
			yield(engine.StreamEvent{}, err)
		}
	}
}

// antMessages converts the neutral message list to Anthropic content blocks,
// grouping consecutive tool results into a single user message as the API requires.
func antMessages(msgs []engine.Message) []map[string]any {
	var out []map[string]any
	var pending []map[string]any
	flush := func() {
		if len(pending) > 0 {
			out = append(out, map[string]any{"role": "user", "content": pending})
			pending = nil
		}
	}
	for _, m := range msgs {
		switch m.Role {
		case engine.RoleUser:
			flush()
			out = append(out, map[string]any{
				"role":    "user",
				"content": []map[string]any{{"type": "text", "text": m.Content}},
			})
		case engine.RoleAssistant:
			flush()
			var blocks []map[string]any
			if m.Content != "" {
				blocks = append(blocks, map[string]any{"type": "text", "text": m.Content})
			}
			for _, tc := range m.ToolCalls {
				blocks = append(blocks, map[string]any{
					"type": "tool_use", "id": tc.ID, "name": tc.Name, "input": tc.Input,
				})
			}
			if len(blocks) == 0 { // an empty turn (e.g. all thinking): null content is a 400
				blocks = append(blocks, map[string]any{"type": "text", "text": emptyTurn})
			}
			out = append(out, map[string]any{"role": "assistant", "content": blocks})
		case engine.RoleTool:
			pending = append(pending, map[string]any{
				"type": "tool_result", "tool_use_id": m.ToolCallID, "content": m.Content,
			})
		}
	}
	flush()
	return out
}

// minMaxTokens is the smallest max_tokens sent. On current models thinking is
// always on and shares max_tokens with the reply, so the small caps totems
// ask for (4096) would cut answers short; max_tokens is a ceiling, not spend.
const minMaxTokens = 16000

// outputSchema adapts a JSON Schema to what structured outputs accept (both
// Anthropic's output_config.format and OpenAI's response_format): every
// object closed with additionalProperties: false (required), and the
// constraints they reject — numeric bounds, string lengths, array sizes —
// removed. Property names are never taken for schema keywords.
func outputSchema(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		if list, ok := v.([]any); ok {
			out := make([]any, len(list))
			for i, item := range list {
				out[i] = outputSchema(item)
			}
			return out
		}
		return v
	}
	out := make(map[string]any, len(m)+1)
	for k, val := range m {
		switch k {
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf",
			"minLength", "maxLength", "minItems", "maxItems", "uniqueItems":
			continue
		case "properties", "$defs", "definitions":
			// A map of name → schema: recurse into the schemas only.
			if props, ok := val.(map[string]any); ok {
				clean := make(map[string]any, len(props))
				for name, schema := range props {
					clean[name] = outputSchema(schema)
				}
				out[k] = clean
				continue
			}
		}
		out[k] = outputSchema(val)
	}
	if out["type"] == "object" || out["properties"] != nil {
		out["additionalProperties"] = false
	}
	return out
}
