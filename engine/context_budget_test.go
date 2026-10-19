package engine

import (
	"context"
	"fmt"
	"iter"
	"strings"
	"testing"
)

// A tool result is cut to fit a small window, and says so.
func TestClampResult(t *testing.T) {
	big := strings.Repeat("x", 30000)
	if got := clampResult(big, 0); got != big {
		t.Fatal("an unknown window cut the result")
	}
	got := clampResult(big, 4096)
	if len(got) > 4096+200 || !strings.Contains(got, "context window") {
		t.Fatalf("4k window: %d bytes, note present=%v", len(got), strings.Contains(got, "context window"))
	}
}

// The conversation is shrunk to the window: older results first, then the
// oldest messages — the task and the newest turn stay.
func TestFitContext(t *testing.T) {
	big := strings.Repeat("word ", 1200) // ~1.5k tokens each
	msgs := []Message{{Role: RoleUser, Content: "the task"}}
	for i := range 6 {
		id := fmt.Sprint(i)
		msgs = append(msgs,
			Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: id, Name: "read_file"}}},
			Message{Role: RoleTool, ToolCallID: id, Content: big})
	}
	window := 4096
	out := fitContext(msgs, "system", window)
	if n := conversationTokens(out); n > int(float64(window)*historyShare) {
		t.Fatalf("still %d tokens for a %d window", n, window)
	}
	if out[0].Content != "the task" {
		t.Fatalf("the task was dropped: %+v", out[0])
	}
	if last := out[len(out)-1]; last.Content != big {
		t.Fatal("the newest result was cut")
	}
	if got := fitContext(msgs, "system", 0); len(got) != len(msgs) {
		t.Fatal("an unknown window changed the conversation")
	}
}

// A run on a small-window model keeps each request within the window.
func TestRunStaysWithinWindow(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"big.txt": strings.Repeat("line of text\n", 5000)})
	reads := []ChatResponse{}
	for i := range 4 {
		reads = append(reads, ChatResponse{ToolCalls: []ToolCall{{ID: fmt.Sprint(i), Name: "read_file", Input: map[string]any{"path": "big.txt", "offset": float64(i*1000 + 1)}}}})
	}
	reads = append(reads, ChatResponse{Text: "done"})
	p := &recordingProvider{mockProvider: mockProvider{responses: reads}}
	eng := newTestEngine(p, root)
	eng.SetContextWindow(4096)
	if _, err := eng.Run(context.Background(), RunRequest{Prompt: "read it", UseTools: true}); err != nil {
		t.Fatal(err)
	}
	for i, req := range p.requests {
		if n := conversationTokens(req.Messages); n > 4096 {
			t.Fatalf("request %d carried %d tokens into a 4096 window", i, n)
		}
	}
}

// recordingProvider is mockProvider that keeps every request.
type recordingProvider struct {
	mockProvider
	requests []ChatRequest
}

func (p *recordingProvider) Chat(ctx context.Context, req ChatRequest) iter.Seq2[StreamEvent, error] {
	p.requests = append(p.requests, req)
	return p.mockProvider.Chat(ctx, req)
}
