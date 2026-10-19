package registry

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/discorgento/ouija/internal/engine"
)

// Args carries the parsed CLI input handed to a totem.
type Args struct {
	Task string
}

// Totem is one focused autonomous workflow harness that runs on top of the
// engine. Init is called once at setup time (before Run); Cleanup is called
// after Run returns (even on error). Implementations that don't need lifecycle
// management can return nil from both.
type Totem interface {
	Name() string
	Summary() string
	Init(ctx context.Context, eng *engine.Engine, cfg *engine.Config) error
	Run(ctx context.Context, eng *engine.Engine, args Args) (any, error)
	Cleanup() error
}

// Renderer is an optional interface a totem result can implement to control
// its human-facing terminal output (the CLI falls back to JSON otherwise).
type Renderer interface {
	Render() string
}

var (
	totems = map[string]Totem{}
	order  []string
)

// Register adds a totem to the registry (called from each totem's init()).
func Register(t Totem) {
	if _, dup := totems[t.Name()]; !dup {
		order = append(order, t.Name())
	}
	totems[t.Name()] = t
}

// Get resolves a totem by name.
func Get(name string) (Totem, bool) {
	t, ok := totems[name]
	return t, ok
}

// List returns all registered totems, sorted by name.
func List() []Totem {
	names := append([]string(nil), order...)
	slices.Sort(names)
	out := make([]Totem, 0, len(names))
	for _, n := range names {
		out = append(out, totems[n])
	}
	return out
}

// Help renders the list of available totems ("summons the available spirits").
func Help() string {
	names := append([]string(nil), order...)
	slices.Sort(names)
	var b strings.Builder
	b.WriteString("🔮  ouija — multi-totem AI harness · any project · any LLM\n")

	b.WriteString("\n  ◆ Setup\n")
	b.WriteString("      setup      Interactive global config wizard (API keys + provider)\n")
	b.WriteString("      init       Create .ouija/config.json (interactive)\n")
	b.WriteString("      config     Change provider/model (interactive)\n")
	b.WriteString("      doctor     Diagnose config, provider key, and harness wiring\n")

	b.WriteString("\n  ◆ Development\n")
	b.WriteString("      generate   Scaffold a new totem (interactive)\n")
	b.WriteString("                   ouija generate totem\n")

	b.WriteString("\n  ◆ Workflow\n")
	b.WriteString("      chat       Interactive chat with project-aware AI\n")
	b.WriteString("                   ouija chat\n")
	b.WriteString("      learn      Save/show knowledge about the project\n")
	b.WriteString("                   ouija learn \"fact\"\n")
	b.WriteString("      task       Analyze a task and produce a plan\n")
	b.WriteString("                   ouija task \"description\"\n")

	b.WriteString("\n  ◆ Totems\n")
	for _, n := range names {
		fmt.Fprintf(&b, "      %-8s %s\n", n, totems[n].Summary())
	}

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
