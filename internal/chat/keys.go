package chat

import (
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/discorgento/ouija/engine"
)

// ─── Key handling ─────────────────────────────────────────────────────

func (m *model) handleKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Use Keystroke() instead of String() — Keystroke() always returns the
	// symbolic key name ("enter", "esc", "left", etc.) regardless of whether
	// the decoder populated Key.Text with the raw rune. String() can return
	// the raw text (e.g. "\r" for Enter) on some terminal configurations,
	// which would silently break key matching.
	key := msg.Keystroke()
	code := msg.Key().Code

	// ── Quit ──────────────────────────────────────────────────────────
	if key == "ctrl+c" || key == "ctrl+d" {
		m.cancel()
		return m, tea.Quit
	}

	// ── Approval prompt active ────────────────────────────────────
	if m.approvalReq != nil {
		// Match critical keys by code (not string) to avoid terminal-
		// specific quirks with String()/Keystroke() for Enter/arrows.
		switch {
		case code == tea.KeyLeft || key == "left":
			opts := approvalOptionsFor(m.approvalReq.tool)
			m.approvalIdx = (m.approvalIdx - 1 + len(opts)) % len(opts)
			m.rebuildContent()
			return m, nil
		case code == tea.KeyRight || key == "right":
			opts := approvalOptionsFor(m.approvalReq.tool)
			m.approvalIdx = (m.approvalIdx + 1) % len(opts)
			m.rebuildContent()
			return m, nil
		case code == tea.KeyEnter || key == "enter":
			return m.chooseApproval(approvalOptionsFor(m.approvalReq.tool)[m.approvalIdx])
		case code == tea.KeyEscape || key == "esc" || key == "escape":
			return m.approvalCancel()
		}
		// A letter picks the option it labels — only the options this prompt
		// shows (delete_file has no "y" or "s": deleting takes a fresh "d").
		for _, opt := range approvalOptionsFor(m.approvalReq.tool) {
			if key == opt.key {
				return m.chooseApproval(opt)
			}
		}
		return m, nil // other keys are ignored during the prompt
	}

	// ── Escape: cancel or clear queue ────────────────────────────────
	// NB: msg.String() returns "esc" (not "escape") in bubbletea v2.
	if key == "esc" || key == "escape" {
		// An open list closes first: Esc on it must not cancel the reply.
		if m.picker.Visible || m.showCmdList {
			m.picker.Hide()
			m.showCmdList = false
			m.setViewportSize()
			return m, nil
		}
		if m.waiting {
			m.cancelGeneration()
			return m, m.dispatchQueued()
		}
		if len(m.pendingQueue) > 0 {
			// Clear queue.
			m.pendingQueue = nil
			m.lines = append(m.lines,
				line{role: "banner", text: "○ queue cleared"},
			)
			m.rebuildContent()
			if m.autoScroll {
				m.vp.GotoBottom()
			}
			return m, nil
		}
		return m, nil
	}

	// ── Copy last assistant response ──────────────────────────────────
	if key == "ctrl+y" {
		return m.handleCopyMessage()
	}

	// ── Input history ────────────────────────────────────────────────
	if key == "alt+up" {
		return m.handleHistoryPrev()
	}
	if key == "alt+down" {
		return m.handleHistoryNext()
	}

	// ── Viewport navigation ─────────────────────────────────────────
	if key == "pgup" || key == "pgdown" || key == "home" || key == "end" {
		vp, cmd := m.vp.Update(msg)
		m.vp = vp
		if key == "pgup" || key == "home" {
			m.autoScroll = false
		} else if key == "pgdown" && m.vp.ScrollPercent() >= 0.99 {
			m.autoScroll = true
		} else if key == "end" {
			m.autoScroll = true
		} else if m.vp.ScrollPercent() < 0.99 {
			m.autoScroll = false
		}
		return m, cmd
	}

	// ── Slash command list / picker navigation ─────────────────────
	if m.picker.Visible {
		if key == "tab" { // complete the highlighted item, and offer the next step
			m.selectCompletion()
			m.input.SetValue(m.input.Value() + " ")
			m.input.CursorEnd()
			m.updateCmdList()
			m.setViewportSize()
			return m, nil
		}
		handled, confirmed := m.picker.HandleKey(key)
		if handled {
			if confirmed {
				m.selectCompletion()
				m.setViewportSize()
				input := strings.TrimSpace(m.input.Value())
				if strings.HasPrefix(input, "/") {
					return m.handleSend()
				}
				return m, nil
			}
			m.setViewportSize()
			return m, nil
		}
		// Key not handled by picker — let text input process it.
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if m.updateCmdList() {
			m.setViewportSize()
		}
		return m, cmd
	}
	if m.showCmdList {
		switch key {
		case "down":
			m.selCmdIdx = (m.selCmdIdx + 1) % len(slashCmds)
			return m, nil
		case "up":
			m.selCmdIdx = (m.selCmdIdx - 1 + len(slashCmds)) % len(slashCmds)
			return m, nil
		case "tab":
			m.selectCompletion()
			m.setViewportSize()
			return m, nil
		case "enter":
			cmdName := strings.Fields(slashCmds[m.selCmdIdx].cmd)[0]
			m.input.SetValue(cmdName)
			m.showCmdList = false
			return m.handleSend()
		case "esc", "escape":
			m.showCmdList = false
			m.setViewportSize()
			return m, nil
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		if m.updateCmdList() {
			m.setViewportSize()
		}
		return m, cmd
	}

	// ── Edit last message on Up with empty input ─────────────────────
	if key == "up" && strings.TrimSpace(m.input.Value()) == "" {
		for i := len(m.lines) - 1; i >= 0; i-- {
			if m.lines[i].role == "user" {
				m.input.SetValue(m.lines[i].text)
				m.setViewportSize()
				break
			}
		}
		return m, nil
	}

	// ── Enter / Shift+Enter / Alt+Enter ─────────────────────────────
	if code == tea.KeyEnter {
		// Shift+Enter or Alt+Enter → insert newline
		if msg.Key().Mod&(tea.ModShift|tea.ModAlt) != 0 {
			m.input.InsertString("\n")
			m.setViewportSize()
			return m, nil
		}
		// Plain Enter → send
		return m.handleSend()
	}

	// ── Route to textarea ────────────────────────────────────────────
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	// Refresh command list when user types (e.g. "/model " → show models).
	if m.updateCmdList() {
		m.setViewportSize()
	}
	return m, cmd
}

