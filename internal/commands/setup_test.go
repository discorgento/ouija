package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/internal/ouijaconfig"
)

func TestBuildGlobalConfig(t *testing.T) {
	anthropic := "sk-ant-x"
	blank := ""

	cfg := buildGlobalConfig(nil, map[string]*string{
		"anthropic": &anthropic,
		"openai":    &blank,
	}, "anthropic", "claude-sonnet-4")

	if cfg.Defaults == nil || cfg.Defaults.Provider != "anthropic" || cfg.Defaults.Model != "claude-sonnet-4" {
		t.Errorf("Defaults = %+v, want the picked provider/model", cfg.Defaults)
	}
	if got := cfg.APIKeys["anthropic"]; got != "sk-ant-x" {
		t.Errorf("APIKeys[anthropic] = %q, want the typed key", got)
	}
	if _, ok := cfg.APIKeys["openai"]; ok {
		t.Error("an empty key was stored; blank fields must be skipped")
	}
}

func TestBuildGlobalConfig_NoKeys(t *testing.T) {
	cfg := buildGlobalConfig(nil, map[string]*string{}, "ollama", "llama3")

	if cfg.APIKeys == nil {
		t.Error("APIKeys is nil, want an empty map (it is serialized to JSON)")
	}
	if len(cfg.APIKeys) != 0 {
		t.Errorf("APIKeys = %v, want empty", cfg.APIKeys)
	}
}

// Re-running setup keeps what the wizard does not show: the trusted projects
// and keys of providers outside the form.
func TestBuildGlobalConfig_KeepsWhatTheFormDoesNotShow(t *testing.T) {
	existing := &ouijaconfig.Config{
		APIKeys:         map[string]string{"anthropic": "sk-old", "custom": "sk-custom", "openai": "sk-openai"},
		TrustedProjects: map[string]string{"/proj/.ouija/config.json": "abc123"},
	}
	newKey, blank := "sk-new", ""
	cfg := buildGlobalConfig(existing, map[string]*string{"anthropic": &newKey, "openai": &blank}, "anthropic", "m")

	if cfg.TrustedProjects["/proj/.ouija/config.json"] != "abc123" {
		t.Error("setup erased the trusted projects")
	}
	if cfg.APIKeys["custom"] != "sk-custom" {
		t.Error("setup erased the key of a provider not in the form")
	}
	if cfg.APIKeys["anthropic"] != "sk-new" {
		t.Errorf("anthropic key = %q, want the new one", cfg.APIKeys["anthropic"])
	}
	if _, ok := cfg.APIKeys["openai"]; ok {
		t.Error("a key blanked in the form must be removed")
	}
	if existing.APIKeys["anthropic"] != "sk-old" {
		t.Error("buildGlobalConfig modified the existing config in place")
	}
}

// The wizard's closing card lists its rows, warnings and the next step.
func TestWriteDone(t *testing.T) {
	var buf bytes.Buffer
	writeDone(&buf, "Config saved", [][2]string{{"provider", "anthropic"}, {"model", "claude-x"}}, []string{"no API keys"}, "`ouija chat`")
	out := ansi.Strip(buf.String())
	for _, want := range []string{"✓ Config saved", "provider  anthropic", "model     claude-x", "⚠ no API keys", "next → `ouija chat`"} {
		if !strings.Contains(out, want) {
			t.Errorf("card lacks %q:\n%s", want, out)
		}
	}
}
