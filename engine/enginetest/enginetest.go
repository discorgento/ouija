// Package enginetest provides a scripted provider for testing totems and
// other code that drives an engine.Engine, without a network or a model.
package enginetest

import (
	"context"
	"iter"
	"log/slog"
	"slices"
	"sync"

	"github.com/discorgento/ouija/engine"
)

// Reply is one scripted model turn: text, tool calls, or both — or Err, the
// provider failing the call.
type Reply struct {
	Text      string
	ToolCalls []engine.ToolCall
	Err       error
}

// Provider answers each Chat call with Respond(req). It records the requests
// and is safe for concurrent use (totems run passes in parallel).
type Provider struct {
	Respond func(req engine.ChatRequest) Reply

	mu       sync.Mutex
	requests []engine.ChatRequest
}

// Name implements engine.Provider.
func (p *Provider) Name() string { return "enginetest" }

// Capabilities implements engine.Provider: no native structured output, so a
// run with a Schema goes through submit_result like most providers.
func (p *Provider) Capabilities() engine.Capabilities { return engine.Capabilities{} }

// Chat implements engine.Provider.
func (p *Provider) Chat(_ context.Context, req engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
	p.mu.Lock()
	p.requests = append(p.requests, req)
	p.mu.Unlock()
	r := p.Respond(req)
	return func(yield func(engine.StreamEvent, error) bool) {
		if r.Err != nil {
			yield(engine.StreamEvent{}, r.Err)
			return
		}
		if r.Text != "" && !yield(engine.StreamEvent{Type: engine.EventText, Text: r.Text}, nil) {
			return
		}
		for _, tc := range r.ToolCalls {
			if !yield(engine.StreamEvent{Type: engine.EventToolCall, ToolCall: tc}, nil) {
				return
			}
		}
		if !yield(engine.StreamEvent{Type: engine.EventUsage, Usage: engine.Usage{InputTokens: 10, OutputTokens: 5}}, nil) {
			return
		}
		yield(engine.StreamEvent{Type: engine.EventDone, StopReason: "stop"}, nil)
	}
}

// Requests returns the requests seen so far, in arrival order.
func (p *Provider) Requests() []engine.ChatRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return slices.Clone(p.requests)
}

// Submit is the Reply that hands in v as a run's structured result.
func Submit(v map[string]any) Reply {
	return Reply{ToolCalls: []engine.ToolCall{{ID: "submit", Name: engine.SubmitTool, Input: v}}}
}

// HasTool reports whether req offers the named tool.
func HasTool(req engine.ChatRequest, name string) bool {
	return slices.ContainsFunc(req.Tools, func(d engine.ToolDef) bool { return d.Name == name })
}

// NewEngine returns an engine on p whose project root is root.
func NewEngine(p engine.Provider, root string) *engine.Engine {
	cfg := engine.DefaultConfig()
	cfg.ProjectRoot = root
	eng := engine.New(engine.EngineConfig{Provider: p, Config: cfg, Tracer: engine.NewTracer(false)})
	eng.SetLogger(slog.New(slog.DiscardHandler))
	return eng
}