// chooseApproval answers the approval prompt with opt.
func (m *model) chooseApproval(opt approvalOpt) (tea.Model, tea.Cmd) {
	switch opt.key {
	case "esc":
		return m.approvalCancel()
	case "e":
		return m.handleApprovalEdit()
	case "n":
		// A rejection blocks the whole tool for this generation, not just
		// this target — the user does not want the agent to keep proposing it.
		m.rejectedTools[baseTool(m.approvalReq.tool)] = true
	}
	m.approvalReq.respCh <- opt.resp
	m.approvalReq = nil
	m.nextApproval() // the next queued prompt, or the normal layout back
	return m, nil
}

// approvalCancel handles escape during the approval prompt:
// cancels the current generation and restores the normal input.
func (m *model) approvalCancel() (tea.Model, tea.Cmd) {
	m.approvalReq.respCh <- approvalResponseMsg{allowed: false, cancelled: true}
	m.approvalReq = nil
	m.cancelGeneration()
	return m, m.dispatchQueued()
}

// handleApprovalEdit opens the proposed file in $EDITOR for manual editing.
func (m *model) handleApprovalEdit() (tea.Model, tea.Cmd) {
	req := m.approvalReq
	if req == nil {
		return m, nil
	}
	p, _ := req.input["path"].(string)
	oldStr, _ := req.input["oldString"].(string)
	newStr, _ := req.input["newString"].(string)
	if p == "" || oldStr == "" {
		// Can't edit — just return.
		return m, nil
	}

	// The sandbox decides where the edit lands, as for the tool itself: a
	// plain Join let `../x` or a symlink out of the project be written by the
	// editor, skipping the escape and protected-path checks.
	ctx := cmp.Or[context.Context](req.ctx, context.Background())
	full, err := engine.SafeResolve(ctx, m.eng.Config().ProjectRoot, p)
	if err != nil {
		return m.addBanner("⚠ cannot edit " + p + ": " + err.Error())
	}

	// Read original content and apply the edit to get proposed content.
	b, err := os.ReadFile(full)
	if err != nil {
		// Can't read file — fall through to show error in banner.
		return m.addBanner("⚠ could not read file: " + err.Error())
	}
	orig := string(b)
	proposed := strings.Replace(orig, oldStr, newStr, 1)

	// Write proposed content to temp file.
	tmpFile, err := os.CreateTemp("", "ouija-edit-*"+filepath.Ext(p)) // the extension keeps syntax highlighting
	if err != nil {
		return m.addBanner("⚠ could not create temp file: " + err.Error())
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.WriteString(proposed); err != nil {
		tmpFile.Close()
		os.Remove(tmpPath)
		return m.addBanner("⚠ could not write temp file: " + err.Error())
	}
	if err := tmpFile.Close(); err != nil {
		os.Remove(tmpPath)
		return m.addBanner("⚠ could not close temp file: " + err.Error())
	}

	// Store pending edit state.
	m.pendingEdit = &pendingEditFile{
		path:    full,
		tmpFile: tmpPath,
		respCh:  req.respCh,
	}

	// Clear the approval state — it will be replaced with an "editing" banner.
	m.approvalReq = nil

	editor := cmp.Or(os.Getenv("EDITOR"), "vi")

	m.lines = append(m.lines, line{
		role: "banner",
		text: fmt.Sprintf("✎ editing in %s (save & close to apply, kill to cancel)", editor),
	})
	m.rebuildContent()
	if m.autoScroll {
		m.vp.GotoBottom()
	}

	return m, tea.ExecProcess(exec.Command(editor, tmpPath), func(err error) tea.Msg {
		return editorDoneMsg{err: err}
	})
}
