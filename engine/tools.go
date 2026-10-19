package engine

import (
	"context"
	"fmt"
	"github.com/discorgento/ouija/schema"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// regexpTimeout bounds how long a single regexp compilation may take.
// Catches ReDoS / catastrophic backtracking from model-supplied patterns.
const regexpTimeout = 2 * time.Second

// maxToolBytes is a generous cap: trimming is lossless for analysis in practice,
// it only avoids dumping huge binary/minified files into context.
const maxToolBytes = 40000

const maxGrepMatches = 200

// maxFileBytes skips reading very large files during grep (minified/generated
// blobs) — bounds memory and time, and they are never useful to read line-by-line.
const maxFileBytes = 2_000_000

type toolEntry struct {
	def     ToolDef
	fn      ToolFn
	preview ToolPreviewFn // optional — generates preview before approval
}

// ToolPreviewFn generates a preview string for a tool call before the tool
// is approved by the user. Return "" for no preview.
type ToolPreviewFn func(ctx context.Context, root string, input map[string]any) string

// ToolRegistry provides tool definitions and execution to agents.
// Implementations must be safe for concurrent use.
type ToolRegistry interface {
	// Defs returns all registered tool definitions in registration order.
	Defs() []ToolDef
	// Has reports whether a tool with the given name exists.
	Has(name string) bool
	// Run executes a tool call and returns a ToolResult.
	Run(ctx context.Context, call ToolCall) ToolResult
	// Root returns the sandbox root directory.
	Root() string
	// Preview generates an optional preview string for a tool call.
	// Returns "" when the tool has no preview or the preview cannot be generated.
	Preview(ctx context.Context, call ToolCall) string
}

// Tools is the sandboxed tool registry handed to every agent.
type Tools struct {
	root           string
	entries        map[string]toolEntry
	order          []string
	escapeApprover func(ctx context.Context, toolName, escapedPath string) bool
	sandbox        *SandboxConfig // bash sandbox; nil or disabled = none
}

// sandboxKey carries the bash sandbox config to the bash tool.
type sandboxKey struct{}

// NewTools builds the default read-only toolset rooted at projectRoot.
func NewTools(root string) *Tools {
	t := &Tools{root: root, entries: map[string]toolEntry{}}
	for _, e := range defaultTools() {
		t.entries[e.def.Name] = e
		t.order = append(t.order, e.def.Name)
	}
	return t
}

// Defs returns the tool definitions advertised to the model.
func (t *Tools) Defs() []ToolDef {
	defs := make([]ToolDef, 0, len(t.order))
	for _, n := range t.order {
		defs = append(defs, t.entries[n].def)
	}
	return defs
}

// Has checks whether a built-in tool with the given name exists.
func (t *Tools) Has(name string) bool {
	_, ok := t.entries[name]
	return ok
}

// Root returns the sandbox root directory.
func (t *Tools) Root() string { return t.root }

// Preview generates a preview for a tool call. Returns "" when the tool
// has no preview function or preview generation fails.
func (t *Tools) Preview(ctx context.Context, call ToolCall) string {
	e, ok := t.entries[call.Name]
	if !ok || e.preview == nil {
		return ""
	}
	return e.preview(ctx, t.root, call.Input)
}

// Run executes a tool call. It always returns a ToolResult, either from the
// tool implementation or from a pre-flight gate (unknown tool, escape rejected).
// When a path argument escapes the sandbox, the escapeApprover callback (if set)
// is invoked to request user approval. If approved, the escape path is attached
// to ctx so resolve() can use it — no global variable needed.
func (t *Tools) Run(ctx context.Context, call ToolCall) ToolResult {
	if t.sandbox != nil && t.sandbox.Enabled {
		ctx = context.WithValue(ctx, sandboxKey{}, t.sandbox)
	}
	e, ok := t.entries[call.Name]
	if !ok {
		return errInvalidInput("unknown tool: " + call.Name)
	}

	if escapedAbs := t.escapedAbs(call); escapedAbs != "" && t.escapeApprover != nil {
		if t.escapeApprover(ctx, call.Name, escapedAbs) {
			ctx = context.WithValue(ctx, escapeKey{}, escapedAbs)
		} else {
			return errRejected(fmt.Sprintf(
				"Your call to '%s' (escape sandbox to %s) was REJECTED. Do NOT call '%s' again. Respond to the user in text instead.",
				call.Name, escapedAbs, call.Name))
		}
	}

	return e.fn(ctx, t.root, call.Input)
}

// escapedAbs checks if the call's "path" argument escapes the sandbox.
// Returns the absolute escaped path (relative to root) if it does, or "".
// Detects both lexical escapes (../../foo) and symlink-based escapes
// (a symlink inside root pointing outside).
func (t *Tools) escapedAbs(call ToolCall) string {
	p, _ := call.Input["path"].(string)
	if p == "" {
		return ""
	}
	rootAbs, err := canonRoot(t.root)
	if err != nil {
		return ""
	}
	abs, err := filepath.Abs(filepath.Join(rootAbs, p))
	if err != nil {
		return ""
	}
	// Lexical escape: path computes to outside root
	if !within(rootAbs, abs) {
		return abs
	}
	// Symlink escape: path is lexically inside root but a symlink
	// component resolves to outside
	real, realErr := filepath.EvalSymlinks(abs)
	if realErr == nil && !within(rootAbs, real) {
		return real // approve the symlink target
	}
	return ""
}

func defaultTools() []toolEntry {
	return []toolEntry{
		// ── Read tools ───────────────────────────────────────────────
		{
			def: ToolDef{
				Name:        "read_file",
				Description: "Read a file.",
				Schema: schema.Obj(map[string]any{
					"path": schema.Str("file path"),
				}, "path"),
			},
			fn: readFileTool,
		},
		{
			def: ToolDef{
				Name:        "grep",
				Description: "Search regex in files.",
				Schema: schema.Obj(map[string]any{
					"pattern": schema.Str("regex"),
					"path":    schema.Str("subdir"),
				}, "pattern"),
			},
			fn: grepTool,
		},
		{
			def: ToolDef{
				Name:        "glob",
				Description: "List files matching a glob.",
				Schema: schema.Obj(map[string]any{
					"pattern": schema.Str("glob, e.g. **/*.py"),
				}, "pattern"),
			},
			fn: globTool,
		},
		{
			def: ToolDef{
				Name:        "list_dir",
				Description: "List directory contents.",
				Schema: schema.Obj(map[string]any{
					"path": schema.Str("dir path"),
				}),
			},
			fn: listDirTool,
		},

		// ── Write tools ──────────────────────────────────────────────
		writeFileDef(),
		editFileDef(),
		deleteFileDef(),

		// ── Bash ─────────────────────────────────────────────────────
		bashDef(),
	}
}

func readFileTool(ctx context.Context, root string, in map[string]any) ToolResult {
	p, _ := in["path"].(string)
	full, err := resolve(ctx, root, p)
	if err != nil {
		return errInvalidInput(err.Error())
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return errNotFound(err.Error())
	}
	return ToolResult{Data: trim(string(b)), Mime: "text/plain"}
}

func grepTool(ctx context.Context, root string, in map[string]any) ToolResult {
	pattern, _ := in["pattern"].(string)
	sub, _ := in["path"].(string)
	if sub == "" {
		sub = "."
	}
	re, err := compileRegex(pattern)
	if err != nil {
		return errInvalidInput(err.Error())
	}
	base, err := resolve(ctx, root, sub)
	if err != nil {
		return errInvalidInput(err.Error())
	}
	rootAbs, _ := filepath.Abs(root)
	var out strings.Builder
	matches := 0
	walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 {
			return nil // never follow symlinks out of the sandbox
		}
		if matches >= maxGrepMatches {
			return filepath.SkipAll
		}
		if skipPath(path) {
			return nil
		}
		if isProtectedPath(path) {
			return nil // credentials/system files are unreadable even via grep
		}
		if info, ierr := d.Info(); ierr == nil && info.Size() > maxFileBytes {
			return nil // skip huge files (minified/generated)
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		rel, _ := filepath.Rel(rootAbs, path)
		for i, line := range strings.Split(string(b), "\n") {
			if re.MatchString(line) {
				fmt.Fprintf(&out, "%s:%d: %s\n", rel, i+1, strings.TrimSpace(line))
				matches++
				if matches >= maxGrepMatches {
					break
				}
			}
		}
		return nil
	})
	if matches == 0 {
		if walkErr != nil {
			return errInternal("grep: " + walkErr.Error())
		}
		return ToolResult{Data: "no matches", Mime: "text/plain"}
	}
	if walkErr != nil {
		out.WriteString(fmt.Sprintf("\n[walk error during search: %v]\n", walkErr))
	}
	if matches >= maxGrepMatches {
		out.WriteString(fmt.Sprintf("\n[match limit %d reached]\n", maxGrepMatches))
	}
	return ToolResult{Data: trim(out.String()), Mime: "text/plain", Truncated: matches >= maxGrepMatches}
}

