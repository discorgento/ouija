package chat

import (
	"fmt"
	"image/color"
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
)

// ─── Approval option ──────────────────────────────────────────────────

// approvalOpt is one selectable option in the approval prompt.
type approvalOpt struct {
	key   string // single-key shortcut
	label string // display label
	color color.Color
	resp  approvalResponseMsg
}

// approvalOptions is the full set of actions for a tool approval prompt.
// Order matches the arrow-key navigation order. For delete_file, the first
// option becomes "Delete" (key: d) instead of "Yes" (key: y).
var approvalOptions = []approvalOpt{
	{key: "y", label: "Yes", color: lipgloss.Color("71"), resp: approvalResponseMsg{allowed: true}},
	{key: "n", label: "No", color: lipgloss.Color("131"), resp: approvalResponseMsg{allowed: false, cancelled: false}},
	{key: "s", label: "Session", color: lipgloss.Color("99"), resp: approvalResponseMsg{allowed: true, forSession: true}},
	{key: "e", label: "Edit", color: lipgloss.Color("208"), resp: approvalResponseMsg{allowed: true}},
	{key: "esc", label: "Cancel", color: lipgloss.Color("243"), resp: approvalResponseMsg{allowed: false}},
}

// deleteFileOptions replaces the "Yes" button with "Delete" (key: d, red)
// as an extra confirmation layer for destructive delete operations.
var deleteFileOptions = []approvalOpt{
	{key: "d", label: "Delete", color: lipgloss.Color("196"), resp: approvalResponseMsg{allowed: true}},
	{key: "n", label: "No", color: lipgloss.Color("131"), resp: approvalResponseMsg{allowed: false, cancelled: false}},
	{key: "e", label: "Edit", color: lipgloss.Color("208"), resp: approvalResponseMsg{allowed: true}},
	{key: "esc", label: "Cancel", color: lipgloss.Color("243"), resp: approvalResponseMsg{allowed: false}},
}

// baseTool strips the composite session-key suffix ("tool\x00target") the
// engine appends to scope approvals per target, so the UI always reasons
// about the tool itself. Keys without a suffix pass through unchanged.
func baseTool(key string) string {
	if i := strings.IndexByte(key, 0); i >= 0 {
		return key[:i]
	}
	return key
}

// approvalOptionsFor returns the appropriate options set for the given tool.
func approvalOptionsFor(tool string) []approvalOpt {
	opts := approvalOptions
	if baseTool(tool) == "delete_file" {
		opts = deleteFileOptions
	}
	// Edit opens the proposed oldString → newString change in $EDITOR: only
	// edit_file has one; elsewhere the button did nothing.
	if baseTool(tool) != "edit_file" {
		opts = slices.DeleteFunc(slices.Clone(opts), func(o approvalOpt) bool { return o.key == "e" })
	}
	return opts
}

// renderApprovalBtn renders one selectable option in the approval prompt.
// Both selected and unselected have Padding(0,1) so buttons don't shift
// when navigating.
func renderApprovalBtn(opt approvalOpt, selected bool) string {
	content := fmt.Sprintf("● %s %s", opt.key, opt.label)
	if selected {
		return lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("255")).
			Background(opt.color).
			Padding(0, 1).
			Render(content)
	}
	return lipgloss.NewStyle().
		Foreground(opt.color).
		Background(lipgloss.Color("236")).
		Padding(0, 1).
		Render("○ " + opt.key + " " + opt.label)
}

// ─── Tool display helpers ──────────────────────────────────────────────

// approvalToolIcon maps tool names to a descriptive symbol.
func approvalToolIcon(tool string) string {
	if strings.HasPrefix(tool, "escape:") {
		return "↗"
	}
	switch baseTool(tool) {
	case "bash":
		return "›"
	case "write_file", "edit_file", "delete_file":
		return "＋"
	case "read_file":
		return "◎"
	case "grep":
		return "§"
	case "glob":
		return "*"
	case "list_dir":
		return "≡"
	default:
		return "◆"
	}
}

// approvalToolColor returns a color for the tool label.
func approvalToolColor(tool string) color.Color {
	if strings.HasPrefix(tool, "escape:") {
		return lipgloss.Color("208")
	}
	switch baseTool(tool) {
	case "bash":
		return lipgloss.Color("196")
	case "write_file", "edit_file", "delete_file":
		return lipgloss.Color("220")
	case "read_file", "grep", "glob", "list_dir":
		return lipgloss.Color("118")
	default:
		return lipgloss.Color("141")
	}
}

