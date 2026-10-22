🔮 ouija docs · [Index](../index.md)

---

# Adding a provider

> How to add a provider, the backend that sends ouija's requests to an LLM
> API. Audience: developer

## The Provider interface

```go
type Provider interface {
    Name() string
    Chat(ctx context.Context, req ChatRequest) iter.Seq2[StreamEvent, error]
    Capabilities() Capabilities
}
```

- **Name** — a unique identifier (e.g. `"anthropic"`, `"openai"`).
- **Chat** — a streaming call that yields events, then stops.
- **Capabilities** — declares which optional features the provider supports
  (e.g. `StructuredOutput`).

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
        //    - req.NoToolCalls → declare req.Tools (the history refers to
        //      them) but do not let this call use them: the reply is an answer
        //    - req.Schema → the reply must be this JSON object: a native JSON mode
        //      (response_format), or a forced engine.SubmitTool call whose
        //      input_schema is req.Schema (see anthropic.go)
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

`providers/client.go` provides a `*Client`, created with `NewClient(timeout)`.
It retries with backoff on 429 and 5xx responses, includes a circuit breaker
and parses SSE (server-sent events):

```go
// PostJSON POSTs a body and returns (responseBytes, statusCode, error).
func (c *Client) PostJSON(ctx context.Context, url string, headers map[string]string, body []byte) ([]byte, int, error)

// PostSSE POSTs a body and streams SSE events to onEvent.
func (c *Client) PostSSE(ctx context.Context, url string, headers map[string]string, body []byte, onEvent func(eventType string, data []byte) error) error
```

The retry and backoff helpers, which honour `Retry-After`, live in
`providers/http.go`.

### 5. API key plumbing

The registry holds no keys. `app.DiscoverConfig` puts the active provider's
key in `cfg.APIKey`, taking the global config's key unless the project config
overrides it. `app.BuildProvider(name, key)` then passes the key to your
constructor as its `apiKey` argument. A provider that needs no key, such as
`ollama`, ignores the argument and registers with
`Register("ollama", func(string) engine.Provider { … })`. Also add its name to
`Keyless` in `engine/providers/registry.go`, so the setup wizard and
`ouija doctor` do not ask for a key. Keys always come from the
config file, never from the environment.

### 6. Register the provider and its models

The setup wizard and the `/model` picker read their lists from
`internal/models/store.go`. To make the provider appear in `ouija setup` and
the model picker, add an entry to `defaultModels()` with its label and its
models' context windows.

## Existing providers as reference

| File | Protocol |
|---|---|
| `anthropic.go` | Messages API + SSE streaming + prompt caching |
| `openai.go` | `/chat/completions` + SSE; also registers `openrouter` and `ollama` (OpenAI-compatible shims) |
| `opencode.go` | OpenAI-compatible shim (OpenCode Zen) |

`openai.go` is the cleanest example of the streaming pattern.

---

🔮 ouija docs — [Index](../index.md) · [Architecture](../explanation/architecture.md)
