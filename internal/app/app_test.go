package app

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/providers"
	"github.com/discorgento/ouija/internal/ouijaconfig"
	"github.com/discorgento/ouija/registry"
)

// isolateGlobalConfig points the global (~/.config/ouija) lookup at a temp dir
// so these tests never read the developer's real config or API keys.
func isolateGlobalConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)
}

// userChose saves the provider and model the user picked in `ouija setup`:
// an untrusted project config may not switch away from them.
func userChose(t *testing.T, provider, model string) {
	t.Helper()
	if err := ouijaconfig.Save(&ouijaconfig.Config{Defaults: &ouijaconfig.Defaults{Provider: provider, Model: model}}); err != nil {
		t.Fatal(err)
	}
}

func writeProjectConfig(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// minimalConfig is valid on its own: LoadConfig fills the rest from
// DefaultConfig, so only the two required fields need to be present.
const minimalConfig = `{"provider":"ollama","model":"llama3"}`

func TestBuildProvider(t *testing.T) {
	if _, err := BuildProvider("", ""); err == nil {
		t.Error(`BuildProvider("") error = nil, want an error`)
	}

	_, err := BuildProvider("no-such-provider", "")
	if err == nil {
		t.Fatal("BuildProvider(unknown) error = nil, want an error")
	}
	if !strings.Contains(err.Error(), "unknown provider") {
		t.Errorf("error = %q, want it to mention \"unknown provider\"", err)
	}
	if !strings.Contains(err.Error(), "registered") {
		t.Errorf("error = %q, want it to list the registered providers", err)
	}

	// A registered provider must return a usable instance. "opencode" is
	// registered by the providers package's init().
	p, err := BuildProvider("opencode", "sk-test")
	if err != nil {
		t.Fatalf("BuildProvider(opencode) error = %v", err)
	}
	if p == nil {
		t.Fatal("BuildProvider(opencode) = nil provider")
	}
}

func TestBuildProviderByName(t *testing.T) {
	isolateGlobalConfig(t)

	if _, err := BuildProviderByName("no-such-provider"); err == nil {
		t.Error("BuildProviderByName(unknown) error = nil, want an error")
	}

	// With no global config the key resolves to "", but a keyless provider
	// still builds fine.
	if _, err := BuildProviderByName("ollama"); err != nil {
		t.Errorf("BuildProviderByName(ollama) error = %v, want nil", err)
	}
}

func TestResolveAPIKey(t *testing.T) {
	global := &ouijaconfig.Config{APIKeys: map[string]string{"anthropic": "global-key"}}

	t.Run("global key", func(t *testing.T) {
		if got := resolveAPIKey("anthropic", global, nil); got != "global-key" {
			t.Errorf("resolveAPIKey = %q, want global-key", got)
		}
	})

	t.Run("project overrides global", func(t *testing.T) {
		project := &engine.Config{APIKey: "project-key"}
		if got := resolveAPIKey("anthropic", global, project); got != "project-key" {
			t.Errorf("resolveAPIKey = %q, want project-key", got)
		}
	})

	t.Run("no sources", func(t *testing.T) {
		if got := resolveAPIKey("anthropic", nil, nil); got != "" {
			t.Errorf("resolveAPIKey = %q, want empty", got)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		if got := resolveAPIKey("openai", global, nil); got != "" {
			t.Errorf("resolveAPIKey = %q, want empty", got)
		}
	})
}

func TestDiscoverConfig_ResolvesProjectAPIKey(t *testing.T) {
	isolateGlobalConfig(t)

	dir := t.TempDir()
	p := filepath.Join(dir, "ouija.json")
	writeProjectConfig(t, p, `{"provider":"opencode","model":"zen","apiKey":"sk-project"}`)

	cfg, _, err := DiscoverConfig(p)
	if err != nil {
		t.Fatalf("DiscoverConfig() error = %v", err)
	}
	if cfg.APIKey != "sk-project" {
		t.Errorf("cfg.APIKey = %q, want sk-project", cfg.APIKey)
	}
}

func TestDiscoverConfig_ExplicitPath(t *testing.T) {
	isolateGlobalConfig(t)

	dir := t.TempDir()
	p := filepath.Join(dir, "custom.json")
	writeProjectConfig(t, p, minimalConfig)

	cfg, gotPath, err := DiscoverConfig(p)
	if err != nil {
		t.Fatalf("DiscoverConfig() error = %v", err)
	}
	if gotPath != p {
		t.Errorf("config path = %q, want %q", gotPath, p)
	}
	if cfg.Provider != "ollama" || cfg.Model != "llama3" {
		t.Errorf("provider/model = %q/%q, want ollama/llama3", cfg.Provider, cfg.Model)
	}
}

// Walk-up discovery must find the config from a nested directory, and
// .ouija/config.json wins over ouija.json when both exist.
func TestDiscoverConfig_WalksUpAndPrefersDotOuija(t *testing.T) {
	isolateGlobalConfig(t)

	root := t.TempDir()
	writeProjectConfig(t, filepath.Join(root, "ouija.json"), `{"provider":"openai","model":"gpt-4o"}`)
	writeProjectConfig(t, filepath.Join(root, ".ouija", "config.json"), minimalConfig)
	userChose(t, "ollama", "llama3")

	nested := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Chdir(nested)

	cfg, gotPath, err := DiscoverConfig("")
	if err != nil {
		t.Fatalf("DiscoverConfig() error = %v", err)
	}

	want := filepath.Join(root, ".ouija", "config.json")
	if gotPath != want {
		t.Errorf("config path = %q, want %q", gotPath, want)
	}
	if cfg.Provider != "ollama" {
		t.Errorf("provider = %q, want ollama (from .ouija/config.json)", cfg.Provider)
	}
}

func TestDiscoverConfig_NoConfigFallsBackToDefaults(t *testing.T) {
	isolateGlobalConfig(t)

	t.Chdir(t.TempDir())

	cfg, gotPath, err := DiscoverConfig("")
	if err != nil {
		t.Fatalf("DiscoverConfig() error = %v", err)
	}
	if gotPath != "" {
		t.Errorf("config path = %q, want empty when no project config exists", gotPath)
	}
	if cfg.Provider != engine.DefaultConfig().Provider {
		t.Errorf("provider = %q, want the built-in default %q", cfg.Provider, engine.DefaultConfig().Provider)
	}
}

func TestDiscoverConfig_ProjectOverridesDefaults(t *testing.T) {
	isolateGlobalConfig(t)

	dir := t.TempDir()
	p := filepath.Join(dir, "ouija.json")
	writeProjectConfig(t, p, `{
		"provider": "ollama",
		"model": "llama3",
		"totems": {"task": {"passes": 7}},
		"rules": ["one rule"]
	}`)

	cfg, _, err := DiscoverConfig(p)
	if err != nil {
		t.Fatalf("DiscoverConfig() error = %v", err)
	}

	if got := string(cfg.Totems["task"]); got != `{"passes": 7}` {
		t.Errorf("totems.task = %s, want the project's section", got)
	}
	if len(cfg.Rules) != 1 || cfg.Rules[0] != "one rule" {
		t.Errorf("rules = %v, want [one rule]", cfg.Rules)
	}
}

// The optional sections must survive the merge: applyProjectConfig used to copy
// only provider/model/passes/…, so a `permission`, `tools.bash.allowlist`,
// `streamTimeout`, `maxNudges`, `systemPrompt` or `modelSuggestions` block in
// .ouija/config.json was parsed by LoadConfig and then dropped before the
// engine saw it (headless runs AND the chat TUI both read cfg.Permission).
func TestDiscoverConfig_ProjectOptionalSectionsSurvive(t *testing.T) {
	isolateGlobalConfig(t)

	dir := t.TempDir()
	p := filepath.Join(dir, "ouija.json")
	writeProjectConfig(t, p, `{
		"provider": "ollama",
		"model": "llama3",
		"systemPrompt": "custom system prompt",
		"maxNudges": 0,
		"streamTimeout": 30,
		"permission": {"default": "allow", "tools": {"bash": "deny"}},
		"tools": {"bash": {"allowlist": ["git status", "npm run build"]}},
		"modelSuggestions": {"ollama": ["llama3.2"]}
	}`)

	cfg, _, err := DiscoverConfig(p)
	if err != nil {
		t.Fatalf("DiscoverConfig() error = %v", err)
	}

	if cfg.Permission == nil {
		t.Fatal("permission = nil, want the project section to reach the engine")
	}
	if cfg.Permission.Default != "allow" {
		t.Errorf("permission.default = %q, want allow", cfg.Permission.Default)
	}
	if cfg.Permission.Tools["bash"] != "deny" {
		t.Errorf("permission.tools.bash = %q, want deny", cfg.Permission.Tools["bash"])
	}
	if cfg.Tools == nil || cfg.Tools.Bash == nil {
		t.Fatalf("tools = %v, want tools.bash.allowlist preserved", cfg.Tools)
	}
	if got, want := cfg.Tools.Bash.Allowlist, []string{"git status", "npm run build"}; len(got) != len(want) || got[0] != want[0] {
		t.Errorf("tools.bash.allowlist = %v, want %v", got, want)
	}
	if cfg.StreamTimeout != 30 {
		t.Errorf("streamTimeout = %d, want 30", cfg.StreamTimeout)
	}
	// maxNudges: 0 is an explicit override, not an absent key — it must not
	// fall back to the built-in 3.
	if cfg.MaxNudges != 0 {
		t.Errorf("maxNudges = %d, want the explicit 0", cfg.MaxNudges)
	}
	if cfg.SystemPrompt != "custom system prompt" {
		t.Errorf("systemPrompt = %q, want the project value", cfg.SystemPrompt)
	}
	if got := cfg.ModelSuggestions["ollama"]; len(got) != 1 || got[0] != "llama3.2" {
		t.Errorf("modelSuggestions[ollama] = %v, want [llama3.2]", got)
	}
}

// An absent optional section must keep the built-in defaults — copying
// blindly would let LoadConfig's default-filled struct override cfg.
func TestDiscoverConfig_AbsentOptionalSectionsKeepDefaults(t *testing.T) {
	isolateGlobalConfig(t)

	dir := t.TempDir()
	p := filepath.Join(dir, "ouija.json")
	writeProjectConfig(t, p, minimalConfig)

	cfg, _, err := DiscoverConfig(p)
	if err != nil {
		t.Fatalf("DiscoverConfig() error = %v", err)
	}

	if cfg.Permission != nil {
		t.Errorf("permission = %v, want nil when the section is absent", cfg.Permission)
	}
	if cfg.Tools != nil {
		t.Errorf("tools = %v, want nil when the section is absent", cfg.Tools)
	}
	if cfg.MaxNudges != engine.DefaultConfig().MaxNudges {
		t.Errorf("maxNudges = %d, want the default %d", cfg.MaxNudges, engine.DefaultConfig().MaxNudges)
	}
	if cfg.SystemPrompt != engine.DefaultConfig().SystemPrompt {
		t.Error("systemPrompt = the project default, want it untouched when the key is absent")
	}
	if cfg.StreamTimeout != 0 {
		t.Errorf("streamTimeout = %d, want 0 (unset) so StreamTimeoutSec falls back to 120", cfg.StreamTimeout)
	}
	if cfg.Budget.MaxTokens != engine.DefaultConfig().Budget.MaxTokens {
		t.Errorf("budget.maxTokens = %d, want the default %d", cfg.Budget.MaxTokens, engine.DefaultConfig().Budget.MaxTokens)
	}
}

func TestResolveProjectRoot_DotBecomesWorkingDirectory(t *testing.T) {
	want, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}

	cfg := &engine.Config{ProjectRoot: "."}
	resolveProjectRoot(cfg, filepath.Join(t.TempDir(), "ouija.json"), false)

	if cfg.ProjectRoot != want {
		t.Errorf("ProjectRoot = %q, want the working directory %q", cfg.ProjectRoot, want)
	}
}

func TestResolveProjectRoot_WalkUpResolvesRelativeToConfigDir(t *testing.T) {
	cfgDir := t.TempDir()
	// The root comes back canonical: /var is /private/var on macOS, and
	// Windows' temp dir has a short 8.3 name (RUNNER~1) for a long one.
	canon, err := filepath.EvalSymlinks(cfgDir)
	if err != nil {
		t.Fatal(err)
	}

	cfg := &engine.Config{ProjectRoot: "sub"}
	resolveProjectRoot(cfg, filepath.Join(cfgDir, "ouija.json"), false)

	if want := filepath.Join(canon, "sub"); cfg.ProjectRoot != want {
		t.Errorf("ProjectRoot = %q, want %q", cfg.ProjectRoot, want)
	}
}

// A --config path is taken as authoritative: a relative projectRoot is left
// alone so it resolves against the caller's own working directory.
func TestResolveProjectRoot_ExplicitKeepsRelativePath(t *testing.T) {
	cfg := &engine.Config{ProjectRoot: "sub"}
	resolveProjectRoot(cfg, filepath.Join(t.TempDir(), "ouija.json"), true)

	if cfg.ProjectRoot != "sub" {
		t.Errorf("ProjectRoot = %q, want it left untouched", cfg.ProjectRoot)
	}
}

func TestResolveProjectRoot_NoConfigPathIsNoop(t *testing.T) {
	cfg := &engine.Config{ProjectRoot: "unchanged"}
	resolveProjectRoot(cfg, "", false)

	if cfg.ProjectRoot != "unchanged" {
		t.Errorf("ProjectRoot = %q, want it left untouched", cfg.ProjectRoot)
	}
}

func TestValidateConfig(t *testing.T) {
	t.Run("unknown provider is rejected", func(t *testing.T) {
		cfg := engine.DefaultConfig()
		cfg.Provider, cfg.Model = "no-such-provider", "m"

		if err := ValidateConfig(cfg); err == nil {
			t.Error("ValidateConfig() error = nil for an unknown provider")
		}
	})

	t.Run("missing provider is rejected", func(t *testing.T) {
		cfg := engine.DefaultConfig()
		cfg.Provider = ""

		if err := ValidateConfig(cfg); err == nil {
			t.Error("ValidateConfig() error = nil for a missing provider")
		}
	})

	t.Run("registered provider with required fields is accepted", func(t *testing.T) {
		cfg := engine.DefaultConfig()
		cfg.Provider, cfg.Model = "ollama", "llama3"

		if err := ValidateConfig(cfg); err != nil {
			t.Errorf("ValidateConfig() error = %v, want nil", err)
		}
	})
}

func TestSetupEngine_RejectsInvalidConfig(t *testing.T) {
	isolateGlobalConfig(t)

	dir := t.TempDir()
	p := filepath.Join(dir, "ouija.json")
	writeProjectConfig(t, p, `{"provider":"ollama","model":""}`)

	if _, err := SetupEngine(context.Background(), p, false); err == nil {
		t.Fatal("SetupEngine() error = nil for a config without a model, want an error")
	}
}

// --- lifecycle: Init → Run → Cleanup -----------------------------------------

type fakeTotem struct {
	name       string
	initErr    error
	cleanupErr error

	inited  bool
	ran     bool
	cleaned bool
}

func (f *fakeTotem) Name() string    { return f.name }
func (f *fakeTotem) Summary() string { return "test-only totem" }
func (f *fakeTotem) Init(context.Context, *engine.Engine) error {
	f.inited = true
	return f.initErr
}
func (f *fakeTotem) Run(context.Context, *engine.Engine, registry.Args) (any, error) {
	f.ran = true
	return "fake result", nil
}
func (f *fakeTotem) Cleanup() error {
	f.cleaned = true
	return f.cleanupErr
}

func TestRun_LifecycleInitRunCleanup(t *testing.T) {
	isolateGlobalConfig(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ouija.json")
	writeProjectConfig(t, cfgPath, minimalConfig)

	f := &fakeTotem{name: "app-test-lifecycle"}
	registry.Register(f)

	out, err := Run(context.Background(), f.Name(), registry.Args{Task: "do the thing"}, cfgPath, false, "", nil)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !f.inited || !f.ran || !f.cleaned {
		t.Errorf("lifecycle: init=%v run=%v cleanup=%v, want all true", f.inited, f.ran, f.cleaned)
	}
	if out == nil || out.Result != "fake result" {
		t.Errorf("Outcome.Result = %#v, want the totem result", out)
	}
	if out.ConfigPath != cfgPath {
		t.Errorf("Outcome.ConfigPath = %q, want %q", out.ConfigPath, cfgPath)
	}
}

func TestRun_UnknownTotem(t *testing.T) {
	isolateGlobalConfig(t)

	if _, err := Run(context.Background(), "totem-that-does-not-exist", registry.Args{}, "", false, "", nil); err == nil {
		t.Fatal("Run() error = nil for an unknown totem, want an error")
	}
}

// A failed Init must still run Cleanup (totems acquire resources in Init).
func TestRun_InitErrorStillCleansUp(t *testing.T) {
	isolateGlobalConfig(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ouija.json")
	writeProjectConfig(t, cfgPath, minimalConfig)

	f := &fakeTotem{name: "app-test-init-error", initErr: errors.New("boom")}
	registry.Register(f)

	_, err := Run(context.Background(), f.Name(), registry.Args{Task: "x"}, cfgPath, false, "", nil)
	if err == nil {
		t.Fatal("Run() error = nil after an Init failure, want an error")
	}
	if !strings.Contains(err.Error(), "init") {
		t.Errorf("error = %q, want it to mention the init step", err)
	}
	if !f.cleaned {
		t.Error("Cleanup() was not called after Init failed")
	}
	if f.ran {
		t.Error("Run() was called even though Init failed")
	}
}

func TestRun_CleanupErrorIsReported(t *testing.T) {
	isolateGlobalConfig(t)

	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ouija.json")
	writeProjectConfig(t, cfgPath, minimalConfig)

	f := &fakeTotem{name: "app-test-cleanup-error", cleanupErr: errors.New("cleanup boom")}
	registry.Register(f)

	_, err := Run(context.Background(), f.Name(), registry.Args{Task: "x"}, cfgPath, false, "", nil)
	if err == nil {
		t.Fatal("Run() error = nil after a Cleanup failure, want an error")
	}
	if !strings.Contains(err.Error(), "cleanup: cleanup boom") || !f.ran {
		t.Errorf("error = %q (ran=%v), want the run to happen and the cleanup error reported", err, f.ran)
	}
}

// Configs written before per-totem settings keep working: top-level passes and
// dimensions move to totems.task (and passes to totems.doc, which shared it),
// unless the file already has those sections.
func TestLegacyTotemKeysMigrate(t *testing.T) {
	isolateGlobalConfig(t)
	dir := t.TempDir()

	old := filepath.Join(dir, "old.json")
	writeProjectConfig(t, old, `{"provider":"ollama","model":"x","passes":7,"dimensions":["security"]}`)
	cfg, _, err := DiscoverConfig(old)
	if err != nil {
		t.Fatal(err)
	}
	var task struct {
		Passes     int      `json:"passes"`
		Dimensions []string `json:"dimensions"`
	}
	if err := cfg.TotemConfig("task", &task); err != nil || task.Passes != 7 || len(task.Dimensions) != 1 {
		t.Fatalf("migrated totems.task = %+v, %v", task, err)
	}
	var doc struct{ Passes int }
	if err := cfg.TotemConfig("doc", &doc); err != nil || doc.Passes != 7 {
		t.Fatalf("migrated totems.doc = %+v, %v", doc, err)
	}

	both := filepath.Join(dir, "both.json")
	writeProjectConfig(t, both, `{"provider":"ollama","model":"x","passes":7,"totems":{"task":{"passes":2}}}`)
	cfg, _, _ = DiscoverConfig(both)
	task.Passes = 0
	if err := cfg.TotemConfig("task", &task); err != nil || task.Passes != 2 {
		t.Fatalf("an explicit totems.task must win over legacy keys: %+v, %v", task, err)
	}
}

// With no layer choosing a model, the error says how to choose one; ouija
// has no default model.
func TestDiscoverConfigWithoutAnyModel(t *testing.T) {
	isolateGlobalConfig(t)
	t.Chdir(t.TempDir())
	_, err := SetupEngine(context.Background(), "", false)
	if err == nil || !strings.Contains(err.Error(), "ouija setup") {
		t.Fatalf("SetupEngine with no model chosen = %v; want an error pointing to `ouija setup`", err)
	}
}

// A project config that sets only rules keeps the global provider and model
// (it used to override the provider with the built-in default).
func TestProjectConfigWithoutModelKeepsGlobalChoice(t *testing.T) {
	isolateGlobalConfig(t)
	if err := ouijaconfig.Save(&ouijaconfig.Config{Defaults: &ouijaconfig.Defaults{Provider: "ollama", Model: "llama3"}}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	writeProjectConfig(t, filepath.Join(dir, ".ouija", "config.json"), `{"rules":["project rule"]}`)
	if err := Trust(filepath.Join(dir, ".ouija", "config.json")); err != nil { // rules is a sensitive key
		t.Fatal(err)
	}
	t.Chdir(dir)
	cfg, path, err := DiscoverConfig("")
	if err != nil || path == "" {
		t.Fatalf("DiscoverConfig = %q, %v", path, err)
	}
	if cfg.Provider != "ollama" || cfg.Model != "llama3" || len(cfg.Rules) != 1 {
		t.Fatalf("got provider=%q model=%q rules=%v; want the global choice and the project rules", cfg.Provider, cfg.Model, cfg.Rules)
	}
}

// keyProvider records the key it was built with.
type keyProvider struct {
	engine.Provider
	name, key string
}

func (p keyProvider) Name() string { return p.name }

// The project's key goes only to the provider it was resolved for; `/provider`
// to any other provider uses that provider's global key.
func TestProviderFactoryScopesProjectKey(t *testing.T) {
	isolateGlobalConfig(t)
	for _, n := range []string{"test-keyed-a", "test-keyed-b"} {
		providers.Register(n, func(key string) engine.Provider { return keyProvider{name: n, key: key} })
	}
	if err := ouijaconfig.Save(&ouijaconfig.Config{APIKeys: map[string]string{
		"test-keyed-a": "global-a", "test-keyed-b": "global-b",
	}}); err != nil {
		t.Fatal(err)
	}
	out := &Outcome{Config: &engine.Config{Provider: "test-keyed-a", APIKey: "project-a"}}
	build := out.ProviderFactory()

	for name, want := range map[string]string{"test-keyed-a": "project-a", "test-keyed-b": "global-b"} {
		p, err := build(name)
		if err != nil {
			t.Fatal(err)
		}
		if got := p.(keyProvider).key; got != want {
			t.Errorf("%s built with key %q, want %q", name, got, want)
		}
	}
}

// The context window comes from the project config when set, else the model
// catalog, else Ollama's default; a model the catalog does not know stays 0.
func TestSetupEngineContextWindow(t *testing.T) {
	isolateGlobalConfig(t)
	for body, want := range map[string]int{
		`{"provider":"ollama","model":"llama3"}`:                           4096,
		`{"provider":"ollama","model":"llama3","contextWindow":32768}`:     32768,
		`{"provider":"openrouter","model":"x/unknown","apiKey":"sk-test"}`: 0,
	} {
		dir := t.TempDir()
		path := filepath.Join(dir, ".ouija", "config.json")
		writeProjectConfig(t, path, body)
		out, err := SetupEngine(context.Background(), path, false)
		if err != nil {
			t.Fatalf("%s: %v", body, err)
		}
		if got := out.Config.ContextWindow; got != want {
			t.Errorf("%s: window %d, want %d", body, got, want)
		}
		out.Engine.Close()
	}
}

// recordingProgress records the order of its calls against the totem's run.
type recordingProgress struct{ calls []string }

func (p *recordingProgress) Start(*engine.Engine) { p.calls = append(p.calls, "start") }
func (p *recordingProgress) Stop()                { p.calls = append(p.calls, "stop") }

// The progress watcher brackets the totem's run.
func TestRun_ProgressBracketsTheRun(t *testing.T) {
	isolateGlobalConfig(t)
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "ouija.json")
	writeProjectConfig(t, cfgPath, minimalConfig)
	f := &fakeTotem{name: "app-test-progress"}
	registry.Register(f)

	p := &recordingProgress{}
	if _, err := Run(context.Background(), f.Name(), registry.Args{Task: "x"}, cfgPath, false, "", p); err != nil {
		t.Fatal(err)
	}
	if strings.Join(p.calls, ",") != "start,stop" || !f.ran {
		t.Fatalf("calls = %v, ran = %v", p.calls, f.ran)
	}
}

// No sandbox in the global config leaves it to the engine's default (on);
// "enabled": false there turns it off for every project that does not turn
// it on itself.
func TestGlobalSandboxOffTurnsTheDefaultOff(t *testing.T) {
	isolateGlobalConfig(t)
	t.Chdir(t.TempDir())
	cfg, _, err := DiscoverConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools != nil && cfg.Tools.Bash != nil && cfg.Tools.Bash.Sandbox != nil {
		t.Fatalf("no global sandbox: got %+v, want none (the engine default applies)", cfg.Tools.Bash.Sandbox)
	}
	if err := ouijaconfig.Save(&ouijaconfig.Config{Sandbox: &engine.SandboxConfig{Enabled: false}}); err != nil {
		t.Fatal(err)
	}
	cfg, _, err = DiscoverConfig("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Tools == nil || cfg.Tools.Bash == nil || cfg.Tools.Bash.Sandbox == nil || cfg.Tools.Bash.Sandbox.Enabled {
		t.Fatalf("global enabled:false: got %+v, want an explicit off", cfg.Tools)
	}
}

// Discovery does not climb above the home directory: a config there (in
// /home, or /) belongs to someone else and would apply to every project of
// the user's.
func TestDiscoveryStopsAtHome(t *testing.T) {
	isolateGlobalConfig(t)
	top := t.TempDir()
	home := filepath.Join(top, "home")
	proj := filepath.Join(home, "proj")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	writeProjectConfig(t, filepath.Join(top, ".ouija.json"), `{"model":"planted"}`)
	t.Chdir(proj)
	if _, got, _, err := findConfigFile(""); err != nil || got != "" {
		t.Fatalf("findConfigFile = %q, %v; want nothing found above home", got, err)
	}
	// The home directory itself is still searched.
	writeProjectConfig(t, filepath.Join(home, ".ouija.json"), `{"model":"mine"}`)
	if _, got, _, err := findConfigFile(""); err != nil || got != filepath.Join(home, ".ouija.json") {
		t.Fatalf("findConfigFile = %q, %v; want the home config", got, err)
	}
}
