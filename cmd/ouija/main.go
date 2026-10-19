// ouija — AI project assistant.
package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"

	"github.com/charmbracelet/x/term"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/chat"
	"github.com/discorgento/ouija/internal/commands"
	"github.com/discorgento/ouija/internal/knowledge"
	"github.com/discorgento/ouija/internal/mcp"
	"github.com/discorgento/ouija/internal/ouijaconfig"
	"github.com/discorgento/ouija/internal/progress"
	"github.com/discorgento/ouija/internal/termsafe"
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
	b.WriteString("      trust      Trust this project's config (permissions, allowlist, apiKey, systemPrompt, projectRoot)\n")
	b.WriteString("      config     Same wizard as setup (API keys, default provider/model)\n")
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

	b.WriteString("\n  ◆ Totem flags (after the totem name)\n")
	b.WriteString("      ouija <totem> [task] [--set key=value]… [--json] [--config <file>] [--trace-file <file>]\n")
	b.WriteString("      --set key=value   Named input (repeatable)\n")
	b.WriteString("      --json            Print the raw JSON result\n")
	b.WriteString("      --config <file>   Use this project config instead of discovering one\n")
	b.WriteString("      --trace-file <f>  Write a Perfetto JSON trace\n")
	b.WriteString("      Any command: --help shows its usage\n")

	b.WriteString("\n  ◆ Start\n")
	b.WriteString("      ouija              On a terminal: open the chat (the setup first, the first time)\n")

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

// interactive reports whether ouija runs on a terminal (stdin and out).
func interactive(stdout io.Writer) bool {
	f, ok := stdout.(*os.File)
	return ok && term.IsTerminal(f.Fd()) && term.IsTerminal(os.Stdin.Fd())
}

// run is the testable entry point: it returns the exit code instead of calling
// os.Exit, and writes to the given streams instead of the process globals.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 2 {
		// On a terminal, bare `ouija` opens the chat — after the setup, the
		// first time. Piped or scripted, it prints the help as it always did.
		if !interactive(stdout) {
			fmt.Fprint(stdout, help())
			return 0
		}
		if global, err := ouijaconfig.Load(); err == nil && global == nil {
			fmt.Fprintln(stdout, "First run: let's set ouija up (the same as `ouija setup`).")
			if code := runCommand(commands.Setup, nil, stderr); code != 0 {
				return code
			}
		}
		return cmdChat(nil, stderr)
	}
	switch args[1] {
	case "help", "-h", "--help":
		fmt.Fprint(stdout, help())
		return 0
	case "version", "-v", "--version":
		fmt.Fprintln(stdout, "ouija "+version.Version)
		return 0
	}
	// `<command> --help` shows that command's usage and does nothing else:
	// `ouija mcp install --help` used to install, `init --help` to write a
	// config. Commands that take no arguments refuse stray ones.
	if usage, ok := commandUsage[args[1]]; ok {
		if slices.ContainsFunc(args[2:], func(a string) bool { return a == "-h" || a == "--help" || a == "-help" }) {
			fmt.Fprintln(stdout, usage)
			return 0
		}
		if noArgCommands[args[1]] && len(args) > 2 {
			fmt.Fprintf(stderr, "unexpected argument %q\n%s\n", args[2], usage)
			return 2
		}
	}
	switch args[1] {
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
		fmt.Fprintf(stderr, "unknown command or totem: %s", name)
		if s := closestName(name); s != "" {
			fmt.Fprintf(stderr, " — did you mean %q?", s)
		}
		fmt.Fprintln(stderr, "\n(`ouija help` lists them)")
		return 2
	}

	totemArgs, flags, err := app.ParseTotemArgs(name, args[2:], stderr)
	if err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// On a terminal the run is drawn live (a nil interface, not a nil *Board,
	// when it is not).
	var board app.Progress
	if progress.Enabled(stderr) {
		board = progress.New(stderr, name)
	}
	out, runErr := app.Run(ctx, name, totemArgs, flags.ConfigPath, true, flags.TraceFile, board)
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

	// A result that cannot be encoded fails the run: printing nothing and
	// exiting 0 lost a paid-for result without a word.
	text, ferr := registry.Format(out.Result)
	if flags.JSON {
		var b []byte
		b, ferr = json.MarshalIndent(out.Result, "", "  ")
		text = string(b)
	}
	if ferr != nil {
		fmt.Fprintf(stderr, "error: the result cannot be printed: %v\n%+v\n", ferr, out.Result)
		return 1
	}
	if flags.JSON {
		fmt.Fprintln(stdout, text)
	} else {
		fmt.Fprintln(stdout, termsafe.Clean(text)) // model-written text
	}

	tokens, steps, usd := out.Engine.Spent()
	cost := ""
	if usd > 0 {
		cost = fmt.Sprintf(" · $%.2f", usd)
	}
	fmt.Fprintf(stderr, "\n[ouija] %s · %s · %d tokens · %d steps%s\n", out.Config.Provider, out.Config.Model, tokens, steps, cost)
	return 0
}

