package engine

import (
	"bytes"
	"cmp"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/discorgento/ouija/schema"

	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/syntax"
)

const (
	// bashDefaultTimeout is the maximum execution time for a bash command.
	bashDefaultTimeout = 30 * time.Second
)

// bashTool executes a shell command in the project root.
// The command runs via /bin/sh -c and captures stdout+stderr.
// Timeout is 30s by default; the model can request longer via the timeout field.
// Returns the combined output, trimmed to maxToolBytes.
func bashTool(ctx context.Context, root string, in map[string]any) ToolResult {
	command, _ := in["command"].(string)
	if strings.TrimSpace(command) == "" {
		return errInvalidInput("bash: command is required")
	}

	// Parse optional timeout (seconds).
	timeout := bashDefaultTimeout
	if t, ok := in["timeout"].(float64); ok && t > 0 {
		timeout = time.Duration(t) * time.Second
		if timeout > 120*time.Second {
			timeout = 120 * time.Second // hard cap
		}
	}

	// Derive from parent context so cancellation propagates (e.g. user
	// pressing Esc in the TUI). If the parent context has a deadline that
	// is shorter than timeout, the parent's deadline wins.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/bin/sh", "-c", command)
	cmd.Dir = root
	cmd.Env = sanitizeEnv()
	// WaitDelay bounds how long the process may survive after ctx cancellation.
	// On Unix the Cancel function (set by CommandContext) sends SIGKILL, which
	// is instantaneous, so this is a safety net for edge cases.
	cmd.WaitDelay = 3 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// Capture output even on non-zero exit.
	out := strings.TrimSpace(stdout.String())
	errOut := strings.TrimSpace(stderr.String())

	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return ToolResult{Error: &ToolError{Code: ErrCodeTimeout, Message: fmt.Sprintf("bash: command timed out after %v", timeout)}}
		}
		// Non-zero exit — include stderr in output, not as a Go error.
		if errOut != "" {
			out = errOut
		}
		if out == "" {
			out = fmt.Sprintf("exit error: %v", err)
		}
		return ToolResult{Data: trim(out), Mime: "text/plain"}
	}

	// Combine stdout + stderr if both present.
	if out != "" && errOut != "" {
		out = out + "\n" + errOut
	} else if errOut != "" {
		out = errOut
	}

	return ToolResult{Data: trim(cmp.Or(out, "(no output)")), Mime: "text/plain"}
}

