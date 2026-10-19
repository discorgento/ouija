package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"strings"
	"time"

	"github.com/discorgento/ouija/engine"
)

// ── OpenAI ─────────────────────────────────────────────────────────────

// OpenAI implements engine.Provider via the OpenAI /chat/completions API.
type OpenAI struct {
	client  *Client
	baseURL string
	apiKey  string
	headers map[string]string // optional extra headers
}

func init() {
	Register("openai", func(apiKey string) engine.Provider { return NewOpenAI(apiKey) })
}

func NewOpenAI(apiKey string) *OpenAI {
	return &OpenAI{
		client:  NewClient(5 * time.Minute),
		baseURL: "https://api.openai.com/v1",
		apiKey:  apiKey,
	}
}

func (o *OpenAI) Name() string { return "openai" }

func (o *OpenAI) Capabilities() engine.Capabilities {
	return engine.Capabilities{StructuredOutput: true}
}

func (o *OpenAI) Chat(ctx context.Context, req engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
	return chatOpenAICompat(ctx, o.client, o.Name(), o.baseURL, o.apiKey, o.headers, req)
}

// ── OpenRouter ──────────────────────────────────────────────────────────

// OpenRouter implements engine.Provider via OpenRouter's /chat/completions
// endpoint (OpenAI-compatible). It sends optional attribution headers.
type OpenRouter struct {
	client  *Client
	baseURL string
	apiKey  string
	headers map[string]string // HTTP-Referer, X-Title
}

func init() {
	Register("openrouter", func(apiKey string) engine.Provider { return NewOpenRouter(apiKey) })
}

func NewOpenRouter(apiKey string) *OpenRouter {
	base := os.Getenv("OPENROUTER_BASE_URL")
	if base == "" {
		base = "https://openrouter.ai/api/v1"
	}
	h := make(map[string]string)
	if v := os.Getenv("OPENROUTER_REFERER"); v != "" {
		h["HTTP-Referer"] = v
	}
	if v := os.Getenv("OPENROUTER_TITLE"); v != "" {
		h["X-Title"] = v
	}
	return &OpenRouter{
		client:  NewClient(5 * time.Minute),
		baseURL: base,
		apiKey:  apiKey,
		headers: h,
	}
}

func (o *OpenRouter) Name() string { return "openrouter" }

func (o *OpenRouter) Capabilities() engine.Capabilities {
	return engine.Capabilities{StructuredOutput: true}
}

func (o *OpenRouter) Chat(ctx context.Context, req engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
	return chatOpenAICompat(ctx, o.client, o.Name(), o.baseURL, o.apiKey, o.headers, req)
}

// ── Ollama ─────────────────────────────────────────────────────────────

// Ollama implements engine.Provider via Ollama's OpenAI-compatible /v1 shim.
type Ollama struct {
	client  *Client
	baseURL string
	apiKey  string
	headers map[string]string
}

func init() {
	Register("ollama", func(string) engine.Provider { return NewOllama() })
}

// NewOllama takes no API key: it talks to a local server and supplies a
// placeholder key of its own, so it is registered as a keyless provider.
func NewOllama() *Ollama {
	base := os.Getenv("OLLAMA_HOST")
	if base == "" {
		base = "http://localhost:11434"
	}
	return &Ollama{
		client:  NewClient(10 * time.Minute),
		baseURL: base + "/v1",
		apiKey:  "ollama",
	}
}

func (o *Ollama) Name() string { return "ollama" }

func (o *Ollama) Capabilities() engine.Capabilities {
	return engine.Capabilities{StructuredOutput: true}
}

func (o *Ollama) Chat(ctx context.Context, req engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
	return chatOpenAICompat(ctx, o.client, o.Name(), o.baseURL, o.apiKey, o.headers, req)
}

// ── Shared OpenAI-compatible implementation ─────────────────────────────

// chatOpenAICompat is shared by OpenAI, OpenRouter, and Ollama.
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
					return fmt.Errorf("%s: %s", name, chunk.Error.Message)
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
						return fmt.Errorf("stream cancelled")
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
					for _, tb := range toolByIndex {
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
							return fmt.Errorf("stream cancelled")
						}
					}
				}
				return nil
			},
		)
		if err != nil {
			yield(engine.StreamEvent{}, err)
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