func globTool(ctx context.Context, root string, in map[string]any) ToolResult {
	pattern, _ := in["pattern"].(string)
	rootAbs, _ := canonRoot(root)
	matches, err := filepath.Glob(filepath.Join(rootAbs, pattern))
	if err != nil {
		return errInvalidInput(err.Error())
	}
	var out strings.Builder
	for _, m := range matches {
		if !within(rootAbs, m) {
			continue // pattern escaped the sandbox (e.g. ../../*) — drop it
		}
		rel, _ := filepath.Rel(rootAbs, m)
		out.WriteString(rel + "\n")
	}
	if out.Len() == 0 {
		return ToolResult{Data: "no matches", Mime: "text/plain"}
	}
	return ToolResult{Data: trim(out.String()), Mime: "text/plain"}
}

func listDirTool(ctx context.Context, root string, in map[string]any) ToolResult {
	sub, _ := in["path"].(string)
	if sub == "" {
		sub = "."
	}
	full, err := resolve(ctx, root, sub)
	if err != nil {
		return errInvalidInput(err.Error())
	}
	entries, err := os.ReadDir(full)
	if err != nil {
		return errNotFound(err.Error())
	}
	var out strings.Builder
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		out.WriteString(name + "\n")
	}
	return ToolResult{Data: trim(out.String()), Mime: "text/plain"}
}