// sanitizeEnv filters the parent process environment to remove credential-like
// variables before passing them to a bash subprocess. This prevents accidental
// leakage of API keys, tokens, and other secrets via the shell.
// Reads fresh from os.Environ() each call (no caching) so that t.Setenv in
// tests works correctly — the per-call allocation is negligible for a CLI tool.
func sanitizeEnv() []string {
	var isSensitive = func(key string) bool {
		switch {
		// Block vars with credential-like prefixes.
		case strings.HasPrefix(key, "AWS_"),
			strings.HasPrefix(key, "GITHUB_"),
			strings.HasPrefix(key, "GITLAB_"),
			strings.HasPrefix(key, "BITBUCKET_"),
			strings.HasPrefix(key, "AZURE_"),
			strings.HasPrefix(key, "GOOGLE_"),
			strings.HasPrefix(key, "OPENAI_"),
			strings.HasPrefix(key, "ANTHROPIC_"),
			strings.HasPrefix(key, "TOGETHER_"),
			strings.HasPrefix(key, "OUIJA_"):
			return true
		// Block vars with credential-like substrings in the key name.
		case strings.Contains(key, "API_KEY"),
			strings.Contains(key, "APIKEY"),
			strings.Contains(key, "SECRET"),
			strings.Contains(key, "PASSWORD"),
			strings.Contains(key, "TOKEN"),
			strings.Contains(key, "CREDENTIAL"),
			strings.Contains(key, "AUTH"):
			return true
		}
		return false
	}

	raw := os.Environ()
	out := make([]string, 0, len(raw))
	for _, kv := range raw {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		if isSensitive(strings.ToUpper(kv[:eq])) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// bashPreview generates a preview for a bash call — shows the command, a
// summary of base commands extracted from it, and a warning if it matches the
// denylist.
func bashPreview(ctx context.Context, root string, in map[string]any) string {
	cmd, _ := in["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		return ""
	}

	cmds := ExtractBaseCommands(cmd)
	danger := IsDenied(cmd)

	var b strings.Builder
	fmt.Fprintf(&b, "will run: %s", cmd)
	// Long commands are cut at 400 chars: the approval decision must see the
	// payload (base64 blobs, heredocs), not a 120-char teaser.
	if len(cmd) > 400 {
		b.Reset()
		fmt.Fprintf(&b, "will run: %s…", cmd[:400])
	}
	if len(cmds) > 0 {
		b.WriteString("  [")
		for i, c := range cmds {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(c)
		}
		b.WriteString("]")
	}
	if danger {
		b.WriteString("  ⚠ matches denylist (may be blocked)")
	}
	if timeout, ok := in["timeout"].(float64); ok && timeout > 0 {
		b.WriteString(fmt.Sprintf("  timeout: %.0fs", timeout))
	}
	return b.String()
}

func bashDef() toolEntry {
	return toolEntry{
		def: ToolDef{
			Name:        "bash",
			Description: "Execute a shell command.",
			Schema: schema.Obj(map[string]any{
				"command": schema.Str("shell command"),
				"timeout": map[string]any{
					"type":        "number",
					"description": "timeout in secs (max 120)",
				},
			}, "command"),
		},
		fn:      bashTool,
		preview: bashPreview,
	}
}

// ExtractBaseCommands returns all base commands from a shell command string.
// Uses mvdan.cc/sh's real shell parser to build an AST, then walks the tree
// to find every command invocation — including inside command substitutions
// ($(...) or “), pipelines, and chained statements (&&, ||, ;).
// Env-var prefixes (OLLAMA_HOST=localhost) are skipped.
// Returns nil for empty or parse-failed commands (fail-open to a single
// segment fallback to avoid blocking legitimate commands on trivial syntax).
func ExtractBaseCommands(cmd string) []string {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}

	parser := syntax.NewParser()
	ast, err := parser.Parse(strings.NewReader(cmd), "")
	if err != nil {
		// Fall back to the first word for unparsable commands so that
		// edge cases (incomplete heredocs, etc.) don't break the TUI.
		if f := strings.Fields(cmd); len(f) > 0 {
			return []string{f[0]}
		}
		return nil
	}

	seen := make(map[string]struct{})
	var baseCmds []string

	// collect extracts the base command name from a CallExpr, skipping
	// env-var assignments, and appends it (deduplicated) to cmds.
	collect := func(call *syntax.CallExpr) {
		if len(call.Args) == 0 {
			return
		}
		// Skip env-var prefix tokens (VAR=value). The parser places
		// them in Assigns, but we also guard against literals with "=".
		base := call.Args[0].Lit()
		if base == "" || strings.Contains(base, "=") {
			return
		}
		if _, dup := seen[base]; dup {
			return
		}
		seen[base] = struct{}{}
		baseCmds = append(baseCmds, base)
	}

	syntax.Walk(ast, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			collect(n)
		}
		return true
	})

	if len(baseCmds) == 0 {
		return nil
	}
	return baseCmds
}

// ExtractCommands returns the full command segments from a shell command
// string — each segment includes its arguments. For example:
//
//	"git status -s && rm -rf /tmp" → ["git status -s", "rm -rf /tmp"]
//
// Uses the real shell parser (mvdan.cc/sh) and walks the AST to find every
// command invocation. Skips env-var prefixes.
// Returns nil for empty or unparsable commands.
func ExtractCommands(cmd string) []string {
	if strings.TrimSpace(cmd) == "" {
		return nil
	}

	parser := syntax.NewParser()
	ast, err := parser.Parse(strings.NewReader(cmd), "")
	if err != nil {
		// Fallback: return the whole trimmed command.
		if s := strings.TrimSpace(cmd); s != "" {
			return []string{s}
		}
		return nil
	}

	seen := make(map[string]struct{})
	var segs []string

	collect := func(call *syntax.CallExpr) {
		if len(call.Args) == 0 {
			return
		}
		base := call.Args[0].Lit()
		if base == "" || strings.Contains(base, "=") {
			return
		}
		// Extract the original source text for this call expression using
		// its position in the source. This preserves quoting, $(), etc.
		// When the call has env-var assignments (VAR=value), use the
		// first argument's position to skip the prefix.
		start := int(call.Pos().Offset())
		if len(call.Assigns) > 0 && len(call.Args) > 0 {
			if ap := call.Args[0].Pos().Offset(); ap > uint(start) {
				start = int(ap)
			}
		}
		end := int(call.End().Offset())
		if start >= end || start < 0 || end > len(cmd) {
			return
		}
		seg := strings.TrimSpace(cmd[start:end])
		if seg == "" {
			return
		}
		if _, dup := seen[seg]; dup {
			return
		}
		seen[seg] = struct{}{}
		segs = append(segs, seg)
	}

	syntax.Walk(ast, func(node syntax.Node) bool {
		switch n := node.(type) {
		case *syntax.CallExpr:
			collect(n)
		}
		return true
	})

	if len(segs) == 0 {
		return nil
	}
	return segs
}

