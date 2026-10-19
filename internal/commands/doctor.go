package commands

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"

	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/engine/providers"
	"github.com/discorgento/ouija/internal/mcp"
	"github.com/discorgento/ouija/internal/ouijaconfig"
	"github.com/discorgento/ouija/internal/registry"
	"github.com/discorgento/ouija/internal/version"
)

// Doctor prints a diagnostic report of the local setup: where the binary and
// configs live, which provider/model resolve, whether an API key is available,
// and which agent harnesses are wired up.
//
// It returns an error (non-zero exit) only when something is definitively
// broken — a missing provider, an invalid config, no totems. Warnings (a missing
// key, a harness that is not connected) are reported but do not fail, so a
// working-but-imperfect setup still exits 0.
func Doctor(w io.Writer) error {
	r := &report{w: w}

	fmt.Fprintln(w, "🔮  ouija doctor")
	fmt.Fprintln(w)

	r.binary()
	r.configFiles()
	r.provider()
	r.totems()
	r.harnesses()

	return r.finish()
}

// labelWidth keeps the report in one aligned column.
const labelWidth = 16

type report struct {
	w        io.Writer
	problems []string
	warnings []string
}

// line prints "  <mark> <label>  <text>".
func (r *report) line(mark, label, text string) {
	fmt.Fprintf(r.w, "  %s %-*s %s\n", mark, labelWidth, label, text)
}

// detail prints a continuation line aligned under the label column.
func (r *report) detail(text string) {
	fmt.Fprintf(r.w, "    %-*s %s\n", labelWidth, "", text)
}

func (r *report) ok(label, text string)   { r.line("✓", label, text) }
func (r *report) info(label, text string) { r.line("·", label, text) }

// warn reports a problem that does not make the setup unusable.
func (r *report) warn(label, text string) {
	r.line("!", label, text)
	r.warnings = append(r.warnings, label+": "+text)
}

// fail reports a problem that does.
func (r *report) fail(label, text string) {
	r.line("✗", label, text)
	r.problems = append(r.problems, label+": "+text)
}

func (r *report) binary() {
	r.ok("version", "ouija "+version.Version)

	if exe, err := os.Executable(); err == nil {
		r.ok("binary", exe)
	}

	if p, err := exec.LookPath("ouija"); err == nil {
		r.ok("on PATH", p)
	} else {
		r.warn("on PATH", "`ouija` is not on PATH — harnesses spawn it by name")
	}
}

func (r *report) configFiles() {
	global, err := ouijaconfig.Load()
	switch {
	case err != nil:
		r.fail("global config", ouijaconfig.Path()+" — "+err.Error())
	case global == nil:
		r.warn("global config", "not found — run `ouija setup` to add API keys")
	default:
		r.ok("global config", ouijaconfig.Path())
		if prov := global.DefaultProvider(); prov == "" {
			r.warn("defaults", "no default provider set — run `ouija setup`")
		} else {
			r.detail("defaults: " + prov + " / " + global.DefaultModel())
		}
		if keys := keyNames(global); len(keys) == 0 {
			r.detail("keys: none")
		} else {
			r.detail("keys: " + strings.Join(keys, ", "))
		}
	}
}

func (r *report) provider() {
	cfg, cfgPath, err := app.DiscoverConfig("")
	if err != nil {
		r.fail("project config", err.Error())
		return
	}

	if cfgPath == "" {
		r.info("project config", "none — using global defaults")
	} else {
		r.ok("project config", cfgPath)
		r.detail(fmt.Sprintf("provider: %s · model: %s", cfg.Provider, cfg.Model))
	}

	if cfg.Provider == "" {
		r.fail("provider", "none resolved — run `ouija setup` (global) or `ouija init` (project)")
		return
	}

	switch {
	case !providers.Known(cfg.Provider):
		r.fail("provider", fmt.Sprintf("%q is not a known provider", cfg.Provider))
	case providers.Keyless(cfg.Provider):
		r.ok("provider", cfg.Provider+" (no key required)")
	case cfg.APIKey != "":
		r.ok("provider", cfg.Provider+" (key set)")
	default:
		// No provider reads keys from the environment, so a missing key means
		// every totem run will fail immediately. Report it as a real problem.
		r.fail("provider", fmt.Sprintf("%s has no API key — run `ouija setup`", cfg.Provider))
	}

	if err := app.ValidateConfig(cfg); err != nil {
		r.fail("config", err.Error())
	} else {
		r.ok("config", "valid")
	}
}

func (r *report) totems() {
	names := make([]string, 0, len(registry.List()))
	for _, t := range registry.List() {
		names = append(names, t.Name())
	}
	if len(names) == 0 {
		r.fail("totems", "none registered — the binary was built without totems")
		return
	}
	r.ok("totems", fmt.Sprintf("%d registered: %s", len(names), strings.Join(names, ", ")))
}

func (r *report) harnesses() {
	fmt.Fprintln(r.w)
	fmt.Fprintln(r.w, "  harnesses")

	states := mcp.Check()
	if len(states) == 0 {
		r.detail("could not determine (no home directory)")
		return
	}
	for _, st := range states {
		switch {
		case !st.Installed:
			r.info(st.Name, "not installed")
		case st.Err != nil:
			r.warn(st.Name, st.Err.Error())
		case st.Connected:
			r.ok(st.Name, "connected")
		default:
			r.warn(st.Name, "not connected — run `ouija mcp install`")
		}
	}
}

func (r *report) finish() error {
	fmt.Fprintln(r.w)

	switch {
	case len(r.problems) > 0:
		fmt.Fprintf(r.w, "  %d problem(s) found\n", len(r.problems))
		return fmt.Errorf("%d problem(s) found", len(r.problems))
	case len(r.warnings) > 0:
		fmt.Fprintf(r.w, "  no problems · %d warning(s)\n", len(r.warnings))
		return nil
	default:
		fmt.Fprintln(r.w, "  all good")
		return nil
	}
}

// keyNames returns the providers with a non-empty key, sorted.
func keyNames(c *ouijaconfig.Config) []string {
	names := make([]string, 0, len(c.APIKeys))
	for name, key := range c.APIKeys {
		if key != "" {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	return names
}
