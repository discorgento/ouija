package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/termsafe"
	"github.com/discorgento/ouija/registry"
)

// ─── Totems in the chat (/task) ───────────────────────────────────────

// TotemRunner runs a totem on the chat's engine and returns its result as
// text; onRun hears each model run start and end. Injected by Run (the TUI
// only has EngineAPI); nil disables /task.
type TotemRunner func(ctx context.Context, name string, args registry.Args, onRun engine.RunCallback) (string, error)

// totemRunMsg reports a run of the totem in flight starting or ending.
type totemRunMsg struct {
	ev  engine.RunEvent
	gen int
}

// totemDoneMsg carries the totem's result.
type totemDoneMsg struct {
	text string
	err  error
	gen  int
}

// totemRun is a totem running in the chat: its runs, drawn live.
type totemRun struct {
	name  string
	start time.Time
	runs  []*totemStep
}

// totemStep is one model run of the totem (scout, a pass, reconcile).
type totemStep struct {
	label string
	start time.Time
	dur   time.Duration
	done  bool
	err   string
}

// startTotem runs the named totem on the chat's engine, the description as
// its task, and draws its runs live; Esc stops it.
func (m *model) startTotem(name, desc string) (tea.Model, tea.Cmd) {
	switch {
	case m.runTotem == nil:
		return m.toast("totems are not available here")
	case m.waiting:
		return m.toast("wait for the reply, or Esc to stop it, before /" + name)
	case strings.TrimSpace(desc) == "":
		if t, ok := m.totemCommand("/" + name); ok && t.needsTask {
			return m.toast("say what for: /" + name + " <what>")
		}
	}
	m.lines = append(m.lines, line{role: "user", text: strings.TrimSpace("/" + name + " " + desc), ts: time.Now().Format("15:04")})
	m.msgCount++
	m.waiting = true
	m.responseStart = time.Now()
	m.usdAtTurn = m.usd
	m.partialText = ""
	m.currentTool = ""
	m.totem = &totemRun{name: name, start: time.Now()}
	m.gen++
	gen, ctx, run, send := m.gen, m.chatCtx, m.runTotem, m.totemSink
	m.setViewportSize()
	return m, tea.Batch(m.spinner.Tick, func() tea.Msg {
		text, err := run(ctx, name, registry.Args{Task: desc}, func(ev engine.RunEvent) {
			if send != nil {
				send(totemRunMsg{ev: ev, gen: gen})
			}
		})
		return totemDoneMsg{text: text, err: err, gen: gen}
	})
}

// totemEvent records a run starting or ending.
func (m *model) totemEvent(msg totemRunMsg) {
	if m.totem == nil || msg.gen != m.gen {
		return
	}
	ev := msg.ev
	if !ev.Done {
		m.totem.runs = append(m.totem.runs, &totemStep{label: ev.Label, start: time.Now()})
	} else {
		for i := len(m.totem.runs) - 1; i >= 0; i-- {
			if s := m.totem.runs[i]; s.label == ev.Label && !s.done {
				s.done, s.dur = true, ev.Duration
				if ev.Err != nil {
					s.err = ev.Err.Error()
				}
				break
			}
		}
	}
	m.rebuildStreaming()
}

// totemDone shows the totem's result (or why it failed) in the transcript.
func (m *model) totemDone(msg totemDoneMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.gen || m.totem == nil {
		return m, nil
	}
	name := m.totem.name
	m.totem = nil
	m.waiting = false
	m.lastDuration = time.Since(m.responseStart)
	m.tokens, m.steps, m.usd = m.eng.Spent()
	now := time.Now().Format("15:04")
	if msg.err != nil {
		title, hint := errorHelp(msg.err)
		m.lines = append(m.lines, line{role: "error", text: termsafe.Clean(msg.err.Error()), meta: name + ": " + title, hint: hint, ts: now})
	} else {
		meta := name + " · " + formatDuration(m.lastDuration)
		if cost := m.usd - m.usdAtTurn; cost >= 0.005 {
			meta += fmt.Sprintf(" · $%.2f", cost)
		}
		m.lines = append(m.lines, line{role: "assistant", text: termsafe.Clean(msg.text), ts: now, meta: meta})
	}
	m.setViewportSize()
	return m, m.dispatchQueued()
}

// renderTotemBoard draws the running totem: one line per model run with a
// spinner, then ✓ or ✗ and its time, under the elapsed time.
func (m *model) renderTotemBoard(width int) []string {
	t := m.totem
	head := toolsHead.Render("◆ séance · ") + slashTyped.Render(t.name) + toolsHead.Render(" · "+formatDuration(time.Since(t.start)))
	lines := []string{head}
	if len(t.runs) == 0 {
		return append(lines, "  "+m.spinner.View()+" "+toolMeta.Render("gathering the hands…"))
	}
	labelW := 0
	for _, s := range t.runs {
		labelW = max(labelW, ansi.StringWidth(s.label))
	}
	labelW = min(labelW, 28)
	for _, s := range t.runs {
		var mark, dur string
		switch {
		case !s.done:
			mark, dur = m.spinner.View(), toolRun.Render(formatDuration(time.Since(s.start)))
		case s.err != "":
			mark, dur = toolFail.Render("✗"), toolMeta.Render(formatDuration(s.dur))
		default:
			mark, dur = toolOK.Render("✓"), toolMeta.Render(formatDuration(s.dur))
		}
		label := ansi.Truncate(s.label, labelW, "…")
		row := fmt.Sprintf("  %s %s%s  %s", mark, toolArg.Render(label), strings.Repeat(" ", labelW-ansi.StringWidth(label)), dur)
		if s.err != "" {
			reason, _, _ := strings.Cut(termsafe.Clean(s.err), "\n")
			row += "  " + toolFail.Render(reason)
		}
		lines = append(lines, ansi.Truncate(row, width, "…"))
	}
	return lines
}

// totemInfo is a registered totem as the chat offers it.
type totemInfo struct {
	name, summary string
	needsTask     bool // its input schema requires a task
}

// listTotems returns the registered totems.
func listTotems() []totemInfo {
	var out []totemInfo
	for _, t := range registry.List() {
		info := totemInfo{name: t.Name(), summary: strings.ReplaceAll(t.Summary(), "->", "→")}
		switch r := registry.InputSchema(t)["required"].(type) {
		case []string:
			info.needsTask = slices.Contains(r, "task")
		case []any:
			info.needsTask = slices.Contains(r, any("task"))
		}
		out = append(out, info)
	}
	return out
}

// slashCommands are the chat's slash commands: one per totem first (they
// run a séance right in the chat), then the chat's own.
func slashCommands() []slashCmd {
	var out []slashCmd
	for _, t := range listTotems() {
		arg := " [what]"
		if t.needsTask {
			arg = " <what>"
		}
		out = append(out, slashCmd{"/" + t.name + arg, "totem · " + t.summary})
	}
	return append(out, baseSlashCmds...)
}

// totemCommand resolves a slash command naming a registered totem.
func (m *model) totemCommand(cmd string) (totemInfo, bool) {
	for _, t := range listTotems() {
		if cmd == "/"+t.name {
			return t, true
		}
	}
	return totemInfo{}, false
}
