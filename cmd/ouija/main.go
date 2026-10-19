// ouija — AI project assistant.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/chat"
	"github.com/discorgento/ouija/internal/commands"
	"github.com/discorgento/ouija/internal/knowledge"
	"github.com/discorgento/ouija/internal/mcp"
	"github.com/discorgento/ouija/internal/version"
	"github.com/discorgento/ouija/registry"

	_ "github.com/discorgento/ouija/totems"
)

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}

// help renders the CLI-specific help banner and command sections, splicing in
// the list of registered totems (which the registry renders generically).
func help() string {
	var b strings.Builder
	b.WriteString("🔮  ouija — multi-totem AI harness · any project · any LLM\n")
	b.WriteString(registry.Help())

	b.WriteString("\n  ◆ Setup\n")
	b.WriteString("      setup      Interactive global config wizard (API keys + provider)\n")
	b.WriteString("      init       Create .ouija/config.json (interactive)\n")
	b.WriteString("      trust      Trust this project's config (permissions, allowlist, apiKey)\n")
	b.WriteString("      config     Change provider/model (interactive)\n")
	b.WriteString("      doctor     Diagnose config, provider key, and harness wiring\n")

	b.WriteString("\n  ◆ Development\n")
	b.WriteString("      generate   Scaffold a new totem (interactive)\n")
	b.WriteString("                   ouija generate totem\n")
	b.WriteString("                   ouija generate totem --external [dir]  # standalone module\n")

	b.WriteString("\n  ◆ Workflow\n")
	b.WriteString("      chat       Interactive chat with project-aware AI\n")
	b.WriteString("                   ouija chat\n")
	b.WriteString("      learn      Save/show knowledge about the project\n")
	b.WriteString("                   ouija learn \"fact\"\n")

	b.WriteString("\n  ◆ Harnesses\n")
	b.WriteString("      mcp        Serve/connect the MCP server for any agent\n")
	b.WriteString("                   ouija mcp · ouija mcp install · ouija mcp status\n")

	b.WriteString("\n  ◆ Flags\n")
	b.WriteString("      --json            Print raw JSON\n")
	b.WriteString("      --trace-file <f>  Perfetto JSON trace\n")

	b.WriteString("\n  ◆ Info\n")
	b.WriteString("      version            Print the version\n")
	b.WriteString("      help               Show this help\n")

	b.WriteString("\n  ◆ Example\n")
	b.WriteString("      ouija init\n")
	b.WriteString("      ouija chat\n")
	b.WriteString("      ouija learn \"This module uses React\"\n")
	b.WriteString("      ouija task \"Fix sorting bug\"\n")
	return b.String()
}

// run is the testable entry point: it returns the exit code instead of calling
// os.Exit, and writes to the given streams instead of the process globals.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		fmt.Fprint(stdout, help())
		return 0
	}
	switch args[1] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, help())
		return 0
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, "ouija "+version.Version)
		return 0
	case "setup":
		return runCommand(commands.Setup, args[2:], stderr)
	case "init":
		return runCommand(commands.Init, args[2:], stderr)
	case "trust":
		return runCommand(commands.Trust, args[2:], stderr)
	case "config":
		return runCommand(commands.Config, args[2:], stderr)
	case "learn":
		return runCommand(commands.Learn, args[2:], stderr)
	case "generate":
		return runCommand(commands.Generate, args[2:], stderr)
	case "chat":
		return cmdChat(args[2:], stderr)
	case "mcp":
		return cmdMcp(args[2:], stdout, stderr)
	case "doctor":
		if err := commands.Doctor(stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	name := args[1]
	if _, ok := registry.Get(name); !ok {
		fmt.Fprintf(stderr, "unknown totem: %s\n\n%s", name, help())
		return 1
	}

	totemArgs, flags, err := app.ParseTotemArgs(name, args[2:], stderr)
	if err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	out, runErr := app.Run(ctx, name, totemArgs, flags.ConfigPath, true, flags.TraceFile)
	if out != nil && out.ConfigPath != "" {
		fmt.Fprintf(stderr, "[ouija] config: %s\n", out.ConfigPath)
	}
	if runErr != nil {
		fmt.Fprintf(stderr, "[ouija] error: %v\n", runErr)
		if hint := errorHint(runErr); hint != "" {
			fmt.Fprintf(stderr, "[ouija] hint: %s\n", hint)
		}
		if out != nil {
			t, s, _ := out.Engine.Spent()
			fmt.Fprintf(stderr, "[ouija] %d tokens, %d steps\n", t, s)
		}
		return 1
	}

	if flags.JSON {
		b, _ := json.MarshalIndent(out.Result, "", "  ")
		fmt.Fprintln(stdout, string(b))
	} else {
		fmt.Fprintln(stdout, registry.Render(out.Result))
	}

	tokens, steps, usd := out.Engine.Spent()
	cost := ""
	if usd > 0 {
		cost = fmt.Sprintf(" · $%.2f", usd)
	}
	fmt.Fprintf(stderr, "\n[ouija] %s · %s · %d tokens · %d steps%s\n", out.Config.Provider, out.Config.Model, tokens, steps, cost)
	return 0
}

