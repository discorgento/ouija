package chat

import (
	"os"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		return m.handleResize(msg)

	case tea.KeyPressMsg:
		m.lastWasPress = true
		return m.handleKey(msg)

	case tea.KeyReleaseMsg:
		// Some terminals (Kitty) send KeyReleaseMsg INSTEAD of KeyPressMsg
		// for Enter (no press event at all). But most terminals send BOTH
		// press and release. If we already processed a press, ignore the
		// release to avoid double-firing (e.g. approving two tools with
		// one Enter press).
		if m.lastWasPress {
			m.lastWasPress = false
			return m, nil
		}
		// No press was received — this terminal might be Kitty-style.
		// Handle the release as if it were a press.
		k := tea.KeyPressMsg(msg)
		return m.handleKey(k)

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
		// Ignore stale tokens from cancelled requests.
		if !m.waiting {
			return m, nil
		}
		m.partialText += msg.text
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
		m.currentTool = msg.tool + ": " + msg.arg
		m.rebuildContent()
		return m, nil

	case approvalRequestMsg:
		// Auto-reject if the generation was cancelled or the user already
		// rejected this tool. The engine may still send approval requests
		// for subsequent tool calls from the same batch even after
		// cancellation (the p.Send enqueues before ctx.Done() is checked).
		if m.generationCancelled || m.rejectedTools[baseTool(msg.tool)] {
			msg.respCh <- approvalResponseMsg{allowed: false}
			return m, nil
		}

		// Guard against orphaned goroutines: when parallel tools both hit
		// Ask permission, each sends an approvalRequestMsg concurrently.
		// Only the last one stored in m.approvalReq gets a response from
		// the user; all earlier ones would block forever on their respCh.
		// Reject any pending approval before storing the new one.
		if m.approvalReq != nil {
			m.approvalReq.respCh <- approvalResponseMsg{allowed: false, cancelled: true}
		}

		m.approvalReq = &msg
		m.toolCallN++ // sequential counter for "call #2", "#3", etc.
		// Reset the selection: the option list varies per tool (delete_file has
		// four), so a stale index from a longer list would panic on Enter.
		m.approvalIdx = 0
		m.setViewportSize() // approval box is taller → shrink viewport
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

	case editorDoneMsg:
		return m.handleEditorDone(msg)

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
	m.input.SetHeight(min(m.input.Line()+1, m.input.MaxHeight))

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
		pe.respCh <- approvalResponseMsg{allowed: true, skipExecution: true}
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
	m.lines = append(m.lines, line{role: "banner", text: text})
	m.rebuildContent()
	if m.autoScroll {
		m.vp.GotoBottom()
	}
	return m, nil
}
