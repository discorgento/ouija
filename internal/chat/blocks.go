package chat

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/termsafe"
	"github.com/discorgento/ouija/internal/theme"
)

// ─── Turn tools block ─────────────────────────────────────────────────

var (
	toolsHead = lipgloss.NewStyle().Foreground(theme.Gray)
	toolOK    = lipgloss.NewStyle().Foreground(theme.Green)
	toolFail  = lipgloss.NewStyle().Foreground(theme.Red)
	toolMeta  = lipgloss.NewStyle().Foreground(theme.Muted)
	toolArg   = lipgloss.NewStyle().Foreground(theme.Silver)
	toolRun   = lipgloss.NewStyle().Foreground(theme.Amber)
)

// renderToolsBlock lists a turn's tool calls, one per line — icon, name,
// target, then ✓ or ✗ and the duration right after the widest target, the
// reason under a failure — under a "◇ N tool calls" header. width is the room
// the lines get; targets are cut to fit.
//
// live renders a turn still running: a call without a duration yet is in
// progress.
func renderToolsBlock(sum engine.TurnSummary, width int, live bool) []string {
	if len(sum.Tools) == 0 {
		return nil
	}
	head := fmt.Sprintf("◇ %d tool call", len(sum.Tools))
	if len(sum.Tools) > 1 {
		head += "s"
	}
	lines := []string{toolsHead.Render(head)}
	tools := sum.Tools
	if live && len(tools) > maxLiveRows {
		// A long turn shows its latest calls; the earlier ones fold.
		lines = append(lines, "  "+toolMeta.Render(fmt.Sprintf("… %d earlier", len(tools)-maxLiveRows)))
		tools = tools[len(tools)-maxLiveRows:]
	}

	nameW, targetW := 0, 0
	for _, t := range sum.Tools {
		nameW = max(nameW, len(t.Name))
		targetW = max(targetW, ansi.StringWidth(termsafe.Clean(t.Target)))
	}
	// The status sits right after the widest target, not at the screen's edge
	// — and one long command does not push every status far right.
	targetW = min(targetW, maxTargetW, max(width-nameW-18, 8))
	for _, t := range tools {
		c := approvalToolColor(t.Name)
		name := lipgloss.NewStyle().Bold(true).Foreground(c).Render(fmt.Sprintf("%s %-*s", approvalToolIcon(t.Name), nameW, t.Name))
		target := ansi.Truncate(termsafe.Clean(t.Target), targetW, "…")
		target = toolArg.Render(target) + strings.Repeat(" ", targetW-ansi.StringWidth(target))
		status := toolOK.Render("✓")
		switch {
		case t.Error != "":
			status = toolFail.Render("✗")
		case live && t.Duration == 0:
			status = toolRun.Render("⋯")
		}
		if t.Duration > 0 {
			status += " " + toolMeta.Render(formatDuration(t.Duration))
		} else if live && t.Error == "" {
			status += " " + toolRun.Render("running")
		}
		lines = append(lines, ansi.Truncate("  "+name+"  "+target+"  "+status, width, "…"))
		if t.Error != "" {
			reason, _, _ := strings.Cut(termsafe.Clean(t.Error), "\n")
			lines = append(lines, "    "+toolFail.Render(ansi.Truncate("└ "+reason, width-6, "…")))
		}
	}
	return lines
}

// maxTargetW caps the target column of the tools list; maxLiveRows is how
// many calls the live list shows.
const (
	maxTargetW  = 48
	maxLiveRows = 8
)

// renderLiveSummary is a running turn's tools on one line: how many, how many
// are done, failed, running or waiting for an approval.
func renderLiveSummary(sum engine.TurnSummary, awaiting bool, width int) string {
	if len(sum.Tools) == 0 {
		return ""
	}
	done, failed, running := 0, 0, 0
	for _, t := range sum.Tools {
		switch {
		case t.Error != "":
			failed++
		case t.Duration > 0:
			done++
		default:
			running++
		}
	}
	noun := "tools"
	if len(sum.Tools) == 1 {
		noun = "tool"
	}
	line := toolsHead.Render(fmt.Sprintf("◇ %d %s", len(sum.Tools), noun))
	if done > 0 {
		line += toolMeta.Render(" · ") + toolOK.Render(fmt.Sprintf("%d ✓", done))
	}
	if failed > 0 {
		line += toolMeta.Render(" · ") + toolFail.Render(fmt.Sprintf("%d ✗", failed))
	}
	if running > 0 {
		what := "running"
		if awaiting {
			what = "awaiting your approval"
		}
		line += toolMeta.Render(" · ") + toolRun.Render(fmt.Sprintf("%d %s", running, what))
	}
	return ansi.Truncate(line+toolMeta.Render("   Ctrl+T details"), width, "…")
}

