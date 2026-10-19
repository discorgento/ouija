package engine

import (
	"strings"
	"sync"
)

// BashAllowlist restricts which commands the bash tool may execute.
// Supports pattern matching:
//   - "git" allows any "git ..." invocation (prefix match)
//   - "git status" allows "git status ..." but blocks "git push ..."
//
// When nil or empty, all commands are allowed. Thread-safe.
type BashAllowlist struct {
	mu       sync.RWMutex
	patterns []string // ordered list of allowed patterns
}

// NewBashAllowlist creates an allowlist with the given initial patterns.
func NewBashAllowlist(initial []string) *BashAllowlist {
	clean := cleanPatterns(initial)
	return &BashAllowlist{patterns: clean}
}

// Set replaces the allowlist with the given patterns.
// When nil or empty, all commands are allowed (no restriction).
func (w *BashAllowlist) Set(cmds []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.patterns = cleanPatterns(cmds)
}

// Add adds a pattern to the allowlist at runtime.
// Used by the TUI approval callback to auto-learn approved commands.
// Silently ignores empty strings.
func (w *BashAllowlist) Add(pattern string) {
	if strings.TrimSpace(pattern) == "" {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	// Deduplicate.
	for _, p := range w.patterns {
		if p == pattern {
			return
		}
	}
	w.patterns = append(w.patterns, pattern)
}

// Restricts reports whether the allowlist is non-empty. An empty allowlist
// means "no commands pre-approved yet": callers use this to distinguish
// "nothing configured" (prompt the user / enforce nothing) from "restricted".
func (w *BashAllowlist) Restricts() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.patterns) > 0
}

// IsAllowed returns true if the command matches any allowlist pattern.
// When the allowlist is empty, all commands are allowed (no restriction —
// the permission layer, not this list, is what gates unattended execution).
// Matching:
//   - single-word pattern "git" matches "git status -s" (blanket base command);
//   - multi-word pattern "npm run build" matches the exact string or extends
//     it only with further flags ("npm run build --prod"), never with extra
//     operands ("npm run build ./dist" is a different action → not matched).
func (w *BashAllowlist) IsAllowed(cmd string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if len(w.patterns) == 0 {
		return true
	}
	for _, p := range w.patterns {
		if cmd == p {
			return true
		}
		if !strings.HasPrefix(cmd, p+" ") {
			continue
		}
		if !strings.Contains(p, " ") {
			return true // blanket base command: "git …" allowed
		}
		rest := cmd[len(p)+1:]
		if rest == "" || strings.HasPrefix(rest, "-") {
			return true
		}
	}
	return false
}

// Patterns returns a copy of the current patterns (for persistence).
func (w *BashAllowlist) Patterns() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	out := make([]string, len(w.patterns))
	copy(out, w.patterns)
	return out
}

// cleanPatterns removes empty strings and trims whitespace.
func cleanPatterns(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{})
	for _, p := range in {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if _, dup := seen[p]; dup {
			continue
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}
	return out
}
