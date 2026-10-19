// Package app holds the orchestration shared by every entrypoint (CLI, MCP):
// config discovery, provider wiring, and running a totem on the engine.
package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/discorgento/ouija/internal/engine"
	"github.com/discorgento/ouija/internal/engine/providers"
	"github.com/discorgento/ouija/internal/ouijaconfig"
	"github.com/discorgento/ouija/internal/registry"
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
	projectCfg, cfgPath, err := findConfigFile(explicit)
	if err != nil {
		return nil, "", err
	}

	globalCfg, _ := ouijaconfig.Load()
	cfg := engine.DefaultConfig()
	applyGlobalConfig(cfg, globalCfg)
	applyProjectConfig(cfg, projectCfg, cfgPath, explicit != "")
	// Resolve the effective API key for the final provider into cfg.APIKey, so
	// the value is explicit in the config rather than hidden in a global store.
	cfg.APIKey = resolveAPIKey(cfg.Provider, globalCfg, projectCfg)

	return cfg, cfgPath, nil
}

// findConfigFile walks up from the current directory looking for a project
// config file (.ouija/config.json, .ouija.json, ouija.json). If explicit is
// non-empty, it loads that path directly instead. Returns (nil, "", nil) when
// no project config is found.
func findConfigFile(explicit string) (*engine.Config, string, error) {
	if explicit != "" {
		c, err := engine.LoadConfig(explicit)
		if err != nil {
			return nil, "", err
		}
		return c, explicit, nil
	}

	dir, _ := os.Getwd()
	for {
		for _, rel := range []string{".ouija/config.json", ".ouija.json", "ouija.json"} {
			p := filepath.Join(dir, rel)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				if c, err := engine.LoadConfig(p); err == nil {
					return c, p, nil
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return nil, "", nil
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
	if projectCfg.Passes > 0 {
		cfg.Passes = projectCfg.Passes
	}
	if len(projectCfg.Dimensions) > 0 {
		cfg.Dimensions = projectCfg.Dimensions
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
	if def := engine.DefaultConfig(); projectCfg.MaxNudges != def.MaxNudges {
		cfg.MaxNudges = projectCfg.MaxNudges
	}
	if def := engine.DefaultConfig(); projectCfg.StreamTimeout != def.StreamTimeout {
		cfg.StreamTimeout = projectCfg.StreamTimeout
	}
	if def := engine.DefaultConfig(); projectCfg.SystemPrompt != def.SystemPrompt {
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
		if joined, jerr := engine.SafeJoin(base, cfg.ProjectRoot); jerr == nil {
			cfg.ProjectRoot = joined
		} else {
			cfg.ProjectRoot = base
		}
	}
}

// ValidateConfig checks the merged config against the provider registry.
// Call after DiscoverConfig to catch misconfiguration before engine start.
func ValidateConfig(cfg *engine.Config) error {
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

// validateProviderName reports an error when name is empty or unregistered.
func validateProviderName(name string) error {
	if name == "" {
		return engine.ErrConfig("provider name is empty — set one in config or run `ouija init`")
	}
	if !providers.Known(name) {
		return engine.ErrProvider(fmt.Sprintf("unknown provider: %s (registered: %v)", name, providers.Names()))
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
// builds the provider. It is the name-only variant used by the interactive
// chat's `/provider` switch, which has no project config key at hand.
func BuildProviderByName(name string) (engine.Provider, error) {
	if err := validateProviderName(name); err != nil {
		return nil, err
	}
	key := ""
	if globalCfg, _ := ouijaconfig.Load(); globalCfg != nil {
		key = globalCfg.APIKeys[name]
	}
	p, _ := providers.Build(name, key)
	return p, nil
}

// Run discovers config, builds the engine, initialises the totem, runs it,
// and cleans up. It returns the Outcome even when the totem itself fails (so
// callers can still report token/cost stats); only setup errors return nil.
// The span tree is always rendered to stderr (tracer no-op when inactive).
// trace enables the tracer; traceFile writes a Perfetto-compatible JSON export.
func Run(ctx context.Context, name string, args registry.Args, configPath string, trace bool, traceFile string) (*Outcome, error) {
	mod, ok := registry.Get(name)
	if !ok {
		return nil, fmt.Errorf("unknown totem: %s", name)
	}
	out, err := SetupEngine(ctx, configPath, trace || traceFile != "")
	if err != nil {
		return nil, err
	}

	// Lifecycle: Init → Run → Cleanup (Cleanup always runs).
	var runErr error
	if initErr := mod.Init(ctx, out.Engine, out.Config); initErr != nil {
		if cerr := mod.Cleanup(); cerr != nil {
			out.Engine.Logger().Warn("totem cleanup failed after init error", "totem", name, "error", cerr)
		}
		out.Engine.Close()
		return nil, fmt.Errorf("init: %w", initErr)
	}
	res, runErr := mod.Run(ctx, out.Engine, args)
	out.Result = res

	if cerr := mod.Cleanup(); cerr != nil && runErr == nil {
		runErr = fmt.Errorf("cleanup: %w (after run)", cerr)
	}

	// Render trace tree to stderr (tracer no-op when inactive).
	out.Engine.Tracer().Render(os.Stderr)

	// Export Perfetto JSON when a file path is given.
	if traceFile != "" {
		if err := out.Engine.Tracer().WritePerfettoFile(traceFile); err != nil {
			fmt.Fprintf(os.Stderr, "[ouija] trace file error: %v\n", err)
		}
	}

	out.Engine.Close()
	return out, runErr
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
	tr := engine.NewTracer(trace)

	eng := engine.New(engine.EngineConfig{Provider: prov, Config: cfg, Tracer: tr})
	return &Outcome{Engine: eng, Config: cfg, ConfigPath: cfgPath}, nil
}
