// Package bootstrap wires the harness together for embedders: it discovers the
// config the ouija CLI would use (project layers + the global store written by
// `ouija setup`), resolves the provider and returns a ready engine.
//
// It exists so a totem binary living in an external module needs no glue of its
// own: import bootstrap, call Setup, run totems against app.Engine — and the
// user's existing `~/.config/ouija/config.json` (keys, provider, budget, rules)
// applies unchanged.
package bootstrap

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/mcp"
)

// App is a ready-to-use harness: the engine App.Run runs totems on, plus the
// config it was built from.
type App struct {
	// Engine is the shared engine App.Run uses: ReAct loop, tools, budget,
	// sandbox, trace. Its budget adds up over every App.Run; a long-lived host
	// that runs one totem per request should Setup a fresh App per request.
	// ServeMCP does not use it: each MCP call gets a fresh engine built from
	// the same config, with its own budget.
	Engine *engine.Engine
	// Config is the effective config after layer discovery and validation.
	Config *engine.Config
	// ConfigPath is the project config file that was loaded ("" when only the
	// global defaults apply).
	ConfigPath string

	name             string
	explicitConfPath string // WithConfigPath, honoured by ServeMCP
}

type options struct {
	configPath string
	trace      bool
	name       string
}

// Option customizes Setup.
type Option func(*options)

// WithConfigPath loads the project config from an explicit path instead of
// discovering it from the current directory.
func WithConfigPath(path string) Option {
	return func(o *options) { o.configPath = path }
}

// WithTrace enables the console trace tree (per-agent spans, streamed to
// stderr). Useful in development; off by default so embedders control their
// own output.
func WithTrace(enabled bool) Option {
	return func(o *options) { o.trace = enabled }
}

// WithName sets the identity the MCP server reports to harnesses (serverInfo
// name and the `<name>_<totem>` tool namespace). Defaults to the binary's base
// name, so your totem shows up as yours — not as "ouija".
func WithName(name string) Option {
	return func(o *options) { o.name = name }
}

// Setup discovers config, validates it, builds the provider and returns a
// ready engine. The context is carried for cancellation-sensitive wiring; it
// does not bound later runs (pass the run's own ctx to eng.Run).
func Setup(ctx context.Context, opts ...Option) (*App, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	if o.name == "" {
		o.name = filepath.Base(os.Args[0]) // WithName's documented default
	}

	out, err := app.SetupEngine(ctx, o.configPath, o.trace)
	if err != nil {
		return nil, err
	}
	return &App{
		Engine:           out.Engine,
		Config:           out.Config,
		ConfigPath:       out.ConfigPath,
		name:             o.name,
		explicitConfPath: o.configPath,
	}, nil
}

// ServeMCP serves the Model Context Protocol on in/out until in reaches EOF
// or ctx ends — every registered totem becomes a `<name>_<totem>` tool
// callable by harnesses like Claude Code, Cursor or OpenCode. Diagnostics go
// to stderr; stdout carries the protocol. Each tool call runs on a fresh
// engine from the App's config (its own budget, no approval UI, so write
// tools are denied unless the config allows them) — not on App.Engine, whose
// runtime setters therefore do not apply here.
func (a *App) ServeMCP(ctx context.Context, in io.Reader, out io.Writer) error {
	srv, err := mcp.New(func(f string, args ...any) {
		fmt.Fprintf(os.Stderr, "[%s-mcp] "+f+"\n", append([]any{a.name}, args...)...)
	})
	if err != nil {
		return fmt.Errorf("mcp: %w", err)
	}
	srv.SetName(a.name)
	// Tool calls build a fresh engine per call (own budget), so the server
	// needs the same config source Setup used — not a cwd rediscovery.
	if a.explicitConfPath != "" {
		if err := srv.SetConfigPath(a.explicitConfPath); err != nil {
			return fmt.Errorf("mcp: %w", err)
		}
	}
	return srv.Run(ctx, in, out)
}
