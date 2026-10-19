package ouijaconfig

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// tempConfigDir points every OS config-dir lookup at a fresh temp directory so
// these tests never read or write the developer's real config (which holds API
// keys):
//   - Linux:   XDG_CONFIG_HOME
//   - macOS:   HOME (os.UserConfigDir uses ~/Library/Application Support)
//   - Windows: AppData
func tempConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)
	return dir
}

func TestPath_LivesUnderConfigDir(t *testing.T) {
	dir := tempConfigDir(t)

	got := Path()
	if !strings.HasSuffix(got, filepath.Join("ouija", "config.json")) {
		t.Fatalf("Path() = %q, want it to end with ouija/config.json", got)
	}
	if !strings.HasPrefix(got, dir) {
		t.Fatalf("Path() = %q, want it inside the temp config dir %q", got, dir)
	}
}

func TestLoad_FirstRunReturnsNilNil(t *testing.T) {
	tempConfigDir(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v, want nil on first run", err)
	}
	if cfg != nil {
		t.Fatalf("Load() = %+v, want nil on first run", cfg)
	}
}

func TestSaveLoad_RoundTrip(t *testing.T) {
	tempConfigDir(t)

	in := &Config{
		APIKeys: map[string]string{
			"anthropic": "sk-ant-secret",
			"openai":    "sk-openai-secret",
		},
		Defaults: &Defaults{Provider: "openai", Model: "gpt-4o"},
	}
	if err := Save(in); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	out, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if out == nil {
		t.Fatal("Load() = nil after Save")
	}
	if got := out.DefaultProvider(); got != "openai" {
		t.Errorf("DefaultProvider() = %q, want openai", got)
	}
	if got := out.DefaultModel(); got != "gpt-4o" {
		t.Errorf("DefaultModel() = %q, want gpt-4o", got)
	}
	if !out.HasKey("anthropic") {
		t.Error("HasKey(anthropic) = false after a round-trip")
	}
	if got := out.APIKeys["anthropic"]; got != "sk-ant-secret" {
		t.Errorf("APIKeys[anthropic] = %q, want the saved key", got)
	}
}

func TestSave_RestrictsPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not enforced on Windows")
	}
	tempConfigDir(t)

	if err := Save(&Config{Defaults: &Defaults{Provider: "x", Model: "y"}}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Expected modes are literals, not the filePerm/dirPerm constants: comparing
	// against the constant would make this test vacuous, since loosening the
	// constant would loosen the expectation too.
	fi, err := os.Stat(Path())
	if err != nil {
		t.Fatalf("stat config: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("config file perm = %o, want 600 (the file can hold API keys)", perm)
	}

	di, err := os.Stat(filepath.Dir(Path()))
	if err != nil {
		t.Fatalf("stat config dir: %v", err)
	}
	if perm := di.Mode().Perm(); perm != 0o700 {
		t.Errorf("config dir perm = %o, want 700", perm)
	}
}

func TestLoad_TightensLoosePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not enforced on Windows")
	}
	tempConfigDir(t)

	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), dirPerm); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte(`{"provider":"openai","model":"gpt-4o"}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	// os.WriteFile is subject to umask; force the loose mode under test.
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatalf("chmod: %v", err)
	}

	if _, err := Load(); err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("after Load, perm = %o, want 600 (Load must tighten loose configs)", perm)
	}
}

func TestLoad_InvalidJSON(t *testing.T) {
	tempConfigDir(t)

	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), dirPerm); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(p, []byte("{ not json"), filePerm); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := Load(); err == nil {
		t.Fatal("Load() error = nil for invalid JSON, want a parse error")
	}
}

func TestDefaultSelection(t *testing.T) {
	tests := []struct {
		name         string
		cfg          Config
		wantProvider string
		wantModel    string
	}{
		{
			name:         "defaults block wins over legacy top-level fields",
			cfg:          Config{Defaults: &Defaults{Provider: "openai", Model: "gpt-4o"}, Provider: "anthropic", Model: "claude"},
			wantProvider: "openai",
			wantModel:    "gpt-4o",
		},
		{
			name:         "legacy fields used when there is no defaults block",
			cfg:          Config{Provider: "anthropic", Model: "claude"},
			wantProvider: "anthropic",
			wantModel:    "claude",
		},
		{
			name:         "empty defaults block falls back to legacy fields",
			cfg:          Config{Defaults: &Defaults{}, Provider: "ollama", Model: "llama3"},
			wantProvider: "ollama",
			wantModel:    "llama3",
		},
		{
			name: "nothing configured",
			cfg:  Config{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.cfg.DefaultProvider(); got != tt.wantProvider {
				t.Errorf("DefaultProvider() = %q, want %q", got, tt.wantProvider)
			}
			if got := tt.cfg.DefaultModel(); got != tt.wantModel {
				t.Errorf("DefaultModel() = %q, want %q", got, tt.wantModel)
			}
		})
	}
}

func TestHasKey(t *testing.T) {
	var nilCfg *Config
	if nilCfg.HasKey("anthropic") {
		t.Error("nil *Config must report no key")
	}

	noMap := &Config{}
	if noMap.HasKey("anthropic") {
		t.Error("config with nil APIKeys must report no key")
	}

	cfg := &Config{APIKeys: map[string]string{"anthropic": "sk-ant-x", "openai": ""}}
	if !cfg.HasKey("anthropic") {
		t.Error("HasKey(anthropic) = false, want true")
	}
	if cfg.HasKey("openai") {
		t.Error("HasKey(openai) = true for an empty value, want false")
	}
	if cfg.HasKey("missing") {
		t.Error("HasKey(missing) = true, want false")
	}
}
