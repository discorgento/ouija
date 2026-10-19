// Package chat implements the Bubble Tea TUI for interactive ouija chat sessions.
package chat

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"

	"github.com/discorgento/ouija/engine"
)

// Run starts the chat TUI. It blocks until the user exits.
// The engine must be configured and ready; the system prompt is pre-built.
// buildProvider is injected by the entrypoint (app.BuildProvider) so this
// package never imports app — and so tests can drive /provider with a stub.
func Run(eng *engine.Engine, buildProvider ProviderFactory, system, configPath string) {
	// Silence engine progress — the TUI owns the visual.
	eng.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Set terminal title.
	fmt.Fprintf(os.Stdout, "\x1b]2;ouija\x07")

	m := newModel(eng, buildProvider, system, configPath)
	p := tea.NewProgram(m)

	// Token streaming: bridge the engine's streaming callback to the Bubble Tea
	// event loop so tokens appear in real time.
	// The callbacks guard on m.ctx (the root context, never replaced) so they
	// have a stable cancellation signal. On Esc, only the per-generation
	// m.chatCancel fires; m.ctx stays live, so the bridge goroutine and the
	// engine callbacks keep running uninterrupted across generations.
	tokenCh := make(chan tokenMsg, 256)
	toolCh := make(chan toolProgressMsg, 32)

	eng.SetTokenCallback(func(text string) {
		select {
		case tokenCh <- tokenMsg{text: text}:
		case <-m.ctx.Done():
		}
	})
	eng.SetToolCallback(func(name string, input map[string]any) {
		select {
		case toolCh <- toolProgressMsg{tool: name, arg: friendlyArg(name, input)}:
		case <-m.ctx.Done():
		}
	})

	// ── Permission / Approval system ────────────────────────────────
	// Load from config if set, otherwise use defaults: read-only tools
	// (list_dir, read_file, grep, glob) are Allow; write/exec (bash,
	// write_file, edit_file, delete_file) are Ask.
	// The user can override via /permit or .ouija/config.json.
	permCfg := eng.Config().Permission.ToEngine()
	if permCfg == nil {
		permCfg = engine.NewDefaultPermissionConfig()
	}
	eng.SetPermissionConfig(permCfg)

	// Approval manager with TUI callback: sends a request to the event loop
	// and blocks until the user responds (y/n/s/esc) or the context is cancelled.
	approvalMgr := engine.NewApprovalManager()
	approvalMgr.SetCallback(func(ctx context.Context, tool string, input map[string]any) (engine.ApprovalResult, error) {
		path := friendlyArg(tool, input)
		respCh := make(chan approvalResponseMsg, 1)
		p.Send(approvalRequestMsg{tool: tool, path: path, input: input, respCh: respCh})
		select {
		case resp := <-respCh:
			// Auto-learn only on an explicit "Session" choice. A plain "yes"
			// runs the command once: the old code persisted every approved
			// command to config.json, so one keystroke silently allowlisted
			// it forever. Session also persists, because the user asked for
			// the approval to outlive this run.
			if resp.allowed && resp.forSession && baseTool(tool) == "bash" {
				if cmd, ok := input["command"].(string); ok {
					for _, seg := range engine.ExtractCommands(cmd) {
						eng.AddAllowedCommand(seg)
					}
					if configPath != "" {
						if err := eng.SaveConfigAt(configPath); err != nil {
							slog.Warn("persist allowlist", "path", configPath, "err", err)
						}
					}
				}
			}
			return engine.ApprovalResult{Allowed: resp.allowed, ForSession: resp.forSession, Duration: resp.duration, Cancelled: resp.cancelled, SkipExecution: resp.skipExecution}, nil
		case <-ctx.Done():
			return engine.ApprovalResult{}, ctx.Err()
		}
	})
	eng.SetApprovalManager(approvalMgr)

	// Bridge goroutine: forwards engine events to the Bubble Tea event loop.
	// Watches m.ctx (root, never replaced) so it survives Esc cancels and
	// keeps forwarding tokens for auto-dispatched queued messages.
	go func() {
		for {
			select {
			case <-m.ctx.Done():
				return
			case msg, ok := <-tokenCh:
				if !ok {
					return
				}
				p.Send(msg)
			case msg, ok := <-toolCh:
				if !ok {
					return
				}
				p.Send(msg)
			}
		}
	}()

	final, runErr := p.Run()

	// Cancel root context — stops the bridge goroutine and engine callbacks.
	m.cancel()
	if runErr != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", runErr)
		os.Exit(1)
	}

	// Session summary after the alt-screen exits.
	if m2, ok := final.(*model); ok && m2.msgCount > 0 {
		tokens, steps, usd := eng.Spent()
		cfg := eng.Config()
		label := fmt.Sprintf("%d msg · %d tok · %d steps · %s",
			m2.msgCount, tokens, steps, cfg.Model)
		if usd > 0 {
			label = fmt.Sprintf("%d msg · %d tok · %d steps · $%.2f · %s",
				m2.msgCount, tokens, steps, usd, cfg.Model)
		}
		fmt.Fprintf(os.Stderr, "\n  %s\n", strings.Repeat("─", 47))
		fmt.Fprintf(os.Stderr, "  %s\n", label)
		fmt.Fprintf(os.Stderr, "  %s\n", strings.Repeat("─", 47))
	}
}