// renderToolsSummary is a finished turn's tools on one line: the call itself
// when there was one, else the tools by count, and what failed — Ctrl+T shows
// them one per line.
func renderToolsSummary(sum engine.TurnSummary, width int) string {
	if len(sum.Tools) == 0 {
		return ""
	}
	var failed []string
	counts := map[string]int{}
	var order []string
	for _, t := range sum.Tools {
		if counts[t.Name] == 0 {
			order = append(order, t.Name)
		}
		counts[t.Name]++
		if t.Error != "" {
			failed = append(failed, t.Name)
		}
	}
	var what string
	if len(sum.Tools) == 1 {
		t := sum.Tools[0]
		what = lipgloss.NewStyle().Foreground(approvalToolColor(t.Name)).Render(approvalToolIcon(t.Name)+" "+t.Name) +
			" " + toolArg.Render(termsafe.Clean(t.Target))
	} else {
		parts := make([]string, len(order))
		for i, n := range order {
			parts[i] = lipgloss.NewStyle().Foreground(approvalToolColor(n)).Render(n)
			if c := counts[n]; c > 1 {
				parts[i] += toolMeta.Render(fmt.Sprintf(" ×%d", c))
			}
		}
		what = toolsHead.Render(fmt.Sprintf("%d tools ", len(sum.Tools))) + strings.Join(parts, toolMeta.Render(", "))
	}
	status := toolOK.Render("✓")
	if len(failed) > 0 {
		status = toolFail.Render(fmt.Sprintf("✗ %d failed", len(failed)))
	}
	line := toolsHead.Render("◇ ") + what + "  " + status + toolMeta.Render("   Ctrl+T details")
	return ansi.Truncate(line, width, "…")
}

// formatDuration renders a tool or turn time: 12ms, 3.2s, 1m05s.
func formatDuration(d time.Duration) string {
	switch {
	case d < time.Millisecond:
		return "<1ms"
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// ─── Diff block ───────────────────────────────────────────────────────

var hunkHeader = regexp.MustCompile(`@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

var (
	diffFrame   = lipgloss.NewStyle().Foreground(theme.Muted)
	diffPath    = lipgloss.NewStyle().Bold(true).Foreground(theme.Lavender)
	diffAddN    = lipgloss.NewStyle().Bold(true).Foreground(theme.Green)
	diffDelN    = lipgloss.NewStyle().Bold(true).Foreground(theme.Red)
	diffAddLine = lipgloss.NewStyle().Foreground(lipgloss.Color("#afffaf")).Background(theme.DiffAddBg)
	diffDelLine = lipgloss.NewStyle().Foreground(lipgloss.Color("#ffafc0")).Background(theme.DiffDelBg)
	diffCtxLine = lipgloss.NewStyle().Foreground(theme.Gray)
	diffHunkSep = lipgloss.NewStyle().Foreground(theme.Amber).Faint(true)
)

// diffStats counts the added and removed lines of a diff from
// engine.BuildDiffSnippet.
func diffStats(diff string) (added, removed int) {
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "  +"):
			added++
		case strings.HasPrefix(l, "  -"):
			removed++
		}
	}
	return added, removed
}

// renderDiffBlock frames a diff from engine.BuildDiffSnippet at the given
// width: the file and its +N −M on the top border, line numbers in a gutter,
// removed and added lines on a red and a green background. Lines too long
// for the frame are cut.
func renderDiffBlock(path, diff string, width int) string {
	width = max(width, 24)
	inner := width - 4 // "│ " … " │"
	added, removed := diffStats(diff)

	stats := diffAddN.Render(fmt.Sprintf("+%d", added)) + " " + diffDelN.Render(fmt.Sprintf("−%d", removed))
	title := "✎ " + termsafe.Clean(path)
	if path == "" {
		title = "✎ diff"
	}
	title = ansi.Truncate(title, max(inner-lipgloss.Width(stats)-6, 4), "…")
	// "╭─ " title " " fill " " stats " ─╮": 8 cells besides title, fill, stats.
	fill := max(width-lipgloss.Width(title)-lipgloss.Width(stats)-8, 1)
	var b strings.Builder
	b.WriteString(diffFrame.Render("╭─ ") + diffPath.Render(title) + " " +
		diffFrame.Render(strings.Repeat("─", fill)) + " " + stats + diffFrame.Render(" ─╮") + "\n")

	side := diffFrame.Render("│")
	row := func(content string) {
		b.WriteString(side + " " + content + " " + side + "\n")
	}
	oldN, newN := 0, 0
	for _, l := range strings.Split(strings.TrimRight(diff, "\n"), "\n") {
		if m := hunkHeader.FindStringSubmatch(l); m != nil {
			oldN, _ = strconv.Atoi(m[1])
			newN, _ = strconv.Atoi(m[2])
			row(diffHunkSep.Render(padRight("⋯ "+strings.TrimSpace(l), inner)))
			continue
		}
		body := strings.ReplaceAll(strings.TrimPrefix(l, "  "), "\t", "    ")
		var num int
		var style lipgloss.Style
		sign := " "
		switch {
		case strings.HasPrefix(body, "-"):
			num, style, sign, body = oldN, diffDelLine, "-", body[1:]
			oldN++
		case strings.HasPrefix(body, "+"):
			num, style, sign, body = newN, diffAddLine, "+", body[1:]
			newN++
		default:
			num, style = newN, diffCtxLine
			oldN++
			newN++
		}
		gutter := toolMeta.Render(fmt.Sprintf("%4d ", num))
		text := padRight(ansi.Truncate(sign+" "+body, inner-5, "…"), inner-5)
		row(gutter + style.Render(text))
	}
	b.WriteString(diffFrame.Render("╰" + strings.Repeat("─", width-2) + "╯"))
	return b.String()
}

// padRight pads plain text with spaces to width cells.
func padRight(s string, width int) string {
	if w := ansi.StringWidth(s); w < width {
		return s + strings.Repeat(" ", width-w)
	}
	return s
}
