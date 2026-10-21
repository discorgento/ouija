package commands

import (
	"bytes"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/internal/models"
	"github.com/discorgento/ouija/internal/ouijaconfig"
)

func TestBuildGlobalConfig(t *testing.T) {
	cfg := buildGlobalConfig(nil, "anthropic", "sk-ant-x", "claude-sonnet-4")

	if cfg.Defaults == nil || cfg.Defaults.Provider != "anthropic" || cfg.Defaults.Model != "claude-sonnet-4" {
		t.Errorf("Defaults = %+v, want the picked provider/model", cfg.Defaults)
	}
	if got := cfg.APIKeys["anthropic"]; got != "sk-ant-x" {
		t.Errorf("APIKeys[anthropic] = %q, want the typed key", got)
	}
}

func TestBuildGlobalConfig_NoKeys(t *testing.T) {
	cfg := buildGlobalConfig(nil, "ollama", "", "llama3")

	if cfg.APIKeys == nil {
		t.Error("APIKeys is nil, want an empty map (it is serialized to JSON)")
	}
	if len(cfg.APIKeys) != 0 {
		t.Errorf("APIKeys = %v, want empty", cfg.APIKeys)
	}
}

// Re-running setup keeps what the wizard does not show: the trusted projects
// and the other providers' keys. A blank key keeps the saved one.
func TestBuildGlobalConfig_KeepsWhatTheFormDoesNotShow(t *testing.T) {
	existing := &ouijaconfig.Config{
		APIKeys:         map[string]string{"anthropic": "sk-old", "openai": "sk-openai"},
		TrustedProjects: map[string]string{"/proj/.ouija/config.json": "abc123"},
	}
	cfg := buildGlobalConfig(existing, "anthropic", "sk-new", "m")

	if cfg.TrustedProjects["/proj/.ouija/config.json"] != "abc123" {
		t.Error("setup erased the trusted projects")
	}
	if cfg.APIKeys["openai"] != "sk-openai" {
		t.Error("setup erased the key of another provider")
	}
	if cfg.APIKeys["anthropic"] != "sk-new" {
		t.Errorf("anthropic key = %q, want the new one", cfg.APIKeys["anthropic"])
	}
	if existing.APIKeys["anthropic"] != "sk-old" {
		t.Error("buildGlobalConfig modified the existing config in place")
	}
	if kept := buildGlobalConfig(existing, "anthropic", "", "m"); kept.APIKeys["anthropic"] != "sk-old" {
		t.Errorf("a blank key dropped the saved one: %q", kept.APIKeys["anthropic"])
	}
}

// The model picker puts the suggestions first, starred, then the rest of the
// provider's list once; a suggestion the provider no longer lists is left out.
func TestModelOptions(t *testing.T) {
	suggested := []models.Model{{ID: "good", Label: "Good"}, {ID: "gone"}}
	list := []models.Model{{ID: "a"}, {ID: "good"}, {ID: "b"}}
	var got []string
	for _, o := range modelOptions(suggested, list) {
		got = append(got, o.Value+"="+o.Key)
	}
	want := []string{"good=★ Good  good ", "a=  a    ", "b=  b    "}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("options = %q, want %q", got, want)
	}
	// The free ones come right after the suggestions, before the paid ones.
	var order []string
	for _, o := range modelOptions(nil, []models.Model{{ID: "paid"}, {ID: "x:free"}, {ID: "y", Label: "Y (free)"}}) {
		order = append(order, o.Value)
	}
	if strings.Join(order, ",") != "x:free,y,paid" {
		t.Errorf("order = %v, want the free models first", order)
	}
	// Offline (no list): the suggestions are all there is.
	if n := len(modelOptions(suggested, nil)); n != 2 {
		t.Errorf("offline options = %d, want both suggestions", n)
	}
}

// The intro card names the three steps and where the config goes; the key
// field says where to get a key and that a blank one keeps the saved key.
func TestSetupTexts(t *testing.T) {
	intro := ansi.Strip(setupIntro("~/.config/ouija/config.json"))
	for _, want := range []string{"three quick steps", "provider", "API key", "model", "~/.config/ouija/config.json"} {
		if !strings.Contains(intro, want) {
			t.Errorf("intro lacks %q:\n%s", want, intro)
		}
	}
	help := keyHelp("anthropic", "sk-ant-1234567890")
	for _, want := range []string{"console.anthropic.com", "this computer only", "••••7890"} {
		if !strings.Contains(help, want) {
			t.Errorf("key help lacks %q:\n%s", want, help)
		}
	}
	if strings.Contains(keyHelp("anthropic", ""), "Leave it blank") {
		t.Error("the key help offers to keep a key that is not saved")
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
