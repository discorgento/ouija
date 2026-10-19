package providers

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"iter"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/discorgento/ouija/engine"
)

// OpenAICompat implements engine.Provider for any OpenAI-compatible API
// (/chat/completions, /models). OpenAI, OpenRouter, OpenCode Zen and Ollama
// are this one type with different endpoints, headers and key rules.
type OpenAICompat struct {
	name       string
	client     *Client
	baseURL    string
	apiKey     string
	headers    map[string]string // extra headers (e.g. OpenRouter attribution)
	requireKey bool              // fail fast with an AUTH error when no key is set
}

func (o *OpenAICompat) Name() string { return o.name }

func (o *OpenAICompat) Capabilities() engine.Capabilities {
	return engine.Capabilities{StructuredOutput: true}
}

// errNoKey is the AUTH error for a provider that needs a key and has none.
func (o *OpenAICompat) errNoKey() error {
	return engine.ErrAuth(o.name + ": API key not set — run `ouija setup`")
}

func (o *OpenAICompat) Chat(ctx context.Context, req engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
	if o.requireKey && o.apiKey == "" {
		return func(yield func(engine.StreamEvent, error) bool) { yield(engine.StreamEvent{}, o.errNoKey()) }
	}
	return chatOpenAICompat(ctx, o.client, o.name, o.baseURL, o.apiKey, o.headers, req)
}

// ListModels lists the models of the API (for Ollama, the ones installed).
func (o *OpenAICompat) ListModels(ctx context.Context) ([]engine.ModelInfo, error) {
	if o.requireKey && o.apiKey == "" {
		return nil, o.errNoKey()
	}
	return listOpenAICompat(ctx, o.client, o.name, o.baseURL, o.apiKey, o.headers)
}

func init() {
	Register("openai", func(apiKey string) engine.Provider { return NewOpenAI(apiKey) })
	Register("openrouter", func(apiKey string) engine.Provider { return NewOpenRouter(apiKey) })
	Register("ollama", func(string) engine.Provider { return NewOllama() })
}

// NewOpenAI talks to the OpenAI API.
func NewOpenAI(apiKey string) *OpenAICompat {
	return &OpenAICompat{name: "openai", client: NewClient(5 * time.Minute),
		baseURL: "https://api.openai.com/v1", apiKey: apiKey, requireKey: true}
}

// NewOpenRouter talks to OpenRouter (OPENROUTER_BASE_URL overrides the
// endpoint; OPENROUTER_REFERER / OPENROUTER_TITLE set attribution headers).
func NewOpenRouter(apiKey string) *OpenAICompat {
	h := map[string]string{}
	if v := os.Getenv("OPENROUTER_REFERER"); v != "" {
		h["HTTP-Referer"] = v
	}
	if v := os.Getenv("OPENROUTER_TITLE"); v != "" {
		h["X-Title"] = v
	}
	return &OpenAICompat{name: "openrouter", client: NewClient(5 * time.Minute),
		baseURL: cmp.Or(os.Getenv("OPENROUTER_BASE_URL"), "https://openrouter.ai/api/v1"),
		apiKey:  apiKey, headers: h, requireKey: true}
}

// NewOllama talks to a local Ollama server (OLLAMA_HOST overrides it). It
// takes no API key — it is registered as a keyless provider.
func NewOllama() *OpenAICompat {
	return &OpenAICompat{name: "ollama", client: NewClient(10 * time.Minute),
		baseURL: cmp.Or(os.Getenv("OLLAMA_HOST"), "http://localhost:11434") + "/v1", apiKey: "ollama"}
}

// ── Shared OpenAI-compatible implementation ─────────────────────────────

