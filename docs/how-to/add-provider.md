🔮 ouija docs · [Index](../index.md)

---

# Adding a provider

> Implementing a new LLM backend provider. Audience: developer

## The Provider interface

```go
type Provider interface {
    Name() string
    Chat(ctx context.Context, req ChatRequest) iter.Seq2[StreamEvent, error]
    Capabilities() Capabilities
}
```

- **Name** — unique identifier (e.g. `"anthropic"`, `"openai"`)
- **Chat** — streaming call that yields events and then stops
- **Capabilities** — declares optional provider features (e.g. `StructuredOutput`)

## Stream events

| Event | When | Fields |
|---|---|---|
| `EventText` | Token delta | `Text` |
| `EventToolCall` | Complete tool call | `ToolCall{ID, Name, Input}` |
| `EventUsage` | Final token count | `Usage{InputTokens, OutputTokens, ...}` |
| `EventDone` | Stream complete | `StopReason` (e.g. `"end_turn"`, `"stop"`) |

## Step by step

### 1. Create the file

```sh
touch engine/providers/mistral.go
```

### 2. Register in init

```go
package providers

import (
    "context"
    "fmt"
    "iter"
    "github.com/discorgento/ouija/engine"
)

func init() {
    Register("mistral", func(apiKey string) engine.Provider { return NewMistral(apiKey) })
}

type Mistral struct {
    apiKey string
}

func NewMistral(apiKey string) *Mistral {
    return &Mistral{apiKey: apiKey}
}

func (m *Mistral) Name() string { return "mistral" }

func (m *Mistral) Capabilities() engine.Capabilities {
    return engine.Capabilities{} // no native structured output
}
```

### 3. Implement Chat

```go
func (m *Mistral) Chat(ctx context.Context, req engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
    return func(yield func(engine.StreamEvent, error) bool) {
        // 1. Build the request payload from req
        //    - req.System → system message
        //    - req.Messages → conversation history
        //    - req.Tools → tool definitions
        //    - req.Schema → final output schema (add submit_result tool)
        //    - req.MaxTokens → response length cap
        //    - req.Model → model name

        // 2. Send via client.PostSSE or client.PostJSON
        //    (see Step 4); retries/backoff are built in.

        // 3. Stream events via yield
        //    yield(engine.StreamEvent{Type: engine.EventText, Text: token}, nil)
        //    yield(engine.StreamEvent{Type: engine.EventToolCall, ToolCall: ...}, nil)
        //    yield(engine.StreamEvent{Type: engine.EventUsage, Usage: ...}, nil)
        //    yield(engine.StreamEvent{Type: engine.EventDone, StopReason: "stop"}, nil)

        // 4. On error before any event, yield a single error
        //    yield(engine.StreamEvent{}, fmt.Errorf("mistral: ..."))

        // 5. Respect ctx cancellation
        //    if ctx.Err() != nil { return }
    }
}
```

### 4. Use the shared HTTP client

`providers/client.go` provides a `*Client` (created with `NewClient(timeout)`)
that wraps retry/backoff on 429/5xx, a circuit breaker, and SSE parsing:

```go
// PostJSON POSTs a body and returns (responseBytes, statusCode, error).
func (c *Client) PostJSON(ctx context.Context, url string, headers map[string]string, body []byte) ([]byte, int, error)

// PostSSE POSTs a body and streams SSE events to onEvent.
func (c *Client) PostSSE(ctx context.Context, url string, headers map[string]string, body []byte, onEvent func(eventType string, data []byte) error) error
```

Retry/backoff helpers (honouring `Retry-After`) live in `providers/http.go`.

### 5. API key plumbing

The registry holds no keys. `app.DiscoverConfig` resolves the effective key for
the active provider (global config → project override) into `cfg.APIKey`, and
`app.BuildProvider(name, key)` passes it to the constructor. Your constructor
receives the key as its `apiKey` argument — keyless providers (e.g. `ollama`)
ignore it and register with `Register("ollama", func(string) engine.Provider { … })`.
Keys come from the config file, not the environment.

### 6. Register the provider and its models

The setup wizard and the `/model` picker read from `internal/models/store.go`.
Add an entry for the provider (label + models with context windows) to
`defaultModels()` so it appears in `ouija setup` and the model picker.

## Existing providers as reference

| File | Protocol |
|---|---|
| `anthropic.go` | Messages API + SSE streaming + prompt caching |
| `openai.go` | `/chat/completions` + SSE; also registers `openrouter` and `ollama` (OpenAI-compatible shims) |
| `opencode.go` | OpenAI-compatible shim (OpenCode Zen) |

Study `openai.go` for a clean example of the streaming pattern.

---

🔮 ouija docs — [Index](../index.md) · [Architecture](../explanation/architecture.md)
