package chat

import (
	"os"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"

	"charm.land/lipgloss/v2"
	"github.com/discorgento/ouija/internal/termsafe"
)

// ─── Init ─────────────────────────────────────────────────────────────

func (m *model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, tickCmd(), m.spinner.Tick)
}

func tickCmd() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg{}
	})
}

// ─── Update ───────────────────────────────────────────────────────────

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	if len(m.pendingCmds) > 0 { // e.g. a model-list fetch started by a keystroke
		cmds := append(m.pendingCmds, cmd)
		m.pendingCmds = nil
		return next, tea.Batch(cmds...)
	}
	return next, cmd
}

func (m *model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case modelListMsg:
		m.gotModelList(msg)
		return m, nil

	case tea.WindowSizeMsg:
		return m.handleResize(msg)

	case tea.KeyPressMsg:
		if m.pressed == nil {
			m.pressed = make(map[rune]bool)
		}
		m.pressed[msg.Code] = true
		return m.handleKey(msg)

	case tea.KeyReleaseMsg:
		// Some terminals (Kitty) send KeyReleaseMsg INSTEAD of KeyPressMsg
		// for Enter (no press event at all); most send both. A release whose
		// press was handled is ignored — matched per key, so overlapping keys
		// (press a, press b, release a, release b) never fire twice.
		// Matched on the key code, not String(): releasing Shift before
		// Enter made the release read "enter" (no shift) — unmatched, so it
		// sent the message (Windows console reports every release).
		if m.pressed[msg.Code] {
			delete(m.pressed, msg.Code)
			return m, nil
		}
		return m.handleKey(tea.KeyPressMsg(msg))

	case tea.PasteMsg:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case tea.PasteStartMsg:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case tea.PasteEndMsg:
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	case tea.MouseWheelMsg:
		vp, _ := m.vp.Update(msg)
		m.vp = vp
		evt := msg.Mouse()
		switch {
		case evt.Button == tea.MouseWheelUp:
			// Scrolling up → browsing history → stop auto-follow.
			m.autoScroll = false
		case evt.Button == tea.MouseWheelDown && m.vp.ScrollPercent() >= 0.99:
			// Scrolling down at bottom → re-enable auto-follow (same as pgdown).
			m.autoScroll = true
		case evt.Button == tea.MouseWheelDown:
			// Scrolling down but not at bottom → user is manually scanning.
			m.autoScroll = false
		}
		return m, nil

	case spinner.TickMsg:
		if !m.waiting {
			return m, nil
		}
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case chatResponse:
		return m.handleResponse(msg)

	case tokenMsg:
		// Ignore stale tokens: a cancelled generation's, still in flight.
		if !m.waiting || msg.gen != m.gen {
			return m, nil
		}
		m.partialText += termsafe.Clean(msg.text)
		// Estimate output tokens during streaming so the footer updates live.
		m.streamingTokens = len(m.partialText) / 4
		// Rebuild at low frequency (2fps) during streaming —
		// frequent enough for text to appear in chunks, rare enough
		// so SetContent → maxLineWidth does not freeze scrolling.
		// Between rebuilds, the viewport is not touched.
		if time.Since(m.lastRebuild) > 500*time.Millisecond {
			m.rebuildContent()
			m.lastRebuild = time.Now()
		}
		if m.autoScroll {
			m.vp.GotoBottom()
		}
		return m, nil

	case toolProgressMsg:
		// Ignore stale tool progress from cancelled requests.
		if !m.waiting {
			return m, nil
		}
		m.currentTool = termsafe.Clean(msg.tool + ": " + msg.arg)
		m.rebuildContent()
		return m, nil

	case approvalRequestMsg:
		if m.answerStale(msg) {
			return m, nil
		}
		// Parallel tools can ask at once: one prompt on screen, the rest
		// wait their turn (answering them "cancelled" unseen misled the model).
		if m.approvalReq != nil {
			m.approvalQueue = append(m.approvalQueue, msg)
			return m, nil
		}
		m.showApproval(msg)
		return m, nil

	case tickMsg:
		m.tickCount++
		if m.tickCount > 20 {
			// Freeze banner after 10s (500ms × 20); nobody's staring at it.
			return m, nil
		}
		m.hueShift++
		// Refresh welcome banner color cycling on tick only (avoids
		// recomputing the entire banner on every keystroke/token).
		m.welcomeBanner = renderWelcomeBanner(m.hueShift, m.eng.Config().ProjectRoot)
		m.rebuildContent()
		return m, tickCmd()

	case bannerMsg:
		return m.addBanner(msg.text)

	case editorDoneMsg:
		model, cmd := m.handleEditorDone(msg)
		m.nextApproval()
		return model, cmd

	default:
		return m, nil
	}
}

