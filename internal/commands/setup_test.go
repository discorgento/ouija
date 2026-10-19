package commands

import "testing"

func TestBuildGlobalConfig(t *testing.T) {
	anthropic := "sk-ant-x"
	blank := ""

	cfg := buildGlobalConfig(map[string]*string{
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
	cfg := buildGlobalConfig(map[string]*string{}, "ollama", "llama3")

	if cfg.APIKeys == nil {
		t.Error("APIKeys is nil, want an empty map (it is serialized to JSON)")
	}
	if len(cfg.APIKeys) != 0 {
		t.Errorf("APIKeys = %v, want empty", cfg.APIKeys)
	}
}
