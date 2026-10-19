package providers

import (
	"context"
	"iter"
	"time"

	"github.com/discorgento/ouija/engine"
)

// ── OpenCode Zen ──────────────────────────────────────────────────────────

// OpenCode implements engine.Provider via OpenCode Zen's /chat/completions
// endpoint (OpenAI-compatible). OpenCode's free tier answers 403
// FreeTierError to any client but the OpenCode app.
type OpenCode struct {
	client  *Client
	baseURL string
	apiKey  string
	headers map[string]string
}

func init() {
	Register("opencode", func(apiKey string) engine.Provider { return NewOpenCode(apiKey) })
}

func NewOpenCode(apiKey string) *OpenCode {
	return &OpenCode{
		client:  NewClient(5 * time.Minute),
		baseURL: "https://opencode.ai/zen/v1",
		apiKey:  apiKey,
	}
}

func (o *OpenCode) Name() string { return "opencode" }

func (o *OpenCode) Capabilities() engine.Capabilities {
	return engine.Capabilities{StructuredOutput: true}
}

func (o *OpenCode) Chat(ctx context.Context, req engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
	return func(yield func(engine.StreamEvent, error) bool) {
		if o.apiKey == "" {
			yield(engine.StreamEvent{}, engine.ErrAuth("opencode: API key not set — run `ouija setup`"))
			return
		}
		for ev, err := range chatOpenAICompat(ctx, o.client, o.Name(), o.baseURL, o.apiKey, o.headers, req) {
			if !yield(ev, err) {
				return
			}
		}
	}
}
