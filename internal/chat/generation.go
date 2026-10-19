package chat

import (
	"context"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/termsafe"
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
	m.usdAtTurn = m.usd
	m.liveTools = ""
	m.eng.Tracer().NextTurn()
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
	for _, q := range m.approvalQueue { // their run is gone
		q.respCh <- approvalResponseMsg{allowed: false, cancelled: true}
	}
	m.approvalQueue = nil
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
	m.lines = append(m.lines, line{role: "user", text: next, ts: time.Now().Format("15:04")})
	m.msgCount++
	return m.beginGeneration(next)
}

// answerStale answers, unshown, a request that must not reach the user: its
// generation was cancelled (the late prompt would land in the next turn) or
// the user already rejected that tool in this generation. It reports whether
// it did.
func (m *model) answerStale(req approvalRequestMsg) bool {
	switch {
	case (req.ctx != nil && req.ctx.Err() != nil) || m.generationCancelled:
		req.respCh <- approvalResponseMsg{allowed: false, cancelled: true}
	case m.rejectedTools[baseTool(req.tool)]:
		req.respCh <- approvalResponseMsg{allowed: false}
	default:
		return false
	}
	return true
}

// showApproval puts req on screen.
func (m *model) showApproval(req approvalRequestMsg) {
	m.approvalReq = &req
	m.toolCallN++ // sequential counter for "call #2", "#3", etc.
	// Reset the selection: the option list varies per tool, so a stale index
	// from a longer list would panic on Enter.
	m.approvalIdx = 0
	m.setViewportSize() // approval box is taller → shrink viewport
}

// nextApproval shows the next queued request once the current one is
// answered, skipping (and answering) the ones gone stale meanwhile.
func (m *model) nextApproval() {
	for m.approvalReq == nil && len(m.approvalQueue) > 0 {
		req := m.approvalQueue[0]
		m.approvalQueue = m.approvalQueue[1:]
		if !m.answerStale(req) {
			m.showApproval(req)
			return
		}
	}
	m.setViewportSize()
}

// say appends a timestamped line and re-renders (which also scrolls to the
// bottom when autoScroll is on).
func (m *model) say(role, text string) {
	m.lines = append(m.lines, line{role: role, text: termsafe.Clean(text), ts: time.Now().Format("15:04")})
	m.rebuildContent()
}

// turnGitNotices returns the git-guard notices of the last turn's tool
// results (engine.GitGuardMarker): a command changed .git/hooks or
// .git/config, which git would run outside ouija's checks.
func turnGitNotices(msgs []engine.Message) []string {
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
		if i := strings.Index(msg.Content, engine.GitGuardMarker); i >= 0 {
			notice, _, _ := strings.Cut(msg.Content[i:], "\n")
			out = append(out, notice)
		}
	}
	return out
}

// turnDiff is one file change of a turn: the file and its diff.
type turnDiff struct {
	path string
	diff string
}

// turnDiffs returns the diffs of the write/edit results of the last turn —
// the messages from the last user message on — so a diff shown once is never
// shown again under a later reply.
func turnDiffs(msgs []engine.Message) []turnDiff {
	start := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == engine.RoleUser {
			start = i
			break
		}
	}
	var out []turnDiff
	for _, msg := range msgs[start:] {
		if msg.Role != engine.RoleTool {
			continue
		}
		head, diff, ok := strings.Cut(msg.Content, "\n")
		if !ok || strings.TrimSpace(diff) == "" {
			continue
		}
		// "✓ edited <path>" / "✓ wrote <path> (N bytes)"
		var path string
		if p, found := strings.CutPrefix(head, "✓ edited "); found {
			path = p
		} else if p, found := strings.CutPrefix(head, "✓ wrote "); found {
			if i := strings.LastIndex(p, " ("); i > 0 {
				p = p[:i]
			}
			path = p
		} else {
			continue
		}
		out = append(out, turnDiff{path: path, diff: diff})
	}
	return out
}