// denylistEntry is a hardcoded dangerous pattern that is always rejected.
type denylistEntry struct {
	match  string   // the pattern to match
	tokens []string // word-tokenized pattern; if set, exact token match is required
}

// denylistPatterns are hardcoded dangerous command patterns that are always
// rejected, regardless of the allowlist. These block the most destructive
// actions (wipe filesystem, fork bomb, raw device writes).
// Each entry may specify `tokens` for word-level matching (avoids false
// positives like `rm -rf /tmp` matching the pattern `rm -rf /`).
var denylistPatterns = []denylistEntry{
	// rm -rf where the target is root or root-glob
	{tokens: []string{"rm", "-rf", "/"}},
	{tokens: []string{"rm", "-rf", "/*"}},
	{tokens: []string{"rm", "-rf", "--no-preserve-root", "/"}},
	{tokens: []string{"rm", "-rf", "~"}},
	{tokens: []string{"rm", "-rf", "$home"}},

	// dd writing to a raw device
	{tokens: []string{"dd", "if=/dev/zero", "of=/dev/sda"}},
	{tokens: []string{"dd", "if=/dev/random", "of=/dev/sda"}},
	{tokens: []string{"dd", "if=/dev/urandom", "of=/dev/sda"}},

	// Filesystem operations on raw devices (any mkfs.*, fdisk, parted)
	{match: "mkfs."},
	{match: "fdisk /dev/sda"},
	{match: "parted /dev/sda"},

	// Fork bomb
	{match: ":(){ :|:& };"},

	// chmod/chown entire root
	{tokens: []string{"chmod", "-r", "0", "/"}},
	{tokens: []string{"chown", "-r", "root:root", "/"}},
}

// IsDenied returns true if the command matches a denylist pattern — either
// the static list above (kept for transparency/back-compat) or the semantic
// analysis below, which catches flag-order variants (`rm -fr /`), execution
// wrappers (`sudo rm -fr /`, `bash -c 'rm -fr /'`) and device commands the
// static entries miss (`mkfs` without a dot, `dd of=/dev/sdb`, `fdisk /dev/nvme0`).
func IsDenied(cmd string) bool {
	normalized := strings.TrimSpace(cmd)
	if normalized == "" {
		return false
	}
	lower := strings.ToLower(normalized)
	fields := strings.Fields(lower)

	for _, e := range denylistPatterns {
		if len(e.tokens) > 0 {
			// Token-level match: all tokens must appear consecutively.
			if len(fields) < len(e.tokens) {
				continue
			}
			for i := 0; i <= len(fields)-len(e.tokens); i++ {
				match := true
				for j, tok := range e.tokens {
					if fields[i+j] != tok {
						match = false
						break
					}
				}
				if match {
					return true
				}
			}
		} else if strings.Contains(lower, e.match) {
			return true
		}
	}

	return semanticDenied(normalized, lower)
}

// reparseShells run a nested command string (`sh -c '…'`, `eval '…'`).
var reparseShells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true}

// deviceTools wipe or repartition raw devices when given a /dev path.
var deviceTools = map[string]bool{
	"fdisk": true, "parted": true, "sgdisk": true, "gdisk": true,
	"wipefs": true, "blkdiscard": true, "pvremove": true, "vgremove": true,
}

// semanticDenied analyses each command segment by its tokens (segment = one
// invocation, chains/pipes already split by ExtractCommands).
func semanticDenied(normalized, lower string) bool {
	// Fork bomb: match regardless of whitespace (`:(){ :|:& };:` etc).
	compact := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, lower)
	if strings.Contains(compact, ":(){:|:&}") {
		return true
	}

	segs := ExtractCommands(normalized)
	if len(segs) == 0 {
		segs = []string{normalized}
	}
	for _, seg := range segs {
		if segmentDenied(strings.Fields(seg), 0) {
			return true
		}
	}
	return false
}

