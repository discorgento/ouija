package engine

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCompactEmpty(t *testing.T) {
	got := compact(nil) // must not panic
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestCompactFewerThanKeepRecent(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "hello"},
		{Role: RoleAssistant, Content: "hi"},
		{Role: RoleTool, Content: strings.Repeat("x", 1000), ToolCallID: "1"},
	}
	msgs = compact(msgs)
	if len(msgs[2].Content) != 1000 {
		t.Fatalf("expected tool content preserved, got len=%d", len(msgs[2].Content))
	}
}

func TestCompactTrimsOldToolResults(t *testing.T) {
	// 8 messages, last 2 are recent (indices 6-7).
	msgs := make([]Message, 8)
	for i := 0; i < 8; i++ {
		msgs[i] = Message{
			Role:       RoleTool,
			ToolCallID: fmt.Sprintf("%d", i),
			Content:    strings.Repeat("x", 1000), // > 800 → should trim
		}
	}
	msgs = compact(msgs)
	// Indices 0-1 are before the keepRecent window (last 6 of 8) → trimmed.
	for i := 0; i < 2; i++ {
		if len(msgs[i].Content) >= 1000 {
			t.Fatalf("msgs[%d]: expected trimmed, got len=%d", i, len(msgs[i].Content))
		}
		if msgs[i].Content[800:] != "…[compacted]" {
			t.Fatalf("msgs[%d]: expected compaction suffix, got %q", i, msgs[i].Content[800:])
		}
	}
	// Indices 2-7 are within the keepRecent window (last 6) → preserved.
	for i := 2; i < 8; i++ {
		if len(msgs[i].Content) != 1000 {
			t.Fatalf("msgs[%d]: expected preserved, got len=%d", i, len(msgs[i].Content))
		}
	}
}

func TestCompactSkipsShortToolResults(t *testing.T) {
	// 8 messages, each with tool content shorter than compactTrimAt.
	msgs := make([]Message, 8)
	for i := 0; i < 8; i++ {
		msgs[i] = Message{
			Role:       RoleTool,
			ToolCallID: fmt.Sprintf("%d", i),
			Content:    "short",
		}
	}
	msgs = compact(msgs)
	for i := 0; i < 8; i++ {
		if msgs[i].Content != "short" {
			t.Fatalf("msgs[%d]: expected 'short', got %q", i, msgs[i].Content)
		}
	}
}

func TestCompactMixedRoles(t *testing.T) {
	// 10 messages: interleaved user/assistant/tool.
	msgs := make([]Message, 10)
	for i := 0; i < 10; i++ {
		role := RoleUser
		if i%2 == 0 {
			role = RoleTool
		}
		msgs[i] = Message{Role: role, Content: strings.Repeat("x", 1000)}
	}
	msgs = compact(msgs)
	// Only tool-role messages before compactKeepRecent get trimmed.
	for i := 0; i < 4; i++ { // first 4 of 10-6=4 old
		if msgs[i].Role == RoleTool && len(msgs[i].Content) >= 900 {
			t.Fatalf("msgs[%d] (tool): expected trimmed, got len=%d", i, len(msgs[i].Content))
		}
	}
}

func TestCompactSlidingWindowPreservesRecent(t *testing.T) {
	// 100 messages (well over compactMaxMsgs=60).
	msgs := make([]Message, 100)
	for i := 0; i < 100; i++ {
		msgs[i] = Message{Role: RoleUser, Content: fmt.Sprintf("msg-%d", i)}
	}
	got := compact(msgs)
	if len(got) != compactMaxMsgs {
		t.Fatalf("expected %d messages after sliding, got %d", compactMaxMsgs, len(got))
	}
	// The last message should be "msg-99" (most recent preserved).
	if got[compactMaxMsgs-1].Content != "msg-99" {
		t.Fatalf("expected last=msg-99, got %q", got[compactMaxMsgs-1].Content)
	}
	// The first message in the result should be "msg-40" (100-60).
	if got[0].Content != "msg-40" {
		t.Fatalf("expected first=msg-40, got %q", got[0].Content)
	}
}

func TestCompactSlidingWindowExactBoundary(t *testing.T) {
	// Exactly compactMaxMsgs → no change.
	msgs := make([]Message, compactMaxMsgs)
	for i := 0; i < compactMaxMsgs; i++ {
		msgs[i] = Message{Role: RoleUser, Content: fmt.Sprintf("msg-%d", i)}
	}
	got := compact(msgs)
	if len(got) != compactMaxMsgs {
		t.Fatalf("expected %d messages (unchanged), got %d", compactMaxMsgs, len(got))
	}
	if got[0].Content != "msg-0" {
		t.Fatalf("expected first=msg-0, got %q", got[0].Content)
	}
}

func TestCompactSlidingWindowMixedRoles(t *testing.T) {
	// 70 messages with mixed roles — sliding should keep the last 60
	// regardless of role.
	msgs := make([]Message, 70)
	for i := 0; i < 70; i++ {
		role := RoleUser
		if i%3 == 0 {
			role = RoleTool
		}
		msgs[i] = Message{Role: role, Content: fmt.Sprintf("content-%d", i), ToolCallID: fmt.Sprintf("t%d", i)}
	}
	got := compact(msgs)
	if len(got) != compactMaxMsgs {
		t.Fatalf("expected %d messages, got %d", compactMaxMsgs, len(got))
	}
	// First message should be index 10 (70-60).
	if got[0].Content != "content-10" {
		t.Fatalf("expected first=content-10, got %q", got[0].Content)
	}
}

