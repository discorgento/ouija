// Package chat implements the Bubble Tea TUI for interactive ouija chat sessions.
package chat

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/registry"
)

// Run starts the chat TUI. It blocks until the user exits.
// The engine must be configured and ready; the system prompt is pre-built.
// buildProvider is injected by the entrypoint (Outcome.ProviderFactory) so
// tests can drive /provider with a stub.
// Options are the chat's start options.
type Options struct {
	// Continue resumes the project's latest conversation.
	Continue bool
}

func Run(eng *engine.Engine, buildProvider ProviderFactory, system, configPath string, opts Options) {
	// Silence engine progress — the TUI owns the visual.
	eng.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))

	// Set terminal title.
	fmt.Fprintf(os.Stdout, "\x1b]2;ouija\x07")

	m := newModel(eng, buildProvider, system, configPath)
	if opts.Continue {
		m.continueLatest()
	}
	p := tea.NewProgram(m)

	// Token streaming: bridge the engine's streaming callback to the Bubble Tea
	// event loop so tokens appear in real time.
	// The callbacks guard on m.ctx (the root context, never replaced) so they
	// have a stable cancellation signal. On Esc, only the per-generation
	// m.chatCancel fires; m.ctx stays live, so the bridge goroutine and the
	// engine callbacks keep running uninterrupted across generations.
	tokenCh := make(chan tokenMsg, 256)
	toolCh := make(chan toolProgressMsg, 32)
	totemCh := make(chan totemRunMsg, 64) // a channel: run events keep their order

	m.totemSink = func(t totemRunMsg) {
		select {
		case totemCh <- t:
		case <-m.ctx.Done():
		}
	}
	// /task runs the totem on this engine; its runs are drawn live.
	m.runTotem = func(ctx context.Context, name string, args registry.Args, onRun engine.RunCallback) (string, error) {
		mod, ok := registry.Get(name)
		if !ok {
			return "", fmt.Errorf("unknown totem: %s", name)
		}
		eng.SetRunCallback(onRun)
		defer eng.SetRunCallback(nil)
		res, err := registry.Invoke(ctx, eng, mod, args)
		if err != nil {
			return "", err
		}
		return registry.Format(res)
	}

	m.tokenSink = func(t tokenMsg) {
		select {
		case tokenCh <- t:
		case <-m.ctx.Done():
		}
	}
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
		p.Send(approvalRequestMsg{ctx: ctx, tool: tool, path: path, input: input, respCh: respCh})
		select {
		case resp := <-respCh:
			// Auto-learn only on an explicit "Session" choice. A plain "yes"
			// runs the command once: the old code persisted every approved
			// command to config.json, so one keystroke silently allowlisted
			// it forever. Session also persists, because the user asked for
			// the approval to outlive this run.
			if resp.allowed && resp.forSession && baseTool(tool) == "bash" {
				if cmd, ok := input["command"].(string); ok {
					// Only a fully static command is learned, one pattern per
					// simple command: approving `echo $(whoami)` must not
					// silently allowlist `whoami`.
					for _, pat := range engine.AllowlistPatterns(cmd) {
						eng.AddAllowedCommand(pat)
					}
					if configPath != "" {
						if err := app.SaveProjectConfig(configPath, eng.SaveConfigAt); err != nil {
							p.Send(bannerMsg{"⚠ allowlist not saved (this session only): " + err.Error()})
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
			case msg := <-totemCh:
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

	// A theme was chosen: restart with it, the conversation resumed.
	if m2, ok := final.(*model); ok && m2.restartTheme != "" {
		if err := m2.saveSession(); err != nil {
			fmt.Fprintf(os.Stderr, "conversation not saved: %v\n", err)
		}
		env := append(os.Environ(), "OUIJA_THEME="+m2.restartTheme)
		err := restartSelf(restartArgs(os.Args, m2.msgCount > 0), env)
		fmt.Fprintf(os.Stderr, "theme %s saved; could not restart (%v) — run ouija again to see it\n", m2.restartTheme, err)
		return
	}

	// Session summary after the alt-screen exits.
	if m2, ok := final.(*model); ok && m2.msgCount > 0 {
		if err := m2.saveSession(); err != nil {
			fmt.Fprintf(os.Stderr, "conversation not saved: %v\n", err)
		}
		tokens, steps, usd := eng.Spent()
		out := colorprofile.NewWriter(os.Stderr, os.Environ())
		fmt.Fprintln(out, "\n"+renderSessionSummary(m2, eng.Config(), tokens, steps, usd))
	}
}
