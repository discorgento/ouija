// Package app holds the orchestration shared by every entrypoint (CLI, MCP):
// config discovery, provider wiring, and running a totem on the engine.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/providers"
	"github.com/discorgento/ouija/internal/models"
	"github.com/discorgento/ouija/internal/ouijaconfig"
	"github.com/discorgento/ouija/registry"
)

// Outcome is the result of running a totem, plus the engine/config for stats.
type Outcome struct {
	Result     any
	Engine     *engine.Engine
	Config     *engine.Config
	ConfigPath string
}

// DiscoverConfig loads the explicit path if given, else walks up from the
// current directory looking for .ouija/config.json / .ouija.json / ouija.json.
// Then it merges with the global ~/.config/ouija/config.json (for API keys).
// Falls back to defaults if nothing is found.
func DiscoverConfig(explicit string) (*engine.Config, string, error) {
	projectCfg, cfgPath, data, err := findConfigFile(explicit)
	if err != nil {
		return nil, "", err
	}
	// A global config that does not parse is reported: ignoring it ran with
	// no provider and sent the user to `ouija setup`, which used to save over it.
	globalCfg, err := ouijaconfig.Load()
	if err != nil {
		return nil, "", err
	}
	// A discovered config may come from a cloned repository: its sensitive
	// keys apply only once the user trusts the file (see trust.go). An
	// explicit --config is trusted like any CLI flag. The check uses the same
	// bytes that were parsed, so the file cannot change in between.
	// Sensitive fields are cleared on every untrusted file, whatever the key
	// detection says: what the user sees warned about is not the guard.
	if projectCfg != nil && explicit == "" && !trusted(globalCfg, cfgPath, data) {
		stripUntrusted(projectCfg, globalCfg)
		if keys, _ := sensitiveKeysIn(data, globalCfg); len(keys) > 0 {
			slog.Warn("untrusted project config: ignoring "+strings.Join(keys, ", ")+
				" — run `ouija trust` to apply them", "path", cfgPath)
		}
	}

	cfg := engine.DefaultConfig()
	applyGlobalConfig(cfg, globalCfg)
	applyProjectConfig(cfg, projectCfg, cfgPath, explicit != "")
	applyGlobalSandbox(cfg, globalCfg)
	// Resolve the effective API key for the final provider into cfg.APIKey, so
	// the value is explicit in the config rather than hidden in a global store.
	cfg.APIKey = resolveAPIKey(cfg.Provider, globalCfg, projectCfg)

	return cfg, cfgPath, nil
}

// findConfigFile walks up from the current directory looking for a project
// config file (.ouija/config.json, .ouija.json, ouija.json), no higher than
// the home directory when it starts inside it, skipping a file another user
// owns. If explicit is
// non-empty, it loads that path directly instead. It returns the parsed
// config, its path and the bytes it was parsed from; (nil, "", nil, nil) when
// no project config is found.
func findConfigFile(explicit string) (*engine.Config, string, []byte, error) {
	load := func(p string) (*engine.Config, string, []byte, error) {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, "", nil, err
		}
		c, err := parseProjectConfig(b)
		if err != nil {
			return nil, "", nil, fmt.Errorf("%s: %w", p, err)
		}
		return c, p, b, nil
	}
	if explicit != "" {
		return load(explicit)
	}

	dir, _ := os.Getwd()
	// Not above the home directory when dir is inside it: above it are
	// directories the user does not own — /home, / — where a config someone
	// else put would apply to all of the user's projects. Compared as files,
	// not names, so a symlink (macOS /Users under /System/Volumes/Data) or a
	// Windows short name (RUNNER~1) cannot hide that the walk reached home.
	var home os.FileInfo
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		home, _ = os.Stat(h)
	}
	for {
		for _, rel := range []string{".ouija/config.json", ".ouija.json", "ouija.json"} {
			p := filepath.Join(dir, rel)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				// Another user's file is skipped, not an error: it would
				// otherwise stop ouija from starting anywhere below it.
				if foreignOwner(st) {
					slog.Warn("ignoring a project config owned by another user", "path", p)
					continue
				}
				if err := staysInDir(p, dir); err != nil {
					return nil, "", nil, err
				}
				// A broken file is an error: silently skipping it would run
				// with the parent's or the global config instead.
				return load(p)
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		if fi, err := os.Stat(dir); err == nil && home != nil && os.SameFile(fi, home) {
			break
		}
		dir = parent
	}
	return nil, "", nil, nil
}

