package engine

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Budget holds the hard ceilings for an Engine run.
type Budget struct {
	MaxTokens int      `json:"maxTokens"`
	MaxSteps  int      `json:"maxSteps"`
	MaxUSD    *float64 `json:"maxUSD"`
}

// PermissionRule is the JSON-serializable permission configuration.
type PermissionRule struct {
	Default string            `json:"default"`         // "allow", "ask", "deny"
	Tools   map[string]string `json:"tools,omitempty"` // tool name → "allow"|"ask"|"deny"
}

// ToEngine converts the JSON config to an engine PermissionConfig.
func (r *PermissionRule) ToEngine() *PermissionConfig {
	if r == nil {
		return nil
	}
	cfg := NewPermissionConfig(ParseLevel(r.Default))
	for tool, lvl := range r.Tools {
		cfg.Tools[tool] = ParseLevel(lvl)
	}
	return cfg
}

// Config is the per-project configuration (loaded from --config JSON or
// .ouija/config.json). Nothing here is project-specific in code:
// rules/dimensions come from the file.
type Config struct {
	Provider    string   `json:"provider"`
	Model       string   `json:"model"`
	APIKey      string   `json:"apiKey,omitempty"` // optional project-level override
	MaxNudges   int      `json:"maxNudges,omitempty"`
	Passes      int      `json:"passes"`
	Budget      Budget   `json:"budget"`
	Dimensions  []string `json:"dimensions"`
	Rules       []string `json:"rules"`
	ProjectRoot string   `json:"projectRoot"`

	// SystemPrompt is sent as the system message in chat. When empty, a
	// default prompt is used that tells the model it has file tools.
	SystemPrompt string          `json:"systemPrompt,omitempty"`
	Permission   *PermissionRule `json:"permission,omitempty"` // optional per-tool permission levels
	Tools        *ToolsConfig    `json:"tools,omitempty"`      // optional per-tool configuration

	// ModelSuggestions maps provider names to recommended model IDs (used by
	// the TUI / slash commands for /model autocomplete; stored here to survive
	// a round-trip through LoadConfig/Save).
	ModelSuggestions map[string][]string `json:"modelSuggestions,omitempty"`

	// Optional pricing so the budget can enforce maxUSD. Illustrative defaults;
	// adjust to the real per-1M-token price of your model/provider.
	CostPer1MInput  float64 `json:"costPer1MInput"`
	CostPer1MOutput float64 `json:"costPer1MOutput"`

	// StreamTimeoutSeconds is the maximum time to wait for the next token from
	// the provider before giving up. Zero or negative means no timeout.
	// Default: 120s.
	StreamTimeout int `json:"streamTimeout,omitempty"`
}

// ToolsConfig holds per-tool configuration that the user can set in config.json.
type ToolsConfig struct {
	Bash *BashConfig `json:"bash,omitempty"`
}

// BashConfig configures the bash tool.
type BashConfig struct {
	// Allowlist restricts execution to specific base commands (e.g. ["git", "npm"]).
	// When empty or nil, all commands are allowed (default).
	// Commands approved interactively in the TUI are added at runtime.
	Allowlist []string `json:"allowlist,omitempty"`
}

// MaxUSD returns the dollar ceiling (0 = unlimited).
func (c *Config) MaxUSD() float64 {
	if c.Budget.MaxUSD == nil {
		return 0
	}
	return *c.Budget.MaxUSD
}

// StreamTimeoutSec returns the streaming timeout in seconds.
// Defaults to 120 when not configured or zero.
func (c *Config) StreamTimeoutSec() int {
	if c.StreamTimeout <= 0 {
		return 120
	}
	return c.StreamTimeout
}

// Validate checks the config is usable and returns all problems at once.
func (c *Config) Validate() error {
	var errs []error

	if c.Provider == "" {
		errs = append(errs, errors.New("provider is required"))
	}
	if c.Model == "" {
		errs = append(errs, errors.New("model is required"))
	}
	if c.Passes <= 0 {
		errs = append(errs, errors.New("passes must be > 0"))
	}
	if c.MaxNudges < 0 {
		errs = append(errs, errors.New("maxNudges must be >= 0"))
	}
	if c.Budget.MaxTokens <= 0 {
		errs = append(errs, errors.New("budget.maxTokens must be > 0"))
	}
	if c.Budget.MaxSteps <= 0 {
		errs = append(errs, errors.New("budget.maxSteps must be > 0"))
	}

	return errors.Join(errs...)
}

// DefaultConfig is used when no --config is given.
func DefaultConfig() *Config {
	return &Config{
		Provider:  "anthropic",
		Model:     "claude-opus-4-8",
		MaxNudges: 3,
		Passes:    3,
		// MaxSteps is a runaway backstop counting EVERY chat call across all agents
		// in a run (scout + N×dims passes + reconcile, each with tool round-trips),
		// so it must be generous — token/USD ceilings are the real cost bound.
		Budget:      Budget{MaxTokens: 2_000_000, MaxSteps: 500},
		Dimensions:  []string{"correctness", "best-practices", "security", "performance"},
		Rules:       []string{"Prefer clarity and correctness", "Cite file/line when asserting something"},
		ProjectRoot: ".",
		SystemPrompt: "You are an AI coding assistant with access to the project files. " +
			"You have file tools: read_file, grep, glob, list_dir — use them to " +
			"explore the codebase before answering questions about the code. " +
			"When asked to analyze, review, or explain the project, read the " +
			"relevant files with your tools instead of asking the user for code. " +
			"The project root is the current working directory.",
	}
}

// LoadConfig reads and validates a config file.
func LoadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c := DefaultConfig()
	if err := json.Unmarshal(b, c); err != nil {
		return nil, err
	}
	if c.Passes <= 0 {
		c.Passes = 3
	}
	if c.ProjectRoot == "" {
		c.ProjectRoot = "."
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return c, nil
}

// Save writes the config as JSON to path, creating parent directories as
// needed, with restricted permissions (0600) since the file may contain API
// keys in the apiKey field.
func (c *Config) Save(path string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("mkdir %s: %w", dir, err)
	}
	b, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal: %w", err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