// chatOpenAICompat implements OpenAICompat.Chat.
// It streams from /chat/completions and yields text deltas, tool calls,
// usage, and a done event.
func chatOpenAICompat(ctx context.Context, client *Client, name, baseURL, apiKey string, extraHeaders map[string]string, req engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
	return func(yield func(engine.StreamEvent, error) bool) {
		body := map[string]any{
			"model":          req.Model,
			"messages":       oaiMessages(req.System, req.Messages),
			"stream":         true,
			"stream_options": map[string]any{"include_usage": true},
		}
		if req.MaxTokens > 0 {
			body["max_tokens"] = req.MaxTokens
		}
		if req.Schema != nil {
			body["response_format"] = map[string]any{
				"type": "json_schema",
				"json_schema": map[string]any{
					"name":   "response",
					"schema": req.Schema,
				},
			}
		} else if len(req.Tools) > 0 {
			var tools []map[string]any
			for _, t := range req.Tools {
				tools = append(tools, map[string]any{
					"type": "function",
					"function": map[string]any{
						"name":        t.Name,
						"description": t.Description,
						"parameters":  t.Schema,
					},
				})
			}
			body["tools"] = tools
		}

		raw, _ := json.Marshal(body)
		headers := map[string]string{
			"content-type":  "application/json",
			"authorization": "Bearer " + apiKey,
		}
		for k, v := range extraHeaders {
			headers[k] = v
		}

		type oaiTool struct {
			ID   string
			Name string
			Args strings.Builder
		}
		toolByIndex := make(map[int]*oaiTool)
		var usage engine.Usage
		var stopReason string

		err := client.PostSSE(ctx, baseURL+"/chat/completions", headers, raw,
			func(eventType string, data []byte) error {
				if string(data) == "[DONE]" {
					return nil
				}

				var chunk struct {
					Choices []struct {
						Index int `json:"index"`
						Delta struct {
							Content   *string `json:"content"`
							ToolCalls []struct {
								Index    int    `json:"index"`
								ID       string `json:"id"`
								Type     string `json:"type"`
								Function struct {
									Name      string `json:"name"`
									Arguments string `json:"arguments"`
								} `json:"function"`
							} `json:"tool_calls"`
						} `json:"delta"`
						FinishReason *string `json:"finish_reason"`
					} `json:"choices"`
					Usage *struct {
						PromptTokens     int `json:"prompt_tokens"`
						CompletionTokens int `json:"completion_tokens"`
					} `json:"usage"`
					Error *struct {
						Message string `json:"message"`
					} `json:"error"`
				}
				if err := json.Unmarshal(data, &chunk); err != nil {
					return nil
				}
				if chunk.Error != nil && chunk.Error.Message != "" {
					return engine.ErrProvider(name + ": " + chunk.Error.Message)
				}

				if chunk.Usage != nil {
					usage = engine.Usage{
						InputTokens:  chunk.Usage.PromptTokens,
						OutputTokens: chunk.Usage.CompletionTokens,
					}
				}

				if len(chunk.Choices) == 0 {
					return nil
				}
				choice := chunk.Choices[0]

				if choice.Delta.Content != nil {
					if !yield(engine.StreamEvent{Type: engine.EventText, Text: *choice.Delta.Content}, nil) {
						return errStopped
					}
				}

				for _, tc := range choice.Delta.ToolCalls {
					if tc.ID != "" {
						toolByIndex[tc.Index] = &oaiTool{
							ID:   tc.ID,
							Name: tc.Function.Name,
						}
					}
					if tc.Function.Arguments != "" {
						if tb, ok := toolByIndex[tc.Index]; ok {
							tb.Args.WriteString(tc.Function.Arguments)
						}
					}
				}

				if choice.FinishReason != nil {
					stopReason = *choice.FinishReason
					// In index order, the order the model made the calls (a map
					// ranges at random), and once: a second finish_reason chunk
					// must not emit them again.
					for _, idx := range slices.Sorted(maps.Keys(toolByIndex)) {
						tb := toolByIndex[idx]
						var input map[string]any
						args := tb.Args.String()
						if args != "" {
							_ = json.Unmarshal([]byte(args), &input)
						}
						if input == nil {
							input = map[string]any{}
						}
						if !yield(engine.StreamEvent{
							Type:     engine.EventToolCall,
							ToolCall: engine.ToolCall{ID: tb.ID, Name: tb.Name, Input: input},
						}, nil) {
							return errStopped
						}
					}
					clear(toolByIndex)
				}
				return nil
			},
		)
		if err != nil {
			if !errors.Is(err, errStopped) {
				yield(engine.StreamEvent{}, err)
			}
			return
		}

		if !yield(engine.StreamEvent{Type: engine.EventUsage, Usage: usage}, nil) {
			return
		}
		if !yield(engine.StreamEvent{Type: engine.EventDone, StopReason: stopReason}, nil) {
			return
		}
	}
}

// oaiMessages converts the neutral message list to OpenAI message format,
// with a system message prefixed when system text is non-empty.
func oaiMessages(system string, msgs []engine.Message) []map[string]any {
	var out []map[string]any
	if system != "" {
		out = append(out, map[string]any{"role": "system", "content": system})
	}
	for _, m := range msgs {
		switch m.Role {
		case engine.RoleUser:
			out = append(out, map[string]any{"role": "user", "content": m.Content})
		case engine.RoleAssistant:
			am := map[string]any{"role": "assistant"}
			if m.Content != "" {
				am["content"] = m.Content
			} else {
				am["content"] = nil
			}
			if len(m.ToolCalls) > 0 {
				var tcs []map[string]any
				for _, tc := range m.ToolCalls {
					args, _ := json.Marshal(tc.Input)
					tcs = append(tcs, map[string]any{
						"id":   tc.ID,
						"type": "function",
						"function": map[string]any{
							"name":      tc.Name,
							"arguments": string(args),
						},
					})
				}
				am["tool_calls"] = tcs
			}
			out = append(out, am)
		case engine.RoleTool:
			out = append(out, map[string]any{
				"role": "tool", "tool_call_id": m.ToolCallID, "content": m.Content,
			})
		}
	}
	return out
}