// segmentDenied applies the destructive-command rules to one invocation's
// token list. Every token position that names a rule-bearing command is
// analysed, not just the first word: execution wrappers put the real command
// after their own flags (`sudo -u root rm -fr /`, `xargs -n1 rm -fr /`) and
// env prefixes sit before it (`VAR=1 rm -rf /`), so a first-word-only scan
// would miss them. Nested command strings (`bash -c '...'`, `eval '...'`) are
// re-parsed up to `depth` levels deep.
func segmentDenied(fields []string, depth int) bool {
	if depth > 3 || len(fields) == 0 {
		return false
	}

	for i, f := range fields {
		base := strings.ToLower(unquote(f))
		rest := fields[i+1:]

		// Nested command strings: `bash -c '...'`, `eval '...'`.
		if base == "eval" {
			if segmentDenied(strings.Fields(stripShellQuotes(joinWords(rest))), depth+1) {
				return true
			}
			continue
		}
		if reparseShells[base] {
			nested := false
			for j, t := range rest {
				if t == "-c" {
					nested = true
					if segmentDenied(strings.Fields(stripShellQuotes(joinWords(rest[j+1:]))), depth+1) {
						return true
					}
					break
				}
			}
			if nested {
				continue
			}
			// No -c (running a script): keep scanning the remaining tokens.
		}

		switch {
		case base == "rm":
			if rmDenied(rest) {
				return true
			}
		case base == "dd":
			if ddDenied(rest) {
				return true
			}
		case strings.HasPrefix(base, "mkfs"):
			// Any mkfs variant formats a filesystem — blocked wherever it
			// appears (the static list already blocks `mkfs.` by substring).
			return true
		case deviceTools[base]:
			if refsDev(rest) {
				return true
			}
		case base == "chmod" || base == "chown":
			if recursiveOnRoot(rest) {
				return true
			}
		}
	}
	return false
}

// unquote strips shell quoting from a token so `"rm"`, `'/'` or
// `of=/dev/sda` reduce to the word actually being run.
func unquote(t string) string { return strings.Trim(t, "\"'`") }

// rmDenied reports recursive deletion of the root/home itself.
// `rm -rf /tmp` stays legal; `rm -rf /`, `rm -fr ~`, `rm -r -f $HOME` don't.
func rmDenied(rest []string) bool {
	if !hasRecursiveFlag(rest) {
		return false
	}
	for _, t := range flagsAndTargets(rest) {
		if isRootish(t) {
			return true
		}
	}
	return false
}

// ddDenied reports dd writing onto a raw block device (`of=/dev/sda`).
func ddDenied(rest []string) bool {
	for _, t := range rest {
		v := strings.ToLower(unquote(t))
		if strings.HasPrefix(v, "of=/dev") {
			return true
		}
	}
	return false
}

// refsDev reports device-tool commands pointing at any /dev path (the static
// list only knew /dev/sda).
func refsDev(rest []string) bool {
	for _, t := range rest {
		if strings.HasPrefix(strings.ToLower(unquote(t)), "/dev/") {
			return true
		}
	}
	return false
}

// recursiveOnRoot reports recursive chmod/chown on / or $HOME.
func recursiveOnRoot(rest []string) bool {
	if !hasRecursiveFlag(rest) {
		return false
	}
	for _, t := range flagsAndTargets(rest) {
		if isRootish(t) {
			return true
		}
	}
	return false
}

// hasRecursiveFlag reports whether rest carries a recursive flag: a short
// cluster containing r/R (-rf, -r, -R) or the long --recursive option.
func hasRecursiveFlag(rest []string) bool {
	for _, t := range rest {
		t = unquote(t)
		if t == "--" {
			break
		}
		if t == "--recursive" {
			return true
		}
		if strings.HasPrefix(t, "-") && !strings.HasPrefix(t, "--") && !strings.Contains(t, "=") {
			for _, c := range t[1:] {
				if c == 'r' || c == 'R' {
					return true
				}
			}
		}
	}
	return false
}

// flagsAndTargets splits a word list into non-flag operands. `--` is kept
// simple: everything after it is an operand (paths to delete).
func flagsAndTargets(rest []string) []string {
	var out []string
	afterDD := false
	for _, t := range rest {
		t = unquote(t)
		if !afterDD {
			if t == "--" {
				afterDD = true
				continue
			}
			if strings.HasPrefix(t, "-") && len(t) > 1 {
				continue // flag (short cluster or long option)
			}
		}
		out = append(out, strings.ToLower(t))
	}
	return out
}

