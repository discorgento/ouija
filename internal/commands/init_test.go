package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/ouijaconfig"
)

func TestSeedProjectConfig_ExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ouija", "config.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"provider":"openai","model":"gpt-4o","projectRoot":"."}`), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	global := &ouijaconfig.Config{Defaults: &ouijaconfig.Defaults{Provider: "anthropic", Model: "claude"}}
	cfg, existing, err := seedProjectConfig(path, global)
	if err != nil {
		t.Fatal(err)
	}

	if !existing {
		t.Error("existing = false, want true for a readable project config")
	}
	if cfg.Provider != "openai" || cfg.Model != "gpt-4o" {
		t.Errorf("provider/model = %q/%q, want the values from the file", cfg.Provider, cfg.Model)
	}
}

func TestSeedProjectConfig_FallsBackToGlobal(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".ouija", "config.json") // does not exist

	global := &ouijaconfig.Config{Defaults: &ouijaconfig.Defaults{Provider: "openai", Model: "gpt-4o"}}
	cfg, existing, err := seedProjectConfig(path, global)
	if err != nil {
		t.Fatal(err)
	}

	if existing {
		t.Error("existing = true, want false when there is no project config")
	}
	if cfg.Provider != "openai" || cfg.Model != "gpt-4o" {
		t.Errorf("provider/model = %q/%q, want the global defaults", cfg.Provider, cfg.Model)
	}
	if cfg.MaxNudges != engine.DefaultConfig().MaxNudges {
		t.Errorf("maxNudges = %d, want the engine defaults preserved", cfg.MaxNudges)
	}
}

func TestInitAPIKey(t *testing.T) {
	tests := []struct {
		name      string
		chosen    string
		globalKey string
		want      string
	}{
		{"blank is not stored", "", "sk-global", ""},
		{"same as global is not duplicated", "sk-global", "sk-global", ""},
		{"different from global is kept", "sk-project", "sk-global", "sk-project"},
		{"kept when there is no global key", "sk-project", "", "sk-project"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := initAPIKey(tt.chosen, tt.globalKey); got != tt.want {
				t.Errorf("initAPIKey(%q, %q) = %q, want %q", tt.chosen, tt.globalKey, got, tt.want)
			}
		})
	}
}

// A project config that does not load is reported, never replaced by
// defaults: saving those would erase the user's rules, totems and permissions.
func TestSeedProjectConfig_BrokenFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"rules": ["keep me"],}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := seedProjectConfig(path, &ouijaconfig.Config{}); err == nil {
		t.Fatal("a broken project config was treated as absent")
	}
}

// A project's key goes only to its provider: after switching provider in the
// form, an untouched old key is not the new provider's (it used to be sent to
// that API and saved as its override).
func TestChosenKey(t *testing.T) {
	for _, c := range []struct{ field, origKey, prov, origProv, want string }{
		{"sk-ant", "sk-ant", "openai", "anthropic", ""},          // untouched, switched: not the new provider's
		{"sk-ant", "sk-ant", "anthropic", "anthropic", "sk-ant"}, // same provider: keep
		{"sk-new", "sk-ant", "openai", "anthropic", "sk-new"},    // typed for the new provider
		{"", "", "openai", "anthropic", ""},
	} {
		if got := chosenKey(c.field, c.origKey, c.prov, c.origProv); got != c.want {
			t.Errorf("chosenKey(%+v) = %q, want %q", c, got, c.want)
		}
	}
}