// staysInDir refuses a discovered config whose real file lies outside dir,
// the directory it was found in. A repository can ship .ouija.json as a
// symlink: to another project's trusted config it would borrow that trust (it
// is keyed on the real file), and to ~/.config/ouija/config.json a chat save
// would overwrite the global config, API keys and sandbox included. A symlink
// that stays inside the directory is fine.
func staysInDir(p, dir string) error {
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		return err
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if rel, err := filepath.Rel(realDir, real); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s points outside %s (to %s): ouija does not load a project config from elsewhere — copy the file instead of linking it", p, dir, real)
	}
	return nil
}

// applyGlobalSandbox makes the global sandbox a floor no project can lower.
// It runs after the project overlay: a project's sandbox that is off (or
// absent) is replaced by the global one, and an enabled one keeps its extra
// paths and ports on top of the global ones. Only a trusted project reaches
// here with a sandbox section at all; an untrusted one had it stripped.
func applyGlobalSandbox(cfg *engine.Config, globalCfg *ouijaconfig.Config) {
	if globalCfg == nil || globalCfg.Sandbox == nil {
		return // the engine's default sandbox applies, unless the project set one
	}
	if !globalCfg.Sandbox.Enabled {
		// Turned off globally: off, unless a trusted project turns it on.
		if cfg.Tools == nil || cfg.Tools.Bash == nil || cfg.Tools.Bash.Sandbox == nil {
			setBashSandbox(cfg, &engine.SandboxConfig{Enabled: false})
		}
		return
	}
	var project *engine.SandboxConfig
	if cfg.Tools != nil && cfg.Tools.Bash != nil {
		project = cfg.Tools.Bash.Sandbox
	}
	setBashSandbox(cfg, mergeSandbox(globalCfg.Sandbox, project))
}

// setBashSandbox sets cfg's bash sandbox on copies of its tools sections:
// the project's structs are not ours to change.
func setBashSandbox(cfg *engine.Config, sb *engine.SandboxConfig) {
	if cfg.Tools == nil {
		cfg.Tools = &engine.ToolsConfig{}
	} else {
		tools := *cfg.Tools
		cfg.Tools = &tools
	}
	if cfg.Tools.Bash == nil {
		cfg.Tools.Bash = &engine.BashConfig{}
	} else {
		bash := *cfg.Tools.Bash
		cfg.Tools.Bash = &bash
	}
	cfg.Tools.Bash.Sandbox = sb
}

// mergeSandbox is the global sandbox plus what an enabled project sandbox
// adds: its extra read/write paths and TCP ports. A disabled or absent one
// adds nothing.
func mergeSandbox(global, project *engine.SandboxConfig) *engine.SandboxConfig {
	out := &engine.SandboxConfig{
		Enabled:          true,
		Read:             slices.Clone(global.Read),
		Write:            slices.Clone(global.Write),
		AllowUnsandboxed: global.AllowUnsandboxed,
	}
	if global.Network != nil {
		n := *global.Network
		n.Connect, n.Bind = slices.Clone(n.Connect), slices.Clone(n.Bind)
		out.Network = &n
	}
	if project == nil || !project.Enabled {
		return out
	}
	// A project may forbid the escape hatch, never allow it over the global.
	if !project.UnsandboxedAllowed() {
		forbid := false
		out.AllowUnsandboxed = &forbid
	}
	out.Read = append(out.Read, project.Read...)
	out.Write = append(out.Write, project.Write...)
	if p := project.Network; p != nil {
		if out.Network == nil {
			out.Network = &engine.SandboxNetwork{}
		}
		out.Network.All = out.Network.All || p.All
		out.Network.Connect = append(out.Network.Connect, p.Connect...)
		out.Network.Bind = append(out.Network.Bind, p.Bind...)
	}
	return out
}

// applyGlobalConfig merges the user's global config (~/.config/ouija/config.json)
// into cfg: it sets the default provider and model. API keys are resolved
// separately by resolveAPIKey, keyed by the final provider name.
func applyGlobalConfig(cfg *engine.Config, globalCfg *ouijaconfig.Config) {
	if globalCfg == nil {
		return
	}
	cfg.Provider = globalCfg.DefaultProvider()
	cfg.Model = globalCfg.DefaultModel()
}

// resolveAPIKey returns the effective API key for provider: the global config's
// key for that provider, overridden by a project-level key when present. It
// returns "" when neither source defines a key.
func resolveAPIKey(provider string, globalCfg *ouijaconfig.Config, projectCfg *engine.Config) string {
	key := ""
	if globalCfg != nil {
		key = globalCfg.APIKeys[provider]
	}
	if projectCfg != nil && projectCfg.APIKey != "" {
		key = projectCfg.APIKey
	}
	return key
}

