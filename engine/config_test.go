package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	// No built-in provider or model: the developer always chooses.
	if cfg.Provider != "" || cfg.Model != "" {
		t.Errorf("DefaultConfig() picks %q/%q; it must name no provider or model", cfg.Provider, cfg.Model)
	}
	if cfg.MaxNudges != 3 {
		t.Errorf("DefaultConfig().MaxNudges = %d, want 3", cfg.MaxNudges)
	}
	if len(cfg.Totems) != 0 {
		t.Errorf("DefaultConfig() has totem settings %v; totems own their defaults", cfg.Totems)
	}
	if cfg.ProjectRoot != "." {
		t.Errorf("DefaultConfig().ProjectRoot = %q, want .", cfg.ProjectRoot)
	}
}

func TestConfigValidate(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider, cfg.Model = "ollama", "llama3"
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid config rejected: %v", err)
	}

	cfg2 := &Config{}
	err := cfg2.Validate()
	if err == nil {
		t.Fatal("empty config should fail validation")
	}
	if !strings.Contains(err.Error(), "provider is required") {
		t.Errorf("missing provider error: %v", err)
	}
	if !strings.Contains(err.Error(), "model is required") {
		t.Errorf("missing model error: %v", err)
	}
}

func TestConfigMaxUSD(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.MaxUSD() != 0 {
		t.Fatalf("default MaxUSD = %f, want 0", cfg.MaxUSD())
	}

	dollar := 5.0
	cfg.Budget.MaxUSD = &dollar
	if cfg.MaxUSD() != 5.0 {
		t.Fatalf("MaxUSD = %f, want 5.0", cfg.MaxUSD())
	}
}

func TestConfigSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".ouija", "config.json")

	original := DefaultConfig()
	original.Provider = "openai"
	original.Model = "gpt-4o"
	original.Totems = map[string]json.RawMessage{"task": json.RawMessage(`{"passes":2}`)}

	if err := original.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}

	if loaded.Provider != "openai" {
		t.Errorf("Provider = %q, want openai", loaded.Provider)
	}
	if loaded.Model != "gpt-4o" {
		t.Errorf("Model = %q, want gpt-4o", loaded.Model)
	}
	var task struct {
		Passes int `json:"passes"`
	}
	if err := loaded.TotemConfig("task", &task); err != nil || task.Passes != 2 {
		t.Errorf("totems.task round trip: passes=%d err=%v, want 2", task.Passes, err)
	}
	// ProjectRoot should default to "." since it was empty in saved JSON
	if loaded.ProjectRoot != "." {
		t.Errorf("ProjectRoot = %q, want .", loaded.ProjectRoot)
	}
}

func TestLoadConfigNonExistent(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.json")
	if err == nil {
		t.Fatal("LoadConfig should fail on non-existent file")
	}
}

func TestSaveCreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	deep := filepath.Join(dir, "a", "b", "c", "config.json")

	cfg := DefaultConfig()
	if err := cfg.Save(deep); err != nil {
		t.Fatalf("Save to deep path: %v", err)
	}

	if _, err := os.Stat(deep); err != nil {
		t.Fatalf("file not created: %v", err)
	}
}

// TotemConfig decodes a totem's entry and leaves v untouched when there is none.
func TestTotemConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Totems = map[string]json.RawMessage{"task": json.RawMessage(`{"passes":5}`), "bad": json.RawMessage(`[1]`)}
	v := struct {
		Passes int    `json:"passes"`
		Other  string `json:"other"`
	}{Passes: 3, Other: "default"}
	if err := cfg.TotemConfig("task", &v); err != nil || v.Passes != 5 || v.Other != "default" {
		t.Fatalf("TotemConfig(task) = %+v, %v", v, err)
	}
	w := struct{ Passes int }{Passes: 3}
	if err := cfg.TotemConfig("absent", &w); err != nil || w.Passes != 3 {
		t.Fatalf("TotemConfig(absent) = %+v, %v; want defaults untouched", w, err)
	}
	if err := cfg.TotemConfig("bad", &w); err == nil || !strings.Contains(err.Error(), "totems.bad") {
		t.Fatalf("TotemConfig(bad) error = %v; want it to name the section", err)
	}
}

// A project file may leave the provider and model to the global config:
// LoadConfig accepts it, and only the merged config must name them.
func TestLoadConfigAcceptsFileWithoutModel(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(path, []byte(`{"rules":["one"]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig rejected a file that leaves the model to the global layer: %v", err)
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("Validate must still require a provider and model on the merged config")
	}
	bad := filepath.Join(t.TempDir(), "bad.json")
	if err := os.WriteFile(bad, []byte(`{"budget":{"maxTokens":-1,"maxSteps":1}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadConfig(bad); err == nil {
		t.Fatal("LoadConfig must still reject what the file itself gets wrong")
	}
}
