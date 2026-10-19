package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"time"

	"github.com/discorgento/ouija/internal/engine"
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
			"max_tokens": req.MaxTokens,
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
		if len(req.Tools) > 0 {
			var tools []map[string]any
			for _, t := range req.Tools {
				tools = append(tools, map[string]any{
					"name":         t.Name,
					"description":  t.Description,
					"input_schema": t.Schema,
				})
			}
			tools[len(tools)-1]["cache_control"] = map[string]any{"type": "ephemeral"}
			body["tools"] = tools
		}

		raw, _ := json.Marshal(body)
		headers := map[string]string{
			"x-api-key":         a.apiKey,
			"anthropic-version": "2023-06-01",
			"content-type":      "application/json",
		}
		if req.Schema != nil {
			headers["anthropic-beta"] = "output-json-encoding-2025-01-31"
		}

		type antTool struct {
			ID   string
			Name string
			Args []byte
		}
		toolBlocks := make(map[int]*antTool)
		var usage engine.Usage
		var stopReason string

		err := a.client.PostSSE(ctx, "https://api.anthropic.com/v1/messages", headers, raw,
			func(eventType string, data []byte) error {
				switch eventType {
				case "error":
					var errPayload struct {
						Error struct {
							Message string `json:"message"`
						} `json:"error"`
					}
					if json.Unmarshal(data, &errPayload) == nil && errPayload.Error.Message != "" {
						return fmt.Errorf("anthropic: %s", errPayload.Error.Message)
					}
					return fmt.Errorf("anthropic: SSE error: %s", string(data))

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
							ID:   cbs.Block.ID,
							Name: cbs.Block.Name,
							Args: []byte(cbs.Block.Input),
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
							return fmt.Errorf("stream cancelled")
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
						var input map[string]any
						if err := json.Unmarshal(tb.Args, &input); err != nil {
							input = map[string]any{}
						}
						if !yield(engine.StreamEvent{
							Type:     engine.EventToolCall,
							ToolCall: engine.ToolCall{ID: tb.ID, Name: tb.Name, Input: input},
						}, nil) {
							return fmt.Errorf("stream cancelled")
						}
						delete(toolBlocks, cbs.Index)
					}

				case "message_delta":
					var md struct {
						Delta struct {
							StopReason string `json:"stop_reason"`
						} `json:"delta"`
						Usage struct {
							OutputTokens        int `json:"output_tokens"`
							CacheCreationTokens int `json:"cache_creation_input_tokens"`
						} `json:"usage"`
					}
					if json.Unmarshal(data, &md) == nil {
						stopReason = md.Delta.StopReason
						if md.Usage.OutputTokens > 0 {
							usage.OutputTokens = md.Usage.OutputTokens
						}
						if md.Usage.CacheCreationTokens > 0 {
							usage.CacheWriteTokens = md.Usage.CacheCreationTokens
						}
					}

				case "message_stop":
					if !yield(engine.StreamEvent{Type: engine.EventUsage, Usage: usage}, nil) {
						return fmt.Errorf("stream cancelled")
					}
					if !yield(engine.StreamEvent{Type: engine.EventDone, StopReason: stopReason}, nil) {
						return fmt.Errorf("stream cancelled")
					}

				case "ping":
					// Anthropic sends pings to keep the connection alive.
				}
				return nil
			},
		)
		if err != nil {
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