// runCommand runs a subcommand and maps a failure to exit code 1.
func runCommand(fn func([]string) error, args []string, stderr io.Writer) int {
	if err := fn(args); err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		if hint := errorHint(err); hint != "" {
			fmt.Fprintf(stderr, "hint: %s\n", hint)
		}
		return 1
	}
	return 0
}

// errorHint returns an actionable suggestion for a known error class, or "" when
// the error carries no recognised code. It lets callers turn a bare failure
// message into a fix ("run ouija setup") without inspecting message strings.
func errorHint(err error) string {
	switch {
	case engine.Is(err, engine.ErrCodeAuth):
		return "the provider has no key or refused it (check the key and what its plan covers): " +
			"`ouija setup` to change it or pick another provider/model, `ouija init` for this project"
	case engine.Is(err, engine.ErrCodeConfig):
		return "check your setup with `ouija doctor`"
	case engine.Is(err, engine.ErrCodeProvider):
		return "the provider refused the request (its reason is above): " +
			"try again, or pick another model with `ouija setup` / `ouija init`"
	case engine.Is(err, engine.ErrCodeBudget):
		return "raise budget.maxTokens / budget.maxSteps in your config"
	}
	return ""
}

// cmdChat starts the interactive chat TUI and returns the process exit code.
func cmdChat(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to a JSON config")
	traceFile := fs.String("trace-file", "", "path to write Perfetto JSON trace")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Chat always renders the trace to stderr — the flag is not required.
	out, err := app.SetupEngine(ctx, *configPath, true)
	if err != nil {
		fmt.Fprintf(stderr, "error: %v\n", err)
		if hint := errorHint(err); hint != "" {
			fmt.Fprintf(stderr, "hint: %s\n", hint)
		}
		return 1
	}

	eng := out.Engine
	chat.Run(eng, out.ProviderFactory(), chatSystemPrompt(out.Config, knowledge.Load(".")), out.ConfigPath)

	// The whole session's span tree (each turn was also shown in the chat).
	app.FinishTrace(eng.Tracer(), stderr, *traceFile)
	eng.Close()
	return 0
}

// cmdMcp runs `ouija mcp [serve|install|status]`. Bare `ouija mcp` serves the
// Model Context Protocol over stdio, which is what a harness launches. install
// registers the server with every agent harness found on this machine.
func cmdMcp(args []string, stdout, stderr io.Writer) int {
	sub := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub = args[0]
	}

	switch sub {
	case "serve":
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		if err := mcp.Serve(ctx, os.Stdin, stdout, stderr); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	case "install":
		if err := mcp.Install(stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	case "status":
		if err := mcp.Status(stdout); err != nil {
			fmt.Fprintf(stderr, "error: %v\n", err)
			return 1
		}
		return 0
	default:
		fmt.Fprintf(stderr, "unknown mcp subcommand: %s\n\n  ouija mcp [serve|install|status]\n", sub)
		return 2
	}
}

// chatSystemPrompt is the chat's system message: the configured systemPrompt,
// then the project rules, root and the facts `ouija learn` recorded.
func chatSystemPrompt(cfg *engine.Config, known string) string {
	var b strings.Builder
	b.WriteString(cmp.Or(cfg.SystemPrompt, "You are ouija, a project-aware AI assistant. "+
		"Use the available tools to read files, search the codebase, and answer questions."))
	if len(cfg.Rules) > 0 {
		b.WriteString("\n\nProject rules:\n- " + strings.Join(cfg.Rules, "\n- "))
	}
	if cfg.ProjectRoot != "" {
		b.WriteString("\n\nProject root: " + cfg.ProjectRoot)
	}
	if known != "" {
		b.WriteString("\n\n## Known project facts\n" + known)
	}
	return b.String()
}
