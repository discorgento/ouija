package chat

import (
	"context"
	"testing"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
)

// newApprovalTestModel builds the smallest model that can receive an approval
// request: the selection state plus the widgets setViewportSize touches.
func newApprovalTestModel() *model {
	return &model{
		vp:            viewport.New(viewport.WithWidth(80), viewport.WithHeight(20)),
		input:         newPaddedInput(), // zero-value textarea has a nil inner viewport
		width:         80,
		height:        24,
		picker:        &Picker{},
		rejectedTools: map[string]bool{},
	}
}

// A stale arrow-key selection must not survive into the next approval. The
// option lists differ in length (delete_file has four, most tools five), so an
// index left at the end of a longer list used to make the Enter handler panic
// with "index out of range" on the shorter one.
func TestApprovalIndexResetsOnNewRequest(t *testing.T) {
	tools := []string{
		"delete_file\x00/tmp/x", // four options
		"bash\x00git status",    // five options
	}
	for _, tool := range tools {
		m := newApprovalTestModel()
		m.approvalIdx = len(approvalOptions) - 1 // as if the user selected "Cancel"
		ch := make(chan approvalResponseMsg, 1)

		m.Update(approvalRequestMsg{tool: tool, path: "p", respCh: ch})

		if m.approvalIdx != 0 {
			t.Fatalf("tool %q: approvalIdx = %d after a new request, want 0", tool, m.approvalIdx)
		}
		if opts := approvalOptionsFor(tool); m.approvalIdx >= len(opts) {
			t.Fatalf("tool %q: index %d out of range (len=%d)", tool, m.approvalIdx, len(opts))
		}
	}
}

// Enter on the first option must not panic right after a stale selection — the
// exact sequence that crashed before the reset was added.
func TestApprovalEnterAfterStaleIndex(t *testing.T) {
	m := newApprovalTestModel()
	m.approvalIdx = len(approvalOptions) - 1
	ch := make(chan approvalResponseMsg, 1)
	m.Update(approvalRequestMsg{tool: "delete_file\x00/tmp/x", path: "p", respCh: ch})

	if _, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter}); m.approvalReq != nil {
		t.Fatal("approvalReq must be cleared after Enter")
	}
	select {
	case resp := <-ch:
		if !resp.allowed {
			t.Fatalf("first option should approve, got %+v", resp)
		}
	default:
		t.Fatal("Enter produced no response on the approval channel")
	}
}

// Arrow navigation must stay inside the (possibly shorter) option list.
func TestApprovalArrowStaysInRange(t *testing.T) {
	m := newApprovalTestModel()
	ch := make(chan approvalResponseMsg, 1)
	m.Update(approvalRequestMsg{tool: "delete_file\x00/tmp/x", path: "p", respCh: ch})

	opts := approvalOptionsFor("delete_file\x00/tmp/x")
	for range len(opts) + 3 { // wrap around more than once
		m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
		if m.approvalIdx < 0 || m.approvalIdx >= len(opts) {
			t.Fatalf("approvalIdx = %d out of range (len=%d)", m.approvalIdx, len(opts))
		}
	}
	for range len(opts) + 3 {
		m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
		if m.approvalIdx < 0 || m.approvalIdx >= len(opts) {
			t.Fatalf("approvalIdx = %d out of range after left (len=%d)", m.approvalIdx, len(opts))
		}
	}
}

// Letter keys answer only with the options the prompt shows: a delete takes a
// fresh "d" — "y" and "s" (approve for the whole session) are not offered.
func TestApprovalLettersFollowTheOptions(t *testing.T) {
	m := newApprovalTestModel()
	ch := make(chan approvalResponseMsg, 1)
	m.Update(approvalRequestMsg{tool: "delete_file\x00/tmp/x", path: "p", respCh: ch})
	for _, k := range []string{"y", "s"} {
		m.Update(tea.KeyPressMsg{Code: rune(k[0]), Text: k})
		if m.approvalReq == nil || len(ch) != 0 {
			t.Fatalf("%q answered a delete prompt that does not offer it", k)
		}
	}
	m.Update(tea.KeyPressMsg{Code: 'd', Text: "d"})
	if r := <-ch; !r.allowed || r.forSession {
		t.Fatalf("d = %+v; want a one-off approval", r)
	}

	m.Update(approvalRequestMsg{tool: "bash\x00ls", path: "p", respCh: ch})
	m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	if r := <-ch; !r.allowed || !r.forSession {
		t.Fatalf("s on bash = %+v; want approve for session", r)
	}
}

// The input box fits the message's lines, wherever the cursor is.
func TestInputHeightFollowsLineCount(t *testing.T) {
	m := newApprovalTestModel()
	m.input.SetValue("one\ntwo\nthree")
	m.input.MoveToBegin() // cursor on the first line
	m.setViewportSize()
	if h := m.input.Height(); h != 3 {
		t.Fatalf("input height = %d with 3 lines, want 3", h)
	}
	m.input.Reset()
	m.setViewportSize()
	if h := m.input.Height(); h != 1 {
		t.Fatalf("empty input height = %d, want 1", h)
	}
}

// Two prompts at once (parallel tools): both reach the user, in turn; neither
// is answered "cancelled" unseen. A stale one (its run cancelled) is answered
// without being shown.
func TestApprovalQueue(t *testing.T) {
	m := newApprovalTestModel()
	a, b := make(chan approvalResponseMsg, 1), make(chan approvalResponseMsg, 1)
	m.Update(approvalRequestMsg{tool: "bash\x00one", respCh: a})
	m.Update(approvalRequestMsg{tool: "bash\x00two", respCh: b})
	if len(a) != 0 || m.approvalReq == nil || m.approvalReq.tool != "bash\x00one" {
		t.Fatal("the first prompt was not kept on screen")
	}
	m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	if r := <-a; !r.allowed {
		t.Fatalf("first answer = %+v", r)
	}
	if m.approvalReq == nil || m.approvalReq.tool != "bash\x00two" {
		t.Fatal("the queued prompt was not shown next")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := make(chan approvalResponseMsg, 1)
	m.approvalReq = nil
	m.Update(approvalRequestMsg{ctx: ctx, tool: "bash\x00late", respCh: c})
	if r := <-c; r.allowed || !r.cancelled || m.approvalReq != nil {
		t.Fatalf("stale prompt: answer=%+v shown=%v", r, m.approvalReq != nil)
	}
}

// A cancelled generation's late tokens do not join the next reply.
func TestStaleTokensDropped(t *testing.T) {
	m := newApprovalTestModel()
	m.waiting = true
	m.gen = 2
	m.Update(tokenMsg{text: "old", gen: 1})
	m.Update(tokenMsg{text: "new", gen: 2})
	if m.partialText != "new" {
		t.Fatalf("partialText = %q, want only the current generation's", m.partialText)
	}
}

// Overlapping keys fire once each; a release with no press (Kitty Enter)
// still fires.
func TestKeyReleasePairing(t *testing.T) {
	m := newApprovalTestModel()
	m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	m.Update(tea.KeyReleaseMsg{Code: 'a', Text: "a"})
	m.Update(tea.KeyReleaseMsg{Code: 'b', Text: "b"})
	if got := m.input.Value(); got != "ab" {
		t.Fatalf("input = %q, want ab (no doubled key)", got)
	}
	m.Update(tea.KeyReleaseMsg{Code: 'c', Text: "c"})
	if got := m.input.Value(); got != "abc" {
		t.Fatalf("input = %q, want a release-only key handled", got)
	}
}