func TestCompactBelowSlidingWindow(t *testing.T) {
	// 10 messages → below both trim and sliding thresholds.
	msgs := make([]Message, 10)
	for i := 0; i < 10; i++ {
		msgs[i] = Message{Role: RoleUser, Content: fmt.Sprintf("msg-%d", i)}
	}
	got := compact(msgs)
	if len(got) != 10 {
		t.Fatalf("expected 10 (untouched), got %d", len(got))
	}
}

// The window must never open on a tool result whose tool call was dropped:
// Anthropic rejects an orphan tool_result.
func TestCompactSlidingWindowNeverStartsOnToolResult(t *testing.T) {
	// user task, then assistant(tool call) + 3 tool results, repeated.
	msgs := []Message{{Role: RoleUser, Content: "task"}}
	for i := 0; len(msgs) < 100; i++ {
		msgs = append(msgs, Message{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: fmt.Sprint(i)}}})
		for range 3 {
			msgs = append(msgs, Message{Role: RoleTool, ToolCallID: fmt.Sprint(i), Content: "r"})
		}
	}
	for n := compactMaxMsgs + 1; n <= len(msgs); n++ {
		got := compact(msgs[:n])
		if len(got) > compactMaxMsgs {
			t.Fatalf("n=%d: window has %d messages, limit %d", n, len(got), compactMaxMsgs)
		}
		if got[0].Role != RoleUser {
			t.Fatalf("n=%d: conversation starts with %q, want user", n, got[0].Role)
		}
		if got[0].Content != "task" {
			t.Fatalf("n=%d: the task was not pinned, first = %q", n, got[0].Content)
		}
		if got[1].Role == RoleTool {
			t.Fatalf("n=%d: orphan tool result right after the pinned task", n)
		}
	}
}

// compact must not rewrite the caller's messages.
func TestCompactDoesNotMutateInput(t *testing.T) {
	msgs := make([]Message, 10)
	for i := range msgs {
		msgs[i] = Message{Role: RoleTool, Content: strings.Repeat("x", 1000)}
	}
	_ = compact(msgs)
	for i, m := range msgs {
		if len(m.Content) != 1000 {
			t.Fatalf("msgs[%d] was modified in place (len=%d)", i, len(m.Content))
		}
	}
}

// assertWellFormed fails when a tool result has no preceding tool call with
// its ID, or a tool call is left without a result — what providers reject.
func assertWellFormed(t *testing.T, msgs []Message) {
	t.Helper()
	open := map[string]bool{}
	for i, m := range msgs {
		switch m.Role {
		case RoleAssistant:
			for _, c := range m.ToolCalls {
				open[c.ID] = true
			}
		case RoleTool:
			if !open[m.ToolCallID] {
				t.Fatalf("msgs[%d]: tool result %q has no matching tool call", i, m.ToolCallID)
			}
			delete(open, m.ToolCallID)
		}
	}
	if len(open) > 0 {
		t.Fatalf("tool calls without a result: %v", open)
	}
}

// A compressed chat turn keeps the edit diffs, and with them the tool calls
// they answer — otherwise the next Chat turn sends orphan tool results.
func TestCompressConversationKeepsEditCallsPaired(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "fix the bug"},
		{Role: RoleAssistant, Content: "Reading.", ToolCalls: []ToolCall{
			{ID: "r1", Name: "read_file"}, {ID: "r2", Name: "grep"},
		}},
		{Role: RoleTool, ToolCallID: "r1", Content: "file contents"},
		{Role: RoleTool, ToolCallID: "r2", Content: "matches"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "e1", Name: "edit_file"}, {ID: "r3", Name: "read_file"},
		}},
		{Role: RoleTool, ToolCallID: "e1", Content: "✓ edited a.go\n-old\n+new"},
		{Role: RoleTool, ToolCallID: "r3", Content: "more contents"},
		{Role: RoleAssistant, Content: "Fixed it."},
	}
	got := compressConversation(msgs)
	assertWellFormed(t, got)

	var diffs, reads int
	for _, m := range got {
		if isEditResult(m) {
			diffs++
		}
		if m.Content == "file contents" || m.Content == "more contents" {
			reads++
		}
	}
	if diffs != 1 || reads != 0 {
		t.Fatalf("want the edit diff kept and reads dropped, got diffs=%d reads=%d: %+v", diffs, reads, got)
	}
	if first, last := got[0], got[len(got)-1]; first.Content != "fix the bug" || last.Content != "Fixed it." {
		t.Fatalf("turn boundaries lost: first=%q last=%q", first.Content, last.Content)
	}
}

// A turn with tools but no edits collapses to question + answer.
func TestCompressConversationDropsReadOnlyTurn(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "what does x do"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "r1", Name: "read_file"}}},
		{Role: RoleTool, ToolCallID: "r1", Content: "contents"},
		{Role: RoleAssistant, Content: "It parses."},
	}
	got := compressConversation(msgs)
	assertWellFormed(t, got)
	if len(got) != 2 {
		t.Fatalf("want question + answer, got %d messages: %+v", len(got), got)
	}
}

// Trimming at a byte offset never splits a multi-byte rune: the cut content is
// resent to the provider, and a broken rune becomes U+FFFD (or a 400).
func TestCompactKeepsUTF8Valid(t *testing.T) {
	msgs := make([]Message, compactKeepRecent+1)
	for i := range msgs {
		msgs[i] = Message{Role: RoleUser, Content: "x"}
	}
	msgs[0] = Message{Role: RoleTool, Content: "a" + strings.Repeat("é", compactTrimAt)} // "é" is 2 bytes; the cut lands mid-rune
	got := compact(msgs)[0].Content
	if !utf8.ValidString(got) || !strings.HasSuffix(got, "…[compacted]") {
		t.Fatalf("compacted content invalid or untrimmed: valid=%v len=%d", utf8.ValidString(got), len(got))
	}
}
