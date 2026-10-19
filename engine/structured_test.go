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

// recordingMock wraps structuredMock and records every ChatRequest, with a
// switchable StructuredOutput capability.
type recordingMock struct {
	structuredMock
	native   bool
	requests []ChatRequest
}

func (m *recordingMock) Capabilities() Capabilities { return Capabilities{StructuredOutput: m.native} }
func (m *recordingMock) Chat(ctx context.Context, req ChatRequest) iter.Seq2[StreamEvent, error] {
	m.requests = append(m.requests, req)
	return m.structuredMock.Chat(ctx, req)
}

// A structured run with tools must offer submit_result to the model, with the
// run's schema as its input schema — prompts tell the model to call it.
func TestStructuredRunExposesSubmitTool(t *testing.T) {
	rm := &recordingMock{native: true, structuredMock: structuredMock{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: SubmitTool, Input: map[string]any{"ok": true}}}},
	}}}
	eng := newTestEngine(rm, t.TempDir())
	schema := map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}}

	res, err := eng.Run(context.Background(), RunRequest{Schema: schema, UseTools: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"ok":true}` {
		t.Fatalf("got %q, want {\"ok\":true}", res.Text)
	}
	var submit *ToolDef
	for i, d := range rm.requests[0].Tools {
		if d.Name == SubmitTool {
			submit = &rm.requests[0].Tools[i]
		}
	}
	if submit == nil {
		t.Fatalf("submit_result not offered; tools = %v", rm.requests[0].Tools)
	}
	if submit.Schema["properties"] == nil {
		t.Fatalf("submit_result schema is not the run schema: %v", submit.Schema)
	}
}

// Phase 2 on a provider that answers structured output with a forced
// submit_result call (Anthropic) must return that call's input, not the
// (empty) text.
func TestStructuredPhaseTwoAcceptsSubmitCall(t *testing.T) {
	rm := &recordingMock{native: true, structuredMock: structuredMock{responses: []ChatResponse{
		{Text: "Done exploring."},
		{ToolCalls: []ToolCall{{ID: "1", Name: SubmitTool, Input: map[string]any{"answer": "x"}}}},
	}}}
	eng := newTestEngine(rm, t.TempDir())

	res, err := eng.Run(context.Background(), RunRequest{Schema: map[string]any{"type": "object"}, UseTools: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"answer":"x"}` {
		t.Fatalf("got %q, want {\"answer\":\"x\"}", res.Text)
	}
	// Phase 2 must not end on an assistant message (prefill is rejected by
	// newer models).
	p2 := rm.requests[1]
	if last := p2.Messages[len(p2.Messages)-1]; last.Role != RoleUser {
		t.Fatalf("phase 2 conversation ends on %q, want user", last.Role)
	}
	if p2.Schema == nil {
		t.Fatal("phase 2 must request native structured output")
	}
	// The history holds tool calls, so the tools stay declared — without
	// letting this call use them (APIs reject undeclared tool calls in history).
	if len(p2.Tools) == 0 || !p2.NoToolCalls {
		t.Fatalf("phase 2 tools=%d noToolCalls=%v; want the tools declared and calls off", len(p2.Tools), p2.NoToolCalls)
	}
}

// Prose from phase 2 is not an answer: the loop nudges back to submit_result
// instead of returning the prose as if it were the JSON.
func TestStructuredPhaseTwoProseIsNudged(t *testing.T) {
	rm := &recordingMock{native: true, structuredMock: structuredMock{responses: []ChatResponse{
		{Text: "Done exploring."},
		{Text: "Here is a summary in words."},
		{ToolCalls: []ToolCall{{ID: "1", Name: SubmitTool, Input: map[string]any{"ok": true}}}},
	}}}
	eng := newTestEngine(rm, t.TempDir())

	res, err := eng.Run(context.Background(), RunRequest{Schema: map[string]any{"type": "object"}, UseTools: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"ok":true}` {
		t.Fatalf("got %q, want {\"ok\":true}", res.Text)
	}
}

// Without native structured output, a prose reply is nudged (no phase 2 call).
func TestStructuredWithoutNativeSupportNudges(t *testing.T) {
	rm := &recordingMock{native: false, structuredMock: structuredMock{responses: []ChatResponse{
		{Text: "Done exploring."},
		{ToolCalls: []ToolCall{{ID: "1", Name: SubmitTool, Input: map[string]any{"ok": true}}}},
	}}}
	eng := newTestEngine(rm, t.TempDir())

	res, err := eng.Run(context.Background(), RunRequest{Schema: map[string]any{"type": "object"}, UseTools: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"ok":true}` {
		t.Fatalf("got %q", res.Text)
	}
	for i, r := range rm.requests {
		if r.Schema != nil {
			t.Fatalf("request %d asked a provider without native support for structured output", i)
		}
	}
}

// After submit_result the history must stay well-formed: the call gets a
// tool result, so a later Chat turn does not send an orphan tool_use.
func TestSubmitCallIsAnsweredInHistory(t *testing.T) {
	sm := &structuredMock{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "s1", Name: SubmitTool, Input: map[string]any{"ok": true}}}},
	}}
	eng := newTestEngine(sm, t.TempDir())
	_, msgs, err := eng.runAgent(context.Background(),
		RunRequest{Schema: map[string]any{"type": "object"}, UseTools: true},
		[]Message{{Role: RoleUser, Content: "go"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	last := msgs[len(msgs)-1]
	if last.Role != RoleTool || last.ToolCallID != "s1" {
		t.Fatalf("submit_result call left unanswered; last message = %+v", last)
	}
}
