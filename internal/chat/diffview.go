package chat

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// ─── Diff color styles ─────────────────────────────────────────────────

var (
	diffHunk   = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render // @@ header — yellow
	diffDel    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render // - removed — red
	diffAdd    = lipgloss.NewStyle().Foreground(lipgloss.Color("118")).Render // + added — green
	diffDimCtx = lipgloss.NewStyle().Faint(true).Render                       // unchanged context — dim
)

// RenderColoredDiff takes a unified diff string (as produced by
// engine.BuildDiffSnippet) and returns an ANSI-colored version suitable
// for terminal display. Lines are color-coded by their prefix:
//
//	@@ ... @@  → yellow (hunk header)
//	-          → red    (removed)
//	+          → green  (added)
//	 (space)   → dim    (unchanged context)
func RenderColoredDiff(diff string) string {
	if diff == "" {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		if line == "" {
			b.WriteByte('\n')
		} else {
			b.WriteString(colorDiffLine(line))
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func colorDiffLine(line string) string {
	trimmed := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(trimmed, "@@"):
		return diffHunk(line)
	case strings.HasPrefix(line, "  -"):
		rest := strings.TrimPrefix(line, "  -")
		return diffDel("  -" + rest)
	case strings.HasPrefix(line, "  +"):
		rest := strings.TrimPrefix(line, "  +")
		return diffAdd("  +" + rest)
	case strings.HasPrefix(line, "  "):
		return diffDimCtx(line)
	default:
		return line
	}
}