// ─── Resize ───────────────────────────────────────────────────────────

func (m *model) handleResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.setViewportSize()
	return m, nil
}

func (m *model) setViewportSize() {
	m.input.SetWidth(m.width - inputCardFrame)
	// Logical lines, not the cursor row (Line): with the cursor on the first
	// line of a long message the box used to shrink to one line.
	m.input.SetHeight(min(m.input.LineCount(), m.input.MaxHeight))

	var n int
	if m.approvalReq != nil {
		// Render the approval box and measure its actual visual height.
		body := RenderApprovalBox(m.approvalReq, m.toolCallN, m.approvalIdx)
		cardContent := ApprovalCard.Render(body, m.width)
		boxHeight := lipgloss.Height(cardContent)
		n = 4 + boxHeight // header(2) + blank(1) + box(h) + footer(1)
	} else {
		// 4 = header(2) + blank_after_vp(1) + footer(1)
		// +2 = card vertical padding (1 top + 1 bottom)
		n = 4 + m.input.Height() + 2
	}
	if m.waiting {
		n++ // +1 for persistent status line between viewport and input
	}
	if m.picker.Visible {
		n += m.picker.Height()
	} else if m.showCmdList {
		n += len(slashCmds) + 2 // items + padding(1,1) top/bottom
	}

	vpHeight := m.height - n
	if vpHeight < 5 {
		vpHeight = 5
	}

	m.vp.SetWidth(m.width)
	m.vp.SetHeight(vpHeight)
	m.vp.SoftWrap = false

	m.rebuildContent()
	if m.autoScroll {
		m.vp.GotoBottom()
	}
}

// ─── Edit file in external editor ──────────────────────────────────────

// handleEditorDone processes the result of an external editor session.
func (m *model) handleEditorDone(msg editorDoneMsg) (tea.Model, tea.Cmd) {
	pe := m.pendingEdit
	if pe == nil {
		return m, nil
	}
	m.pendingEdit = nil

	// If the editor itself failed (non-zero exit, killed, etc.), cancel.
	if msg.err != nil {
		pe.respCh <- approvalResponseMsg{allowed: false, cancelled: true}
		os.Remove(pe.tmpFile)
		return m.addBanner("○ edit cancelled (editor exited with error)")
	}

	// Read the edited temp file.
	b, err := os.ReadFile(pe.tmpFile)
	if err != nil {
		pe.respCh <- approvalResponseMsg{allowed: false, cancelled: true}
		os.Remove(pe.tmpFile)
		return m.addBanner("○ edit cancelled (could not read result)")
	}
	editedContent := string(b)

	// Write the edited content to the real file.
	if err := os.WriteFile(pe.path, []byte(editedContent), 0o644); err != nil {
		// Not applied: the model must not be told "already applied".
		pe.respCh <- approvalResponseMsg{allowed: false, cancelled: true}
		os.Remove(pe.tmpFile)
		return m.addBanner("⚠ wrote to temp but could not write file: " + err.Error())
	}

	os.Remove(pe.tmpFile)

	// Notify engine that the edit was already applied externally —
	// skipExecution tells ToolRunner to not run the tool again.
	pe.respCh <- approvalResponseMsg{allowed: true, skipExecution: true}

	m.lines = append(m.lines, line{
		role: "banner",
		text: "✓ edit applied from editor",
	})
	// Don't set m.waiting = false — the engine is still running.
	m.setViewportSize()
	m.rebuildContent()
	if m.autoScroll {
		m.vp.GotoBottom()
	}
	return m, nil
}

// addBanner appends a banner message and rebuilds the view.
func (m *model) addBanner(text string) (tea.Model, tea.Cmd) {
	m.lines = append(m.lines, line{role: "banner", text: termsafe.Clean(text)})
	m.rebuildContent()
	if m.autoScroll {
		m.vp.GotoBottom()
	}
	return m, nil
}