// applyProjectConfig overlays the project-specific config on top of cfg.
// Fields set in projectCfg take precedence; zero-value fields are left as-is.
// It also resolves ProjectRoot relative to the config file location.
func applyProjectConfig(cfg *engine.Config, projectCfg *engine.Config, cfgPath string, explicit bool) {
	if projectCfg == nil {
		return
	}
	if projectCfg.Provider != "" {
		cfg.Provider = projectCfg.Provider
	}
	if projectCfg.Model != "" {
		cfg.Model = projectCfg.Model
	}
	for name, raw := range projectCfg.Totems {
		if cfg.Totems == nil {
			cfg.Totems = make(map[string]json.RawMessage)
		}
		cfg.Totems[name] = raw
	}
	if len(projectCfg.Rules) > 0 {
		cfg.Rules = projectCfg.Rules
	}
	if projectCfg.ProjectRoot != "" {
		cfg.ProjectRoot = projectCfg.ProjectRoot
	}
	cfg.Budget = projectCfg.Budget
	cfg.CostPer1MInput = projectCfg.CostPer1MInput
	cfg.CostPer1MOutput = projectCfg.CostPer1MOutput

	// Optional sections: LoadConfig starts from DefaultConfig and overlays the
	// JSON, so an absent key still holds the default value. Pointers/maps are
	// copied when present (nil = key absent); scalars are copied when they
	// differ from the built-in default, which detects an explicit override —
	// including an explicit 0 (maxNudges) — while an absent key keeps whatever
	// cfg already holds. Without this block the whole `permission`,
	// `tools.bash.allowlist`, `streamTimeout`, `maxNudges`, `systemPrompt` and
	// `modelSuggestions` sections documented for .ouija/config.json were parsed
	// and then silently dropped before the engine ever saw them.
	if projectCfg.Permission != nil {
		cfg.Permission = projectCfg.Permission
	}
	if projectCfg.Tools != nil {
		cfg.Tools = projectCfg.Tools
	}
	if projectCfg.ModelSuggestions != nil {
		cfg.ModelSuggestions = projectCfg.ModelSuggestions
	}
	def := engine.DefaultConfig()
	if projectCfg.MaxNudges != def.MaxNudges {
		cfg.MaxNudges = projectCfg.MaxNudges
	}
	if projectCfg.StreamTimeout != def.StreamTimeout {
		cfg.StreamTimeout = projectCfg.StreamTimeout
	}
	if projectCfg.ContextWindow > 0 {
		cfg.ContextWindow = projectCfg.ContextWindow
	}
	if projectCfg.SystemPrompt != def.SystemPrompt {
		cfg.SystemPrompt = projectCfg.SystemPrompt
	}

	// Resolve project root relative to config file location.
	resolveProjectRoot(cfg, cfgPath, explicit)
}

// resolveProjectRoot makes ProjectRoot absolute. When no explicit
// projectRoot is set, it defaults to the current working directory so the
// sandbox matches where the user ran the command (OpenCode/Claude Code
// pattern). An explicit projectRoot in config is resolved relative to the
// config file's directory for backward compatibility.
func resolveProjectRoot(cfg *engine.Config, cfgPath string, explicit bool) {
	if cfgPath == "" {
		return
	}
	base := filepath.Dir(cfgPath)
	if filepath.Base(base) == ".ouija" {
		base = filepath.Dir(base)
	}
	switch {
	case cfg.ProjectRoot == "" || cfg.ProjectRoot == ".":
		cfg.ProjectRoot, _ = os.Getwd()
	case !explicit:
		cfg.ProjectRoot = containedRoot(base, cfg.ProjectRoot)
	}
}

// containedRoot joins rel onto base and keeps the result inside base — also
// physically: SafeJoin is lexical, and the sandbox root is later resolved
// through symlinks, so `src -> ../..` would have made it the home directory.
// Anything that leaves base falls back to base.
func containedRoot(base, rel string) string {
	joined, err := engine.SafeJoin(base, rel)
	if err != nil {
		return base
	}
	canonBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		canonBase = base
	}
	if real, err := filepath.EvalSymlinks(joined); err == nil {
		if r, err := filepath.Rel(canonBase, real); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
			return base
		}
	}
	return joined
}

// ValidateConfig checks the merged config against the provider registry.
// Call after DiscoverConfig to catch misconfiguration before engine start.
func ValidateConfig(cfg *engine.Config) error {
	// No layer chose a model: say how to choose one, instead of a bare
	// "provider is required". ouija has no built-in default on purpose.
	if cfg.Provider == "" || cfg.Model == "" {
		return engine.ErrConfig(noModelChosen)
	}
	// Base field validation.
	if err := cfg.Validate(); err != nil {
		return engine.ErrConfig(err.Error())
	}
	// Provider must be registered.
	if err := validateProviderName(cfg.Provider); err != nil {
		return err
	}
	return nil
}

