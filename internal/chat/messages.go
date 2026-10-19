package chat

import (
	"context"
	"strings"
	"time"

	"github.com/discorgento/ouija/engine"
)

// ─── Messages ──────────────────────────────────────────────────────────

// chatResponse carries the engine reply back to the Update loop.
type chatResponse struct {
	text string
	msgs []engine.Message
	err  error
	gen  int // generation at request time (for stale response detection)
}

// toolProgressMsg carries tool execution progress for display.
type toolProgressMsg struct {
	tool string
	arg  string
}

// tokenMsg carries a streaming text token for real-time TUI display.
// bannerMsg puts a notice in the transcript from outside the event loop (the
// approval callback runs on the engine's goroutine).
type bannerMsg struct{ text string }

type tokenMsg struct {
	text string
	gen  int // the generation that streamed it; others are stale
}

// tickMsg drives the welcome animation.
type tickMsg struct{}

// toastClearMsg clears the status-bar notice it was set for (id), unless a
// newer one replaced it.
type toastClearMsg struct{ id int }

// approvalRequestMsg is sent from the engine goroutine to the TUI event
// loop when a tool requires user approval. The TUI must send a response
// through respCh to unblock the engine.
type approvalRequestMsg struct {
	ctx    context.Context // the run's: done once its generation is cancelled
	tool   string
	path   string         // human-readable description (from friendlyArg)
	input  map[string]any // full tool call arguments for display
	respCh chan<- approvalResponseMsg
}

// approvalResponseMsg carries the user's decision back to the engine.
type approvalResponseMsg struct {
	allowed       bool
	forSession    bool
	duration      time.Duration // when set, session expires after this duration
	cancelled     bool          // user pressed Cancel (esc), distinct from No
	skipExecution bool          // tool already applied externally; skip execution
}

// editorDoneMsg is sent when the external editor closes.
type editorDoneMsg struct {
	err error
}

// ─── Tool progress helper ──────────────────────────────────────────────

// friendlyArg returns a human-readable summary of a tool call input
// (e.g. "read_file → src/main.go").
func friendlyArg(name string, input map[string]any) string {
	if strings.HasPrefix(name, "escape:") {
		rest := strings.TrimPrefix(name, "escape:")
		// Format: "toolName\x00absPath" — null byte can't appear in paths
		if idx := strings.IndexByte(rest, 0); idx >= 0 {
			tool := rest[:idx]
			path := rest[idx+1:]
			return "outside sandbox: " + tool + " → " + path
		}
		return "outside sandbox: " + rest
	}
	// The engine scopes approvals by target ("tool\x00path"), so reduce the
	// key to the tool name before matching on it.
	switch baseTool(name) {
	case "read_file", "list_dir", "write_file", "edit_file", "delete_file":
		if p, ok := input["path"]; ok {
			if s, ok := p.(string); ok {
				return s
			}
		}
	case "grep", "glob":
		if p, ok := input["pattern"]; ok {
			if s, ok := p.(string); ok {
				if len(s) > 60 {
					s = strings.ToValidUTF8(s[:57], "") + "..."
				}
				return s
			}
		}
	case "bash":
		if c, ok := input["command"]; ok {
			if s, ok := c.(string); ok {
				if len(s) > 60 {
					s = strings.ToValidUTF8(s[:57], "") + "..."
				}
				return s
			}
		}
	}
	return baseTool(name)
}
