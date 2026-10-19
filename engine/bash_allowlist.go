package engine

import (
	"strings"
	"sync"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
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

// IsAllowed returns true if the command matches the allowlist.
// When the allowlist is empty, all commands are allowed (no restriction —
// the permission layer, not this list, is what gates unattended execution).
//
// The command is parsed as shell, and EVERY simple command in it must match a
// pattern on its own: with "git" allowed, `git status | head` needs "head"
// too, and `git status; curl … | sh` is not allowed. A command whose effect
// the text alone does not pin down — command/process substitution, parameter
// or arithmetic expansion, env assignments (`GIT_SSH_COMMAND=… git fetch`),
// declarations, functions, or output redirection other than to /dev/null —
// is never pre-approved, so it falls back to the human prompt (Ask) or is
// refused (Allow). Matching is per simple command, on its words after quote
// removal:
//   - single-word pattern "git" matches "git status -s" (blanket base
//     command — it trusts everything that program can do, e.g. `git -c`);
//   - multi-word pattern "npm run build" matches those words exactly, extended
//     only with flags ("npm run build --prod"), never with extra operands
//     ("npm run build ./dist" is a different action → not matched).
func (w *BashAllowlist) IsAllowed(cmd string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	if len(w.patterns) == 0 {
		return true
	}
	calls, ok := staticCalls(cmd)
	if !ok || len(calls) == 0 {
		return false
	}
	for _, args := range calls {
		if !w.matchesLocked(args) {
			return false
		}
	}
	return true
}

// matchesLocked reports whether one simple command (its words) matches a
// pattern. The caller holds w.mu.
func (w *BashAllowlist) matchesLocked(args []string) bool {
	for _, p := range w.patterns {
		pw := strings.Fields(p)
		if len(pw) == 0 || len(args) < len(pw) {
			continue
		}
		match := true
		for i := range pw {
			if args[i] != pw[i] {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		if len(pw) == 1 {
			return true // blanket base command: "git …" allowed
		}
		rest := args[len(pw):]
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return true
		}
	}
	return false
}

// AllowlistPatterns returns the patterns that pre-approve exactly the simple
// commands of cmd (one per command, its words joined), or nil when cmd is not
// fully static (see IsAllowed). The chat uses it to learn a "Session"
// approval: approving `echo $(whoami)` must not silently allowlist `whoami`.
func AllowlistPatterns(cmd string) []string {
	calls, ok := staticCalls(cmd)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(calls))
	for _, args := range calls {
		out = append(out, strings.Join(args, " "))
	}
	return out
}

// staticCalls parses cmd and returns the words of each simple command, after
// quote removal. ok is false when the command does not parse or contains a
// construct whose effect the text alone does not determine.
func staticCalls(cmd string) (calls [][]string, ok bool) {
	f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return nil, false
	}
	ok = true
	syntax.Walk(f, func(n syntax.Node) bool {
		if !ok {
			return false
		}
		switch n := n.(type) {
		case *syntax.CmdSubst, *syntax.ProcSubst, *syntax.ParamExp, *syntax.ArithmExp,
			*syntax.ArithmCmd, *syntax.DeclClause, *syntax.LetClause, *syntax.FuncDecl,
			*syntax.CoprocClause, *syntax.TestClause:
			ok = false
		case *syntax.Redirect:
			ok = staticRedirect(n)
		case *syntax.CallExpr:
			if len(n.Assigns) > 0 {
				ok = false // env prefix changes what the command does
				return false
			}
			if len(n.Args) == 0 {
				return true
			}
			args, err := expand.Fields(staticExpandConfig(), n.Args...)
			if err != nil || len(args) == 0 {
				ok = false
				return false
			}
			calls = append(calls, args)
		}
		return ok
	})
	return calls, ok
}

// staticRedirect allows input redirections, fd duplication (2>&1) and output
// to /dev/null; any other output target is a file write the pattern never
// approved.
func staticRedirect(r *syntax.Redirect) bool {
	switch r.Op {
	case syntax.RdrIn, syntax.Hdoc, syntax.DashHdoc, syntax.WordHdoc:
		return true
	case syntax.DplIn, syntax.DplOut:
		target := r.Word.Lit()
		return target == "-" || isAllDigits(target)
	}
	return r.Word != nil && r.Word.Lit() == "/dev/null"
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
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