func skipPath(p string) bool {
	if strings.Contains(p, string(filepath.Separator)+".git"+string(filepath.Separator)) ||
		strings.Contains(p, string(filepath.Separator)+"node_modules"+string(filepath.Separator)) {
		return true
	}
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".pdf", ".zip", ".gz", ".tar",
		".woff", ".woff2", ".ttf", ".ico", ".mp4", ".lock", ".min.js", ".map":
		return true
	}
	return false
}

// compileRegex compiles a regexp with a timeout to guard against ReDoS
// (catastrophic backtracking) from model-supplied patterns.
func compileRegex(pattern string) (*regexp.Regexp, error) {
	type result struct {
		re  *regexp.Regexp
		err error
	}
	ch := make(chan result, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- result{nil, fmt.Errorf("regexp compilation panicked: %v", r)}
			}
		}()
		re, err := regexp.Compile(pattern)
		ch <- result{re, err}
	}()
	select {
	case r := <-ch:
		return r.re, r.err
	case <-time.After(regexpTimeout):
		return nil, fmt.Errorf("regexp compilation timed out after %v (pattern too complex)", regexpTimeout)
	}
}

func trim(s string) string {
	if len(s) <= maxToolBytes {
		return s
	}
	return s[:maxToolBytes] + fmt.Sprintf("\n\n…[trimmed: %d more bytes]", len(s)-maxToolBytes)
}
