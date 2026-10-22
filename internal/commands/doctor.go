package commands

import (
	"context"
	"fmt"
	"image/color"
	"io"
	"os"
	"os/exec"
	"runtime"
	"slices"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/providers"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/mcp"
	"github.com/discorgento/ouija/internal/ouijaconfig"
	"github.com/discorgento/ouija/internal/termsafe"
	"github.com/discorgento/ouija/internal/theme"
	"github.com/discorgento/ouija/internal/version"
	"github.com/discorgento/ouija/registry"
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
	// Colors only where the output can show them (stripped when piped).
	w = colorprofile.NewWriter(w, os.Environ())
	r := &report{w: w}

	fmt.Fprintln(w)
	fmt.Fprintln(w, "  "+theme.GradientText("ouija doctor", 0)+docDim.Render("  ·  is everything in place?"))

	r.section("install")
	r.binary()
	r.section("config")
	r.configFiles()
	r.section("provider")
	r.provider()
	r.section("safety")
	r.sandbox()
	r.section("totems")
	r.totems()
	r.section("harnesses")
	r.harnesses()

	return r.finish()
}

var (
	docOK      = lipgloss.NewStyle().Bold(true).Foreground(theme.Green)
	docWarn    = lipgloss.NewStyle().Bold(true).Foreground(theme.Amber)
	docFail    = lipgloss.NewStyle().Bold(true).Foreground(theme.Red)
	docDim     = lipgloss.NewStyle().Foreground(theme.Muted)
	docLabel   = lipgloss.NewStyle().Foreground(theme.Lavender)
	docSection = lipgloss.NewStyle().Bold(true).Foreground(theme.Pink)
	docText    = lipgloss.NewStyle().Foreground(theme.Text)
)

// section starts a group of checks under a heading.
func (r *report) section(title string) {
	fmt.Fprintf(r.w, "\n  %s\n", docSection.Render("◆ "+title))
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
	style := map[string]lipgloss.Style{"✓": docOK, "!": docWarn, "✗": docFail}[mark]
	if mark == "·" {
		style = docDim
	}
	textStyle := docText
	switch mark {
	case "!":
		textStyle = docWarn.UnsetBold()
	case "✗":
		textStyle = docFail.UnsetBold()
	}
	// text holds config values and errors from a repository or a server.
	fmt.Fprintf(r.w, "  %s %s %s\n", style.Render(mark), docLabel.Render(fmt.Sprintf("%-*s", labelWidth, label)), textStyle.Render(termsafe.Clean(text)))
}

// detail prints a continuation line aligned under the label column.
func (r *report) detail(text string) {
	fmt.Fprintf(r.w, "    %-*s %s\n", labelWidth, "", docDim.Render(termsafe.Clean(text)))
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

	exe, _ := os.Executable()
	if exe != "" {
		r.ok("binary", exe)
	}

	if p, err := exec.LookPath("ouija"); err == nil {
		// Harnesses run the one on PATH: a different, older binary there
		// (a stale `go install`, an archive) serves them while this one
		// reports all good.
		if exe != "" && !sameFile(exe, p) {
			r.warn("on PATH", p+" is a different binary from this one — harnesses run that one; reinstall or fix PATH")
		} else {
			r.ok("on PATH", p)
		}
	} else {
		r.warn("on PATH", "`ouija` is not on PATH — harnesses spawn it by name")
	}
}

// sameFile reports whether two paths are the same file, symlinks resolved.
func sameFile(a, b string) bool {
	fa, errA := os.Stat(a)
	fb, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(fa, fb)
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
		if keys, _ := app.SensitiveKeys(cfgPath); len(keys) > 0 && !app.IsTrusted(cfgPath) {
			r.detail("untrusted: " + strings.Join(keys, ", ") + " ignored — run `ouija trust` to apply them")
		}
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
		return
	}
	r.ok("config", "valid")
	r.providerAPI(cfg)
}

// probeModels lists the provider's models — the cheapest real call that
// proves the key and endpoint work. A variable so tests stay off the network.
var probeModels = func(ctx context.Context, l engine.ModelLister) ([]engine.ModelInfo, error) {
	return l.ListModels(ctx)
}

