package chat

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/discorgento/ouija/engine"
)

// One place for starting, cancelling and chaining generations: the send,
// Esc, approval-cancel and queue paths used to each carry their own copy, and
// the copies had drifted (a queued message started without the status line,
// a frozen spinner, stale per-turn counters).

// beginGeneration starts the model on input: status line, spinner and fresh
// per-turn counters.
func (m *model) beginGeneration(input string) tea.Cmd {
	m.waiting = true
	m.responseStart = time.Now()
	m.partialText = ""
	m.streamingTokens = 0
	m.setViewportSize() // room for the status line; re-renders
	return tea.Batch(m.startChat(input), m.spinner.Tick)
}

// cancelGeneration stops the response in flight — only its per-generation
// context — and marks it in the transcript. A response still arriving for it
// is ignored (gen).
func (m *model) cancelGeneration() {
	m.generationCancelled = true
	m.chatCancel()
	m.chatCtx, m.chatCancel = context.WithCancel(m.ctx)
	m.waiting = false
	m.partialText = ""
	m.currentTool = ""
	m.gen++
	m.lines = append(m.lines, line{role: "banner", text: "✕ cancelled"})
	m.setViewportSize() // the status line is gone; re-renders
}

// dispatchQueued starts the next queued message; nil when the queue is empty.
func (m *model) dispatchQueued() tea.Cmd {
	if len(m.pendingQueue) == 0 {
		return nil
	}
	next := m.pendingQueue[0]
	m.pendingQueue = m.pendingQueue[1:]
	return m.beginGeneration(next)
}

// say appends a timestamped line and re-renders (which also scrolls to the
// bottom when autoScroll is on).
func (m *model) say(role, text string) {
	m.lines = append(m.lines, line{role: role, text: text, ts: time.Now().Format("15:04")})
	m.rebuildContent()
}

// turnDiffs returns the diffs of the write/edit results of the last turn —
// the messages from the last user message on — so a diff shown once is never
// shown again under a later reply.
func turnDiffs(msgs []engine.Message) []string {
	start := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == engine.RoleUser {
			start = i
			break
		}
	}
	var out []string
	for _, msg := range msgs[start:] {
		if msg.Role != engine.RoleTool {
			continue
		}
		if !strings.HasPrefix(msg.Content, "✓ edited") && !strings.HasPrefix(msg.Content, "✓ wrote") {
			continue
		}
		if idx := strings.IndexByte(msg.Content, '\n'); idx > 0 {
			if diff := msg.Content[idx+1:]; strings.TrimSpace(diff) != "" {
				out = append(out, diff)
			}
		}
	}
	return out
}
