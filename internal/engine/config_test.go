package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.Provider != "anthropic" {
		t.Errorf("DefaultConfig().Provider = %q, want anthropic", cfg.Provider)
	}
	if cfg.Model != "claude-opus-4-8" {
		t.Errorf("DefaultConfig().Model = %q, want claude-opus-4-8", cfg.Model)
	}
	if cfg.Passes != 3 {
		t.Errorf("DefaultConfig().Passes = %d, want 3", cfg.Passes)
	}
	if cfg.MaxNudges != 3 {
		t.Errorf("DefaultConfig().MaxNudges = %d, want 3", cfg.MaxNudges)
	}
	if len(cfg.Dimensions) != 4 {
		t.Errorf("DefaultConfig() has %d dimensions, want 4", len(cfg.Dimensions))
	}
	if cfg.ProjectRoot != "." {
		t.Errorf("DefaultConfig().ProjectRoot = %q, want .", cfg.ProjectRoot)
	}
}

func TestConfigValidate(t *testing.T) {
	cfg := DefaultConfig()
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
	if !strings.Contains(err.Error(), "passes must be > 0") {
		t.Errorf("passes error: %v", err)
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
	original.Dimensions = []string{"correctness", "performance"}

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
	if len(loaded.Dimensions) != 2 {
		t.Errorf("got %d dimensions, want 2", len(loaded.Dimensions))
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
