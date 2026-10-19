package engine

import (
	"context"
	"io"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
)

// ─── Structured output tests ──────────────────────────────────────

// structuredMock is like mockProvider but declares StructuredOutput capability.
type structuredMock struct {
	responses []ChatResponse
	calls     int
}

func (m *structuredMock) Name() string               { return "struct-mock" }
func (m *structuredMock) Capabilities() Capabilities { return Capabilities{StructuredOutput: true} }
func (m *structuredMock) Chat(ctx context.Context, req ChatRequest) iter.Seq2[StreamEvent, error] {
	return func(yield func(StreamEvent, error) bool) {
		r := m.responses[m.calls]
		m.calls++
		if r.Text != "" {
			if !yield(StreamEvent{Type: EventText, Text: r.Text}, nil) {
				return
			}
		}
		for _, tc := range r.ToolCalls {
			if !yield(StreamEvent{Type: EventToolCall, ToolCall: tc}, nil) {
				return
			}
		}
		if !yield(StreamEvent{Type: EventUsage, Usage: r.Usage}, nil) {
			return
		}
		if !yield(StreamEvent{Type: EventDone, StopReason: r.StopReason}, nil) {
			return
		}
	}
}

// TestEngineStructuredFastPath: structured output with no tools goes straight
// to a single response_format call — no submit_result, no nudges.
func TestEngineStructuredFastPath(t *testing.T) {
	sm := &structuredMock{responses: []ChatResponse{
		{Text: `{"result":"ok"}`, Usage: Usage{InputTokens: 10, OutputTokens: 5}},
	}}
	eng := newTestEngine(sm, t.TempDir())
	eng.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	res, err := eng.Run(context.Background(), RunRequest{
		Schema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"result":"ok"}` {
		t.Fatalf("got %q, want {\"result\":\"ok\"}", res.Text)
	}
	// Fast path should make exactly 1 provider call.
	if sm.calls != 1 {
		t.Fatalf("expected 1 provider call (fast path), got %d", sm.calls)
	}
	// No tools should be in the metadata.
	if len(res.Tools) != 0 {
		t.Fatalf("expected no tools, got %v", res.Tools)
	}
}

// TestEngineStructuredPhaseTwo: real tools then structured output.
// Phase 1 uses a real tool (read_file) to gather info; Phase 2 returns
// guaranteed JSON via response_format.
func TestEngineStructuredPhaseTwo(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "data.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	sm := &structuredMock{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: "read_file", Input: map[string]any{"path": "data.txt"}}}},
		{Text: "I've read the file, here's my analysis."},
		{Text: `{"result":"final"}`, Usage: Usage{InputTokens: 10, OutputTokens: 5}},
	}}
	eng := newTestEngine(sm, root)
	eng.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	cfg := NewPermissionConfig(Allow)
	eng.SetPermissionConfig(cfg)

	res, err := eng.Run(context.Background(), RunRequest{
		Schema:   map[string]any{"type": "object"},
		UseTools: true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"result":"final"}` {
		t.Fatalf("got %q, want {\"result\":\"final\"}", res.Text)
	}
	// Provider calls: 1 tool turn + 1 done signal + 1 Phase 2 = 3.
	if sm.calls != 3 {
		t.Fatalf("expected 3 provider calls (1 tool + 1 done + 1 structured), got %d", sm.calls)
	}
}

// TestEngineStructuredPhaseTwoNoTools: model outputs text in Phase 1
// (signals done without calling tools), then Phase 2 returns JSON.
func TestEngineStructuredPhaseTwoNoTools(t *testing.T) {
	sm := &structuredMock{responses: []ChatResponse{
		{Text: "I don't need tools for this.", Usage: Usage{InputTokens: 5, OutputTokens: 3}},
		{Text: `{"answer":42}`, Usage: Usage{InputTokens: 5, OutputTokens: 3}},
	}}
	eng := newTestEngine(sm, t.TempDir())
	eng.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	res, err := eng.Run(context.Background(), RunRequest{
		Schema:   map[string]any{"type": "object"},
		UseTools: true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"answer":42}` {
		t.Fatalf("got %q, want {\"answer\":42}", res.Text)
	}
	// 1 for done signal + 1 for Phase 2.
	if sm.calls != 2 {
		t.Fatalf("expected 2 provider calls (1 done + 1 structured), got %d", sm.calls)
	}
}

// The structured-output fallback: model emits JSON as text, no submit_result.
func TestEngineJSONFallback(t *testing.T) {
	mp := &mockProvider{responses: []ChatResponse{
		{Text: `here is the answer {"a":1}`},
	}}
	eng := newTestEngine(mp, t.TempDir())

	res, err := eng.Run(context.Background(), RunRequest{Schema: map[string]any{"type": "object"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"a":1}` {
		t.Fatalf("got %q", res.Text)
	}
}
