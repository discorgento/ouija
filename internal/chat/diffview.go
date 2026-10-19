package chat

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// ─── Diff color styles ─────────────────────────────────────────────────

var (
	diffHunk   = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render // @@ header — yellow
	diffDel    = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render // - removed — red
	diffAdd    = lipgloss.NewStyle().Foreground(lipgloss.Color("118")).Render // + added — green
	diffCtx    = lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render //   context — light gray
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
		trimmed := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trimmed, "@@"):
			b.WriteString(diffHunk(line))
		case strings.HasPrefix(line, "  -"):
			// Old line — render prefix + content in red
			rest := strings.TrimPrefix(line, "  -")
			b.WriteString(diffDel("  -" + rest))
		case strings.HasPrefix(line, "  +"):
			// New line — render prefix + content in green
			rest := strings.TrimPrefix(line, "  +")
			b.WriteString(diffAdd("  +" + rest))
		case strings.HasPrefix(line, "  "):
			// Context line — dim
			b.WriteString(diffDimCtx(line))
		case line == "":
			b.WriteByte('\n')
		default:
			b.WriteString(line)
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// RenderInlineDiff renders a diff summary suitable for embedding in the
// approval prompt. It shows the path, the hunk header, and up to
// maxLines of colored diff. If the diff is longer than maxLines, it is
// truncated and a "… N more lines" indicator is appended.
func RenderInlineDiff(diff string, contentWidth int) string {
	if diff == "" {
		return ""
	}
	lines := strings.Split(diff, "\n")

	// Count total diff lines for the indicator.
	total := len(lines)
	maxLines := contentWidth / 3 // heuristic: ~3 chars per vertical line
	if maxLines < 8 {
		maxLines = 8
	}
	if maxLines > 24 {
		maxLines = 24
	}

	var b strings.Builder
	shown := lines
	if total > maxLines {
		shown = lines[:maxLines]
	}

	for _, line := range shown {
		b.WriteString(colorDiffLine(line))
		b.WriteByte('\n')
	}

	if total > maxLines {
		remain := total - maxLines
		b.WriteString(diffCtx("  … " + itoa(remain) + " more lines"))
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

// itoa is a fast int→string for small positive ints.
func itoa(n int) string {
	return strconv.Itoa(n)
}
