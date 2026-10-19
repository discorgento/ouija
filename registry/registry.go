package registry

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/discorgento/ouija/engine"
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

// Help renders the list of registered totems ("summons the available
// spirits") — one line per totem, name + summary. It is deliberately generic:
// a host that embeds its own totems gets its own list, with no ouija CLI
// assumptions baked in.
func Help() string {
	names := append([]string(nil), order...)
	slices.Sort(names)
	var b strings.Builder
	b.WriteString("◆ Totems\n")
	for _, n := range names {
		fmt.Fprintf(&b, "      %-8s %s\n", n, totems[n].Summary())
	}
	return b.String()
}