// isRootish reports whether a path names the filesystem root or the whole
// home directory (the targets the denylist is about), never a subdirectory.
func isRootish(t string) bool {
	switch t {
	case "/", "/*", "~", "~/", "~/*", "$home", "$home/*", "${home}", "${home}/*", "$home/", "/*/", "/**":
		return true
	}
	return false
}

func joinWords(words []string) string {
	return strings.Join(words, " ")
}

func stripShellQuotes(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\'' || r == '"' || r == '`' {
			return -1
		}
		return r
	}, s)
}

// ─── Protected-path references in bash commands ───────────────────────

// pathTokenTrim strips shell punctuation around a word so that `.env)`,
// `$(cat` or `"~/.ssh/id_rsa"` reduce to a path-like candidate.
const pathTokenTrim = "'\"`" + "(){}[],;:&|<>!?*$"

// commandRefsProtectedPath reports the first word token of cmd that names a
// protected path (credential or system location). bash has no `path` argument
// for the ToolRunner permission gate to inspect, so without this scan a model
// could `cat` the same file the read_file tool refuses.
// False positives are accepted by design: a command word that names `.env`
// or `~/.ssh/…` is rejected with an explicit message instead of being run.
func commandRefsProtectedPath(cmd string) (string, bool) {
	return commandRefsProtectedPathIn("", cmd)
}

// commandRefsProtectedPathIn is commandRefsProtectedPath for a command that
// runs in root. Besides the raw tokens it checks each word's value after quote
// removal (`cat .e""nv` is `cat .env` to the shell) and, when root is set,
// where each word physically lands: a symlink in the project
// (`notes.txt → .env`) is refused like the file it points to.
func commandRefsProtectedPathIn(root, cmd string) (string, bool) {
	for _, c := range protectedPathCandidates(cmd) {
		if isProtectedPath(c) {
			return c, true
		}
		if root != "" {
			if p := physicalCandidate(root, c); p != "" && isProtectedPath(p) {
				return c, true
			}
		}
	}
	return "", false
}

// protectedPathCandidates lists the path-like words of cmd: the raw tokens of
// each command segment, trimmed of shell punctuation, plus every shell word's
// value after quote removal. A `KEY=value` word also yields its value.
func protectedPathCandidates(cmd string) []string {
	segs := ExtractCommands(cmd)
	if len(segs) == 0 {
		segs = []string{cmd}
	}
	var words []string
	for _, seg := range segs {
		words = append(words, strings.Fields(seg)...)
	}
	words = append(words, shellWordValues(cmd)...)

	var out []string
	for _, w := range words {
		candidates := []string{w}
		if eq := strings.IndexByte(w, '='); eq > 0 {
			candidates = append(candidates, w[eq+1:]) // of=/dev/…, KEY=.env value…
		}
		for _, c := range candidates {
			c = strings.Trim(c, pathTokenTrim)
			if c == "" || strings.HasPrefix(c, "-") {
				continue
			}
			out = append(out, c)
		}
	}
	return out
}

// shellWordValues returns the value every word of cmd has after quote removal,
// as the shell would pass it (`.e""nv`, `'.e'nv` and `.e\nv` are all `.env`).
// Nothing is executed: a word needing a real expansion (command substitution)
// is skipped, while the words inside the substitution are still visited.
func shellWordValues(cmd string) []string {
	f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return nil
	}
	var out []string
	syntax.Walk(f, func(n syntax.Node) bool {
		if w, ok := n.(*syntax.Word); ok {
			// Fields, not Literal: argument semantics, so an unquoted
			// backslash is removed too. A nil config never globs or runs.
			if vs, err := expand.Fields(nil, w); err == nil {
				out = append(out, vs...)
			}
		}
		return true
	})
	return out
}

// physicalCandidate resolves a command word to the path it would open from
// root — `~` is the home directory, a relative word is relative to root — with
// its symlinks resolved. Words that are not paths simply resolve to a
// harmless location under root.
func physicalCandidate(root, word string) string {
	switch {
	case word == "~" || strings.HasPrefix(word, "~/"):
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		word = filepath.Join(home, word[1:])
	case !filepath.IsAbs(word):
		word = filepath.Join(root, word)
	}
	return physicalPath(word)
}
