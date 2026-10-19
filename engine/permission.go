package engine

import (
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// ─── Permission levels ──────────────────────────────────────────────

// Level classifies permission for a tool.
type Level int

const (
	Deny  Level = iota // always blocked
	Ask                // requires human approval
	Allow              // runs without approval
)

// String returns the string representation.
func (l Level) String() string {
	switch l {
	case Deny:
		return "deny"
	case Ask:
		return "ask"
	case Allow:
		return "allow"
	default:
		return "deny"
	}
}

// ParseLevel converts a string to a Level. Returns Ask on unknown.
func ParseLevel(s string) Level {
	switch strings.ToLower(s) {
	case "allow":
		return Allow
	case "ask":
		return Ask
	case "deny":
		return Deny
	default:
		return Ask
	}
}

// ─── PermissionConfig ───────────────────────────────────────────────

// PermissionConfig maps tool names to permission levels.
// The Default level is used for tools not explicitly mapped.
// Protected paths are always denied regardless of level.
//
// Default and Tools may be set directly while building the config. Once it is
// handed to an Engine, runs read it concurrently: change it only through
// SetDefault/SetTool and read it through Levels.
type PermissionConfig struct {
	Default Level
	Tools   map[string]Level // tool name → level

	mu sync.RWMutex
}

// SetDefault changes the level for tools without an override. Safe while runs
// read the config.
func (cfg *PermissionConfig) SetDefault(l Level) {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	cfg.Default = l
}

// SetTool overrides the level of one tool. Safe while runs read the config.
func (cfg *PermissionConfig) SetTool(tool string, l Level) {
	cfg.mu.Lock()
	defer cfg.mu.Unlock()
	if cfg.Tools == nil {
		cfg.Tools = make(map[string]Level)
	}
	cfg.Tools[tool] = l
}

// Levels returns the default level and a copy of the per-tool overrides.
func (cfg *PermissionConfig) Levels() (Level, map[string]Level) {
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	return cfg.Default, maps.Clone(cfg.Tools)
}

// NewPermissionConfig creates a config with the given default level.
func NewPermissionConfig(defaultLevel Level) *PermissionConfig {
	return &PermissionConfig{
		Default: defaultLevel,
		Tools:   make(map[string]Level),
	}
}

// NewDefaultPermissionConfig creates a config where read-only tools are
// allowed without approval and write/exec tools require Ask. This is the
// recommended default for interactive use — the user shouldn't have to
// approve every file read, only potentially destructive operations.
func NewDefaultPermissionConfig() *PermissionConfig {
	return &PermissionConfig{
		Default: Ask,
		Tools: map[string]Level{
			"read_file":   Allow,
			"list_dir":    Allow,
			"grep":        Allow,
			"glob":        Allow,
			"write_file":  Ask,
			"edit_file":   Ask,
			"delete_file": Ask,
			"bash":        Ask,
		},
	}
}

// Check returns the permission level for a tool operating on path.
// absPath is the absolute filesystem path (empty for non-file tools).
// Protected paths always return Deny.
func (cfg *PermissionConfig) Check(tool, absPath string) Level {
	if absPath != "" && isProtectedPath(absPath) {
		return Deny
	}
	cfg.mu.RLock()
	defer cfg.mu.RUnlock()
	if lvl, ok := cfg.Tools[tool]; ok {
		return lvl
	}
	return cfg.Default
}

// ─── Protected paths ────────────────────────────────────────────────

// foldCase: macOS and Windows filesystems ignore case by default, so `.ENV`
// opens `.env` and `.SSH/id_rsa` the key — the names are matched lowercased
// there. A variable so tests can exercise it on Linux.
var foldCase = runtime.GOOS == "darwin" || runtime.GOOS == "windows"

// isProtectedPath checks whether a resolved absolute path is protected.
// Protected paths can never be accessed even with Allow level.
// Checks both filename patterns and directory components anywhere in the path.
func isProtectedPath(absPath string) bool {
	if foldCase {
		absPath = strings.ToLower(absPath)
	}
	// ── Protected filenames (regardless of location) ────────────────
	base := filepath.Base(absPath)
	switch base {
	case ".gitconfig", ".netrc", ".npmrc", ".dockercfg", ".dockerconfigjson":
		return true
	// ouija's own project configs: they may hold an apiKey, and they grant
	// permissions — a model that could rewrite them would grant its own.
	case ".ouija.json", "ouija.json":
		return true
	}
	// `.env*` is a prefix, not an enumeration: `.env`, `.env.local`,
	// `.env.production`, `.env.secret`, `.env.test`, `.envrc`, … all hold
	// credentials. Matching only a fixed list left `.env.staging` readable.
	// False positives (`.env.example`, a file named `.environment`) are
	// accepted by design — the policy fails closed.
	if strings.HasPrefix(base, ".env") {
		return true
	}

	// ── Protected system directories ────────────────────────────────
	dir := filepath.Dir(absPath)
	switch {
	case dir == "/etc" || strings.HasPrefix(dir, "/etc/"):
		return true
	case dir == "/var/lib" || strings.HasPrefix(dir, "/var/lib/"):
		return true
	// macOS: /etc and /var are links into /private, so the physical path
	// checks see /private/etc/… (a project symlink to /etc/master.passwd).
	case dir == "/private/etc" || strings.HasPrefix(dir, "/private/etc/"):
		return true
	case dir == "/private/var/lib" || strings.HasPrefix(dir, "/private/var/lib/"):
		return true
	}

	// ── Protected directory names at ANY path level ─────────────────
	// These directories contain credentials regardless of their location:
	// .ssh (SSH keys), .gnupg (GPG keys), .aws (AWS credentials),
	// .docker (Docker auth/config), .kube (Kubernetes config),
	// .azure (Azure auth), .ouija (config with optional apiKey).
	// Note: not using filepath.Join to preserve OS independence — the
	// separator is always part of the absolute path string.
	sep := string(filepath.Separator)
	if containsComponent(absPath, sep, ".ssh", ".gnupg", ".aws", ".docker", ".kube", ".azure", ".ouija") {
		return true
	}
	if containsComponentPair(absPath, sep, ".config", "gcloud") {
		return true
	}
	if containsComponentPair(absPath, sep, ".config", "kube") {
		return true
	}
	// ouija's global config: every provider's API key and the trusted-project
	// list. Matched both as the literal `.config/ouija` and as the real
	// os.UserConfigDir()/ouija (XDG_CONFIG_HOME, macOS Application Support).
	if containsComponentPair(absPath, sep, ".config", "ouija") {
		return true
	}
	// …and the OS config dir's ouija and gcloud (on Windows gcloud keeps its
	// credentials in %AppData%\gcloud, not .config/gcloud).
	if dir, err := os.UserConfigDir(); err == nil {
		for _, name := range []string{"ouija", "gcloud"} {
			g := filepath.Join(dir, name)
			if foldCase {
				g = strings.ToLower(g)
			}
			if absPath == g || strings.HasPrefix(absPath, g+sep) {
				return true
			}
		}
	}

	return false
}

// containsComponent checks if path contains any of the given directory
// names as a path component (not substring match). E.g., ".ssh" matches
// "/home/user/.ssh/keys" and ".ssh/id_rsa" but not "/home/user/.sshkeys".
// Handles both absolute paths (starting with sep) and relative paths.
func containsComponent(path, sep string, names ...string) bool {
	for _, name := range names {
		// Match as start-of-path component: ".ssh/id_rsa" or ".ssh"
		if strings.HasPrefix(path, name+sep) || path == name {
			return true
		}
		// Match as mid-path component: "/home/user/.ssh/keys"
		prefix := sep + name
		if strings.Contains(path, prefix+sep) || strings.HasSuffix(path, prefix) {
			return true
		}
	}
	return false
}

// containsComponentPair checks for a parent/child directory path at any
// level. E.g., (".config", "gcloud") matches "/home/user/.config/gcloud/"
// and ".config/gcloud/creds.json".
// Handles both absolute paths (starting with sep) and relative paths.
func containsComponentPair(path, sep, parent, child string) bool {
	start := parent + sep + child
	// Match as start: ".config/gcloud/creds.json" or ".config/gcloud"
	if strings.HasPrefix(path, start+sep) || path == start {
		return true
	}
	// Match as mid-path: "/home/user/.config/gcloud/creds.json"
	pair := sep + start
	return strings.Contains(path, pair+sep) || strings.HasSuffix(path, pair)
}

// ─── RiskTier ───────────────────────────────────────────────────────

// RiskTier classifies what a tool can do, from safe to dangerous.
type RiskTier int

const (
	RiskReadOnly RiskTier = iota // no side effects (read_file, grep, …)
	RiskDraft                    // simulate, no external effect
	RiskWrite                    // mutates files / external state
)

// toolRisk maps a tool name to its tier. Unknown tools default to RiskWrite
// (fail closed), so any unregistered tool is gated until classified.
func toolRisk(name string) RiskTier {
	switch name {
	case "read_file", "grep", "glob", "list_dir", SubmitTool:
		return RiskReadOnly
	case "write_file", "edit_file", "delete_file", "bash":
		return RiskWrite
	default:
		return RiskWrite
	}
}
