// Package ouijaconfig manages the user's global configuration stored at
// ~/.config/ouija/config.json (XDG Base Directory). This holds API keys for
// all providers and the default provider/model selection.
package ouijaconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	dirPerm  os.FileMode = 0700
	filePerm os.FileMode = 0600
)

// ─── Global config (~/.config/ouija/config.json) ──────────────────────────

// Config is the on-disk structure of ~/.config/ouija/config.json.
// API keys for every provider are stored here; defaults pick the active one.
// Backward compat: if old-style provider/model exist at top level (no defaults
// block), they are treated as the defaults.
type Config struct {
	APIKeys  map[string]string `json:"apiKeys,omitempty"`
	Defaults *Defaults         `json:"defaults,omitempty"`

	// Legacy fields — kept for backward compat with old configs.
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
}

// Defaults selects a provider and model as the active choice.
type Defaults struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
}

// DefaultProvider returns the effective default provider.
func (c *Config) DefaultProvider() string {
	if c.Defaults != nil && c.Defaults.Provider != "" {
		return c.Defaults.Provider
	}
	return c.Provider
}

// DefaultModel returns the effective default model.
func (c *Config) DefaultModel() string {
	if c.Defaults != nil && c.Defaults.Model != "" {
		return c.Defaults.Model
	}
	return c.Model
}

// HasKey reports whether a provider has an API key stored.
func (c *Config) HasKey(provider string) bool {
	if c == nil || c.APIKeys == nil {
		return false
	}
	return c.APIKeys[provider] != ""
}

// Path returns the XDG config path ~/.config/ouija/config.json (or
// $XDG_CONFIG_HOME/ouija/config.json).
func Path() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		dir = ".config"
	}
	return filepath.Join(dir, "ouija", "config.json")
}

// Load reads the config file. Returns nil, nil when the file does not exist
// (first run) — callers should prompt the user to run `ouija setup`.
func Load() (*Config, error) {
	p := Path()
	fi, err := os.Stat(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil // first run
		}
		return nil, fmt.Errorf("stat %s: %w", p, err)
	}
	if fi.Mode()&0o077 != 0 {
		slog.Warn("loose config permissions, fixing", "path", p, "mode", fi.Mode()&0o777)
		if err := os.Chmod(p, filePerm); err != nil {
			slog.Warn("chmod failed", "path", p, "err", err)
		}
	}

	b, err := os.ReadFile(p)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	cfg := &Config{}
	if err := json.Unmarshal(b, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	return cfg, nil
}

// Save writes the config to the XDG path with permission 0600.
func Save(cfg *Config) error {
	p := Path()
	if err := os.MkdirAll(filepath.Dir(p), dirPerm); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(p, b, filePerm); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	return nil
}