// runCommand runs a subcommand and maps a failure to exit code 1, a usage
// error (already reported by the flag package) to 2.
func runCommand(fn func([]string) error, args []string, stderr io.Writer) int {
	if err := fn(args); err != nil {
		if errors.Is(err, app.ErrUsage) {
			return 2
		}
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
	case engine.Is(err, engine.ErrCodeNetwork), engine.Is(err, engine.ErrCodeStream):
		return "cannot reach the provider: check your connection (for ollama: is the server running, is OLLAMA_HOST right?), then `ouija doctor`"
	}
	return ""
}

// cmdChat starts the interactive chat TUI and returns the process exit code.
func cmdChat(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("chat", flag.ContinueOnError)
	fs.SetOutput(stderr)
	configPath := fs.String("config", "", "path to a JSON config")
	traceFile := fs.String("trace-file", "", "path to write Perfetto JSON trace")
	cont := fs.Bool("continue", false, "resume this project's latest conversation")
	fs.BoolVar(cont, "c", false, "shorthand for --continue")
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
	chat.Run(eng, out.ProviderFactory(), chatSystemPrompt(out.Config, knowledge.Load(cmp.Or(out.Config.ProjectRoot, "."))), out.ConfigPath, chat.Options{Continue: *cont})

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
		sub, args = args[0], args[1:]
	}
	if len(args) > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q\n%s\n", args[0], commandUsage["mcp"])
		return 2
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

// commandUsage is each built-in command's usage, shown for `<command> --help`.
var commandUsage = map[string]string{
	"setup":    "usage: ouija setup\n  Interactive wizard for the global config: API keys and the default provider/model.",
	"config":   "usage: ouija config\n  Same wizard as `ouija setup`.",
	"init":     "usage: ouija init\n  Interactive: write .ouija/config.json for this project (provider, model, optional key).",
	"doctor":   "usage: ouija doctor\n  Check the config, the provider key and the harness wiring.",
	"trust":    "usage: ouija trust [--revoke] [path]\n  Review and trust the project config (its permission, tools, apiKey, systemPrompt and projectRoot then apply).",
	"learn":    "usage: ouija learn [fact…]\n  Without a fact: show what was learned. A fact starting with - goes after --: ouija learn -- \"-v means verbose\"",
	"generate": "usage: ouija generate totem [--external [dir]]\n  Scaffold a totem: in this ouija checkout, or as a standalone module with --external.",
	"chat":     "usage: ouija chat [--config <file>] [--trace-file <file>]\n  Interactive chat with the project.",
	"mcp":      "usage: ouija mcp [serve|install|status]\n  serve (default): the MCP server on stdio · install: register it with the harnesses found · status: show them.",
}

// noArgCommands take no arguments at all.
var noArgCommands = map[string]bool{"setup": true, "config": true, "init": true, "doctor": true}

// closestName suggests the command or totem nearest to a mistyped name.
func closestName(name string) string {
	names := slices.Collect(maps.Keys(commandUsage))
	names = append(names, "help", "version")
	for _, t := range registry.List() {
		names = append(names, t.Name())
	}
	best, bestD := "", 3 // more than 2 edits away is not a typo
	for _, n := range names {
		if d := editDistance(name, n); d < bestD {
			best, bestD = n, d
		}
	}
	return best
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur := make([]int, len(b)+1)
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev = cur
	}
	return prev[len(b)]
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