// noModelChosen is the error when no config layer names a provider and model.
const noModelChosen = "no provider/model chosen — run `ouija setup` to pick your default, " +
	"or `ouija init` (or `provider`/`model` in the project config) for this project"

// validateProviderName reports an error when name is empty or unregistered.
func validateProviderName(name string) error {
	if name == "" {
		return engine.ErrConfig(noModelChosen)
	}
	if !providers.Known(name) {
		return engine.ErrConfig(fmt.Sprintf("unknown provider: %s (registered: %v)", name, providers.Names()))
	}
	return nil
}

// BuildProvider returns a new provider instance by name, wired with apiKey.
// The key is resolved by the caller (see DiscoverConfig → cfg.APIKey) and
// passed explicitly: providers hold no package-level state.
func BuildProvider(name, apiKey string) (engine.Provider, error) {
	if err := validateProviderName(name); err != nil {
		return nil, err
	}
	p, _ := providers.Build(name, apiKey)
	return p, nil
}

// BuildProviderByName resolves the API key for name from the global config and
// builds the provider.
func BuildProviderByName(name string) (engine.Provider, error) {
	globalCfg, _ := ouijaconfig.Load()
	return BuildProvider(name, resolveAPIKey(name, globalCfg, nil))
}

// ProviderFactory builds providers for the chat's `/provider` switch. The
// project's own key belongs to the provider it was resolved for: switching
// back to that provider reuses it, any other takes its key from the global
// config (a client key sent to another vendor would only fail auth).
func (o *Outcome) ProviderFactory() func(name string) (engine.Provider, error) {
	provider, key := o.Config.Provider, o.Config.APIKey
	return func(name string) (engine.Provider, error) {
		if name == provider {
			return BuildProvider(name, key)
		}
		return BuildProviderByName(name)
	}
}

// Run discovers config, builds the engine, initialises the totem, runs it,
// and cleans up. It returns the Outcome even when the totem itself fails (so
// callers can still report token/cost stats); only setup errors return nil.
// The span tree is always rendered to stderr (tracer no-op when inactive).
// trace enables the tracer; traceFile writes a Perfetto-compatible JSON export.
//
// progress, when not nil, watches the run (the CLI's live board); it is
// stopped before the span tree is printed.
func Run(ctx context.Context, name string, args registry.Args, configPath string, trace bool, traceFile string, progress Progress) (*Outcome, error) {
	mod, ok := registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown totem: %s", name)
	}
	out, err := SetupEngine(ctx, configPath, trace || traceFile != "")
	if err != nil {
		return nil, err
	}

	// Lifecycle (required inputs → Init → Run → Cleanup) lives in Invoke, so
	// every host runs totems the same way.
	if progress != nil {
		progress.Start(out.Engine)
	}
	res, runErr := registry.Invoke(ctx, out.Engine, mod, args)
	if progress != nil {
		progress.Stop()
	}
	out.Result = res

	FinishTrace(out.Engine.Tracer(), os.Stderr, traceFile)
	out.Engine.Close()
	return out, runErr
}

// Progress watches a totem run: Start is called with its engine before the
// totem runs, Stop once it returned.
type Progress interface {
	Start(eng *engine.Engine)
	Stop()
}

// FinishTrace renders the span tree to w and, when file is set, writes the
// Perfetto export there. Both are no-ops on an inactive tracer.
func FinishTrace(t *engine.Tracer, w io.Writer, file string) {
	t.Render(w)
	if file == "" {
		return
	}
	if err := t.WritePerfettoFile(file); err != nil {
		fmt.Fprintf(w, "[ouija] trace file: %v\n", err)
	}
}

// SetupEngine discovers config, applies env overrides, and builds the engine.
// Useful for interactive commands (chat) that don't map to a totem.
// Set trace to true to enable console tree output to stderr.
func SetupEngine(ctx context.Context, configPath string, trace bool) (*Outcome, error) {
	cfg, cfgPath, err := DiscoverConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	prov, err := BuildProvider(cfg.Provider, cfg.APIKey)
	if err != nil {
		return nil, err
	}
	if cfg.ContextWindow <= 0 { // an explicit contextWindow wins
		cfg.ContextWindow = models.Load().ContextWindow(cfg.Provider, cfg.Model, nil)
	}
	tr := engine.NewTracer(trace)

	eng := engine.New(engine.EngineConfig{Provider: prov, Config: cfg, Tracer: tr})
	return &Outcome{Engine: eng, Config: cfg, ConfigPath: cfgPath}, nil
}
