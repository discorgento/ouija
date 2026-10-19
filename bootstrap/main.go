package bootstrap

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/discorgento/ouija/registry"
)

// Run runs the registered totem name on the App's engine through
// registry.Invoke: required inputs, Init, Run, Cleanup.
func (a *App) Run(ctx context.Context, name string, args registry.Args) (any, error) {
	t, ok := registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown totem: %s", name)
	}
	return registry.Invoke(ctx, a.Engine, t, args)
}

// Main is the whole command line of a binary that hosts your totems:
//
//	<bin>                                            list the totems
//	<bin> <totem> [task] [--set key=value]… [--json] [--config file]
//	<bin> mcp                                        serve them over MCP (stdio)
//
// It returns the exit code, so a host is one line:
//
//	func main() { os.Exit(bootstrap.Main(bootstrap.WithName("my-totem"))) }
func Main(opts ...Option) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return run(ctx, os.Args, os.Stdin, os.Stdout, os.Stderr, opts...)
}

// run is Main with its process globals injected, for tests.
func run(ctx context.Context, argv []string, stdin io.Reader, stdout, stderr io.Writer, opts ...Option) int {
	bin := filepath.Base(argv[0])
	if len(argv) < 2 || argv[1] == "help" || argv[1] == "-h" || argv[1] == "--help" {
		fmt.Fprint(stdout, registry.Help())
		fmt.Fprintf(stdout, "\n  usage: %s <totem> [task] [--set key=value]… [--json] · %s mcp\n", bin, bin)
		return 0
	}

	if argv[1] == "mcp" {
		app, err := Setup(ctx, opts...)
		if err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		defer app.Engine.Close()
		if err := app.ServeMCP(ctx, stdin, stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	name := argv[1]
	if _, ok := registry.Get(name); !ok {
		fmt.Fprintf(stderr, "unknown totem: %s\n\n%s", name, registry.Help())
		return 1
	}

	// The task is the first argument when it is not a flag.
	task, rest := "", argv[2:]
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		task, rest = rest[0], rest[1:]
	}
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", false, "print the raw JSON result")
	configPath := fs.String("config", "", "path to a JSON config")
	params := registry.ParamsFlag{}
	fs.Var(params, "set", "named input key=value (repeatable; JSON values allowed)")
	if err := fs.Parse(rest); err != nil {
		return 2
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q (quote the task, pass inputs as --set key=value)\n", fs.Arg(0))
		return 2
	}
	if task != "" {
		params["task"] = task
	}
	if *configPath != "" {
		opts = append(opts, WithConfigPath(*configPath))
	}

	app, err := Setup(ctx, opts...)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer app.Engine.Close()

	out, err := app.Run(ctx, name, registry.Args{Task: task, Params: params})
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	if *jsonOut {
		b, _ := json.MarshalIndent(out, "", "  ")
		fmt.Fprintln(stdout, string(b))
	} else {
		fmt.Fprintln(stdout, registry.Render(out))
	}
	tokens, steps, usd := app.Engine.Spent()
	fmt.Fprintf(stderr, "\n%s · %d tokens · %d steps · $%.2f\n", app.Config.Model, tokens, steps, usd)
	return 0
}
