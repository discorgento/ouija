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
	"syscall"

	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/termsafe"
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
		// --config is honoured; anything else is a usage error (it was
		// silently ignored, serving another project's config).
		fs := flag.NewFlagSet(bin+" mcp", flag.ContinueOnError)
		fs.SetOutput(stderr)
		configPath := fs.String("config", "", "path to a JSON config")
		if err := fs.Parse(argv[2:]); err != nil {
			return 2
		}
		if fs.NArg() > 0 {
			fmt.Fprintf(stderr, "unexpected argument %q (usage: %s mcp [--config file])\n", fs.Arg(0), bin)
			return 2
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

	totemArgs, flags, err := app.ParseTotemArgs(name, argv[2:], stderr)
	if err != nil {
		return 2
	}
	if flags.ConfigPath != "" {
		opts = append(opts, WithConfigPath(flags.ConfigPath))
	}
	if flags.TraceFile != "" {
		opts = append(opts, WithTrace(true))
	}

	a, err := Setup(ctx, opts...)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	defer a.Engine.Close()

	out, err := a.Run(ctx, name, totemArgs)
	app.FinishTrace(a.Engine.Tracer(), stderr, flags.TraceFile) // active only with --trace-file
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		return 1
	}
	// A result that cannot be encoded fails the run instead of printing
	// nothing and exiting 0.
	text, ferr := registry.Format(out)
	if flags.JSON {
		var b []byte
		b, ferr = json.MarshalIndent(out, "", "  ")
		text = string(b)
	}
	if ferr != nil {
		fmt.Fprintf(stderr, "error: the result cannot be printed: %v\n%+v\n", ferr, out)
		return 1
	}
	if flags.JSON {
		fmt.Fprintln(stdout, text)
	} else {
		fmt.Fprintln(stdout, termsafe.Clean(text)) // model-written text
	}
	tokens, steps, usd := a.Engine.Spent()
	fmt.Fprintf(stderr, "\n%s · %d tokens · %d steps · $%.2f\n", a.Config.Model, tokens, steps, usd)
	return 0
}
