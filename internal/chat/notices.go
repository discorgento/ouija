package chat

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/providers"
	"github.com/discorgento/ouija/internal/termsafe"
	"github.com/discorgento/ouija/internal/theme"
)

// ─── Toasts ───────────────────────────────────────────────────────────

// toastFor is how long a status-bar notice stays.
const toastFor = 2500 * time.Millisecond

// toast shows text in the status bar for a moment instead of adding it to the
// transcript: confirmations nobody needs to scroll back to.
func (m *model) toast(text string) (tea.Model, tea.Cmd) {
	m.toastID++
	m.toastText = termsafe.Clean(text)
	id := m.toastID
	return m, tea.Tick(toastFor, func(time.Time) tea.Msg { return toastClearMsg{id: id} })
}

// ─── Notification when a long reply ends ──────────────────────────────

// notifyAfter is how long a reply must take before its end is worth a
// desktop notification (when the terminal is in the background).
const notifyAfter = 10 * time.Second

// notifyCmd asks the terminal for a desktop notification: OSC 9 (iTerm2,
// kitty, WezTerm, Windows Terminal) and OSC 777 (foot, GNOME's VTE). A
// terminal that knows neither ignores both.
func notifyCmd(body string) tea.Cmd {
	body = strings.Map(func(r rune) rune { // no control bytes inside the OSC
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, termsafe.Clean(body))
	return tea.Raw("\x1b]9;ouija: " + body + "\x07" + "\x1b]777;notify;ouija;" + body + "\x07")
}

// windowTitle says in the terminal tab what ouija is doing.
func (m *model) windowTitle() string {
	switch {
	case m.approvalReq != nil:
		return "ouija · approval needed"
	case m.waiting:
		return "ouija · thinking…"
	}
	if root := m.eng.Config().ProjectRoot; root != "" {
		return "ouija · " + displayPath(root)
	}
	return "ouija"
}

// ─── Error card ───────────────────────────────────────────────────────

// errorHelp names a failure and says what to do about it.
func errorHelp(err error) (title, hint string) {
	switch {
	case errors.Is(err, providers.ErrCircuitOpen):
		return "the provider keeps failing", "ouija stopped calling it for a moment — wait a bit, then ↑ and Enter to resend"
	case engine.Is(err, engine.ErrCodeAuth):
		return "the API key was refused", "run `ouija setup` to set the key, or `ouija doctor` to see what's wrong"
	case engine.Is(err, engine.ErrCodeBudget):
		return "budget exhausted", "raise budget.maxUSD / maxTokens in .ouija/config.json, or /clear to start over"
	case engine.Is(err, engine.ErrCodeNetwork):
		return "network error", "check the connection (for ollama: that it is running), then ↑ and Enter to resend"
	case engine.Is(err, engine.ErrCodeStream):
		return "the reply broke off", "↑ and Enter to resend the message"
	case engine.Is(err, engine.ErrCodeProvider):
		return "the provider refused the request", "`ouija doctor` checks the provider, key and model"
	case engine.Is(err, engine.ErrCodeConfig):
		return "configuration error", "`ouija doctor` shows what's wrong"
	case errors.Is(err, context.DeadlineExceeded):
		return "the request timed out", "↑ and Enter to resend the message"
	}
	return "something went wrong", "/debug writes the engine log to a file, to see more"
}

var (
	errTitle = lipgloss.NewStyle().Bold(true).Foreground(theme.Red)
	errText  = lipgloss.NewStyle().Foreground(theme.Silver)
	errHint  = lipgloss.NewStyle().Foreground(theme.Amber)
)

// renderErrorBlock renders a failed turn: what failed, the error itself and
// the next step, wrapped to width.
func renderErrorBlock(title, text, hint string, width int) string {
	var b strings.Builder
	b.WriteString(errTitle.Render("✕ "+title) + "\n")
	for _, l := range wrapLines(text, "", width-2) {
		b.WriteString(errText.Render(l) + "\n")
	}
	for i, l := range wrapLines(hint, "  ", width-2) {
		prefix := "→ "
		if i > 0 {
			prefix = ""
		}
		b.WriteString(errHint.Render(prefix+l) + "\n")
	}
	return b.String()
}

// ─── Copy ─────────────────────────────────────────────────────────────

var fencedCode = regexp.MustCompile("(?s)```[^\n]*\n(.*?)\n?```")

// lastCodeBlock returns the last fenced code block of the latest reply that
// has one, without its fences.
func (m *model) lastCodeBlock() string {
	for i := len(m.lines) - 1; i >= 0; i-- {
		if m.lines[i].role != "assistant" {
			continue
		}
		if all := fencedCode.FindAllStringSubmatch(m.lines[i].text, -1); len(all) > 0 {
			return all[len(all)-1][1]
		}
	}
	return ""
}

// handleCopyCode copies the last code block to the clipboard.
func (m *model) handleCopyCode() (tea.Model, tea.Cmd) {
	code := m.lastCodeBlock()
	if code == "" {
		return m.toast("no code block to copy yet")
	}
	if err := clipboard.WriteAll(code); err != nil {
		return m.toast("⚠ copy failed: " + err.Error())
	}
	n := strings.Count(code, "\n") + 1
	return m.toast(fmt.Sprintf("✓ copied the last code block (%d lines)", n))
}

// ─── Live tools ───────────────────────────────────────────────────────

// liveToolsKey sums up the running turn's tool list (calls, finished,
// failed), so the transcript is redrawn only when the list changed.
func liveToolsKey(sum engine.TurnSummary) string {
	done, failed := 0, 0
	for _, t := range sum.Tools {
		if t.Duration > 0 {
			done++
		}
		if t.Error != "" {
			failed++
		}
	}
	return fmt.Sprintf("%d/%d/%d", len(sum.Tools), done, failed)
}