// providerAPI checks the configured key against the provider's API: setup
// and the pickers fall back to built-in suggestions without a word when the
// key is wrong, so this is where a bad or revoked key shows.
func (r *report) providerAPI(cfg *engine.Config) {
	p, err := app.BuildProvider(cfg.Provider, cfg.APIKey)
	if err != nil {
		return
	}
	lister, ok := p.(engine.ModelLister)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := probeModels(ctx, lister)
	switch {
	case engine.Is(err, engine.ErrCodeAuth):
		r.fail("provider API", err.Error()+" — check the key with `ouija setup`")
	case err != nil:
		r.warn("provider API", "could not list models: "+err.Error())
	case !slices.ContainsFunc(list, func(m engine.ModelInfo) bool { return m.ID == cfg.Model }):
		r.warn("provider API", fmt.Sprintf("reachable, but model %q is not among its %d models", cfg.Model, len(list)))
	default:
		r.ok("provider API", fmt.Sprintf("reachable · %s available", cfg.Model))
	}
}

// sandbox says whether bash runs confined by the OS, and how to turn that on
// where this machine can enforce it. It is not a failure when off: bash still
// asks before every command.
func (r *report) sandbox() {
	cfg, _, err := app.DiscoverConfig("")
	sb := engine.DefaultSandbox() // what the engine runs with when no config sets one
	if err == nil && cfg.Tools != nil && cfg.Tools.Bash != nil && cfg.Tools.Bash.Sandbox != nil {
		sb = cfg.Tools.Bash.Sandbox
	}
	avail := engine.SandboxAvailable()
	switch {
	case sb.Enabled && avail == nil:
		net := "TCP closed"
		if n := sb.Network; n != nil && (n.All || len(n.Connect) > 0 || len(n.Bind) > 0) {
			net = "TCP open on the ports the config lists"
			if n.All {
				net = "network not restricted"
			}
		}
		how := "on"
		if sb.Optional {
			how = "on (the default)"
		}
		r.ok("bash sandbox", how+" — the OS confines every command ("+net+")")
		switch {
		case runtime.GOOS != "linux":
		case engine.SandboxEndsDetachedJobs():
			r.ok("bash jobs", "each command runs in a PID namespace of its own: nothing it starts outlives it")
		default:
			r.warn("bash jobs", "user namespaces are forbidden here, so a process a command detaches (setsid) can outlive it and write in the project later")
		}
	case sb.Enabled && sb.Optional:
		r.warn("bash sandbox", "on by default, but this machine cannot enforce it, so bash runs unconfined (it asks before every command): "+avail.Error())
	case sb.Enabled:
		r.fail("bash sandbox", "on in the config, but this machine cannot enforce it, so bash refuses to run: "+avail.Error())
	case avail == nil:
		r.warn("bash sandbox", "off in the config — this machine supports it: remove \"enabled\": false to confine bash")
	default:
		r.info("bash sandbox", "off, and not available here ("+avail.Error()+"); bash asks before every command")
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
	pill := func(bg color.Color, text string) string {
		return lipgloss.NewStyle().Bold(true).Foreground(theme.Surface).Background(bg).Render(" " + text + " ")
	}

	switch {
	case len(r.problems) > 0:
		fmt.Fprintf(r.w, "  %s  %s\n", pill(theme.Red, fmt.Sprintf("%d problem(s) found", len(r.problems))), docDim.Render("fix the ✗ lines above"))
		return fmt.Errorf("%d problem(s) found", len(r.problems))
	case len(r.warnings) > 0:
		fmt.Fprintf(r.w, "  %s  %s\n", pill(theme.Amber, fmt.Sprintf("no problems · %d warning(s)", len(r.warnings))), docDim.Render("ouija works; the ! lines say what could be better"))
		return nil
	default:
		fmt.Fprintf(r.w, "  %s  %s\n", pill(theme.Green, "all good"), docDim.Render("run `ouija chat` in a project"))
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
