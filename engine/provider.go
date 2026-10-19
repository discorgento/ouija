package engine

import (
	"context"
	"iter"
)

// Role of a conversation message.
type Role string

const (
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolDef describes a tool the model may call.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any // JSON schema for the tool input
	Risk        RiskTier       // permission tier (defaults to RiskReadOnly)
}

// RunResult wraps the output of a single Run call, including metadata about
// which tools were invoked during the agent loop.
type RunResult struct {
	Text  string   // the final assistant reply (structured JSON for Schema runs)
	Tools []string // names of tools called during this run (for post-run inspection)
}

// ToolCall is a model's request to invoke a tool.
type ToolCall struct {
	ID    string
	Name  string
	Input map[string]any
}

// Message is one turn in the conversation.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // assistant tool-call requests
	ToolCallID string     // for tool-result messages
}

// Usage reports token consumption for one provider call.
type Usage struct {
	InputTokens      int
	OutputTokens     int
	CacheReadTokens  int
	CacheWriteTokens int
}

// ChatRequest is a single provider invocation.
type ChatRequest struct {
	System   string
	Messages []Message
	Tools    []ToolDef
	Schema   map[string]any // when set, the reply must be this JSON object (native structured output)
	// NoToolCalls declares Tools without letting this call use them: the
	// history references them (an API may reject tool calls and results whose
	// tools are not declared), but the reply must be an answer.
	NoToolCalls bool
	MaxTokens   int
	Model       string
}

// ChatResponse is a single provider reply (used internally by helpers).
type ChatResponse struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason string
	Usage      Usage
}

// ─── Streaming ─────────────────────────────────────────────────────────

// StreamEventType describes the kind of event in a streaming provider call.
type StreamEventType int

const (
	EventText     StreamEventType = iota // text token delta
	EventToolCall                        // complete tool call available
	EventUsage                           // final token usage
	EventDone                            // stream complete (last event)
)

// StreamEvent is yielded by streaming provider calls.
type StreamEvent struct {
	Type       StreamEventType
	Text       string   // EventText — token delta
	ToolCall   ToolCall // EventToolCall — complete tool call
	Usage      Usage    // EventUsage — final token usage
	StopReason string   // EventDone — stop reason
}

// Capabilities describes what a provider supports beyond basic chat.
type Capabilities struct {
	// StructuredOutput when true means the provider natively supports
	// returning structured JSON via response_format / format parameters,
	// eliminating the need for submit_result tool + extractJSON fallback.
	StructuredOutput bool
}

// ModelInfo describes one model a provider offers.
type ModelInfo struct {
	ID            string // what goes in Config.Model
	Label         string // human name, when the provider gives one
	ContextWindow int    // input tokens, when the provider says (0 = unknown)
}

// ModelLister is implemented by a provider that can list its models from its
// own API, so pickers show what it really offers today instead of a list
// written into the code.
type ModelLister interface {
	ListModels(ctx context.Context) ([]ModelInfo, error)
}

// Provider is the model adapter every backend implements.
// Chat returns an iterator that yields stream events and then stops.
// On error, the iterator yields a single (StreamEvent{}, error) pair.
type Provider interface {
	Name() string
	Chat(ctx context.Context, req ChatRequest) iter.Seq2[StreamEvent, error]
	Capabilities() Capabilities
}

// ─── Stream helpers ────────────────────────────────────────────────────

// CollectStream consumes a streaming Chat response and accumulates it into
// a ChatResponse. It optionally calls tokenCb (non-nil) for each text delta,
// enabling real-time forwarding (e.g. to a TUI).
func CollectStream(seq iter.Seq2[StreamEvent, error], tokenCb func(string)) (ChatResponse, error) {
	var resp ChatResponse
	for event, err := range seq {
		if err != nil {
			return resp, err
		}
		switch event.Type {
		case EventText:
			resp.Text += event.Text
			if tokenCb != nil {
				tokenCb(event.Text)
			}
		case EventToolCall:
			resp.ToolCalls = append(resp.ToolCalls, event.ToolCall)
		case EventUsage:
			resp.Usage = event.Usage
		case EventDone:
			resp.StopReason = event.StopReason
		}
	}
	return resp, nil
}
