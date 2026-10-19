package engine

import (
	"fmt"
	"strings"
	"testing"
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