// displayToolName strips the composite session key encoding for display.
func displayToolName(tool string) string {
	if strings.HasPrefix(tool, "escape:") {
		rest := strings.TrimPrefix(tool, "escape:")
		if idx := strings.IndexByte(rest, 0); idx >= 0 {
			return rest[:idx] + " (outside sandbox)"
		}
		return rest
	}
	return baseTool(tool)
}

// ─── Approval box rendering ────────────────────────────────────────────

// RenderApprovalBox renders the full approval prompt box body.
// It shows the tool name, call number, the diff (for edit_file via
// input["_preview"]) or raw arguments (for other tools), and the
// option buttons + hint. The selectedIdx is the current arrow-key
// selection position.
func RenderApprovalBox(req *approvalRequestMsg, toolCallN, selectedIdx int) string {
	toolColor := approvalToolColor(req.tool)
	toolIcon := approvalToolIcon(req.tool)

	var inner strings.Builder

	// ── Tool icon + name + call number ──────────────────────────
	icon := lipgloss.NewStyle().Bold(true).Foreground(toolColor).Render(toolIcon)
	cleanName := displayToolName(req.tool)
	name := lipgloss.NewStyle().Bold(true).Foreground(toolColor).Render(" " + cleanName)
	badge := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("220")).Render(fmt.Sprintf(" [%d]", toolCallN))
	inner.WriteString("  ")
	inner.WriteString(icon)
	inner.WriteString(name)
	inner.WriteString(badge)
	inner.WriteByte('\n')

	// ── Tool-specific content ───────────────────────────────────
	// Path line (for tools that accept a path argument).
	if p, ok := req.input["path"]; ok {
		if s, ok2 := p.(string); ok2 && s != "" {
			pathLine := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render("    " + s)
			inner.WriteString("  ")
			inner.WriteString(pathLine)
			inner.WriteByte('\n')
		}
	} else if req.path != "" && req.path != baseTool(req.tool) {
		pathStr := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("60")).Render("    " + req.path)
		inner.WriteString("  ")
		inner.WriteString(pathStr)
		inner.WriteByte('\n')
	}

	// Preview from engine (colored diff for edit_file, summary for others).
	if preview, ok := req.input["_preview"]; ok {
		if s, ok2 := preview.(string); ok2 && s != "" {
			inner.WriteByte('\n')
			// Multi-line diff receives full coloring; single-line summary
			// is rendered in dim white.
			if strings.Contains(s, "\n") && strings.HasPrefix(s, "  @@") {
				colored := RenderColoredDiff(s)
				for _, line := range strings.Split(colored, "\n") {
					inner.WriteString("  ")
					inner.WriteString(line)
					inner.WriteByte('\n')
				}
			} else {
				dim := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render("  " + s)
				inner.WriteString("  ")
				inner.WriteString(dim)
				inner.WriteByte('\n')
			}
		}
	}

	// Raw arguments (filter out _prefixed meta-keys like _preview).
	argKeys := slices.Sorted(maps.Keys(req.input))
	shown := false
	for _, k := range argKeys {
		if strings.HasPrefix(k, "_") {
			continue
		}
		shown = true
		v := req.input[k]
		val := fmt.Sprintf("%v", v)
		if len(val) > 80 {
			val = strings.ToValidUTF8(val[:77], "") + "..."
		}
		argLine := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render(fmt.Sprintf("  %s: %s", k, val))
		inner.WriteString("  ")
		inner.WriteString(argLine)
		inner.WriteByte('\n')
	}
	if shown {
		inner.WriteByte('\n')
	}

	inner.WriteByte('\n')

	// ── Option buttons ──────────────────────────────────────────
	opts := approvalOptionsFor(req.tool)
	optGap := lipgloss.NewStyle().Foreground(lipgloss.Color("60")).Render("  ")
	inner.WriteString("  ")
	for i, opt := range opts {
		if i > 0 {
			inner.WriteString(optGap)
		}
		btn := renderApprovalBtn(opt, i == selectedIdx)
		inner.WriteString(btn)
	}
	inner.WriteByte('\n')

	// ── Hint line ───────────────────────────────────────────────
	hintKeys := "y / n / s / e / esc"
	if baseTool(req.tool) == "delete_file" {
		hintKeys = "d / n / e / esc"
	}
	hint := lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("60")).Render("← → navigate · enter confirm · " + hintKeys)
	inner.WriteString("  ")
	inner.WriteString(hint)
	inner.WriteByte('\n')

	return inner.String()
}
