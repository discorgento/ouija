package engine

import (
	"context"
	"fmt"
	"github.com/discorgento/ouija/schema"
	"path/filepath"
	"strings"
)

// maxToolBytes is a generous cap: trimming is lossless for analysis in practice,
// it only avoids dumping huge binary/minified files into context.
const maxToolBytes = 40000

const maxGrepMatches = 200

// maxFileBytes is the size above which grep streams a file line by line
// instead of reading it whole.
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
		// A protected target is refused before any prompt: no approval can
		// hand out ~/.ssh, and a yes clicked in a hurry must not either.
		if isProtectedPath(escapedAbs) {
			return errInvalidInput("path is protected (credentials/system files): " + escapedAbs)
		}
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
	// Lexical escape: path computes to outside root. The prompt shows where
	// it physically lands: ../shared/id_rsa with shared -> ~/.ssh is ~/.ssh.
	if !within(rootAbs, abs) {
		return physicalPath(abs)
	}
	// Symlink escape: path is lexically inside root but a symlink
	// component (dangling ones included) resolves to outside.
	if real := physicalPath(abs); !within(rootAbs, real) {
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
				Description: "Read a text file. Long files come in parts: pass offset and limit (lines) to read further.",
				Schema: schema.Obj(map[string]any{
					"path":   schema.Str("file path"),
					"offset": map[string]any{"type": "integer", "description": "first line to read (1-based); default 1"},
					"limit":  map[string]any{"type": "integer", "description": "how many lines to read; default as many as fit"},
				}, "path"),
			},
			fn: readFileTool,
		},
		{
			def: ToolDef{
				Name:        "grep",
				Description: "Search a regex in the project's files (.git and node_modules skipped unless path points inside them).",
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
				Description: "List files matching a glob; ** matches any number of directories.",
				Schema: schema.Obj(map[string]any{
					"pattern": schema.Str("glob relative to the project root, e.g. **/*.py or src/**/test_*.go"),
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

// skipPath reports a file grep does not search, by its extension: images,
// archives, fonts, lock files, source maps, minified bundles. (Directories —
// .git, node_modules — are skipped by the walk, below the searched path.)
func skipPath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".svg", ".pdf", ".zip", ".gz", ".tar",
		".woff", ".woff2", ".ttf", ".ico", ".mp4", ".lock", ".map":
		return true
	}
	return strings.HasSuffix(strings.ToLower(p), ".min.js") // Ext sees only ".js"
}

func trim(s string) string {
	if len(s) <= maxToolBytes {
		return s
	}
	return strings.ToValidUTF8(s[:maxToolBytes], "") + fmt.Sprintf("\n\n…[trimmed: %d more bytes]", len(s)-maxToolBytes)
}
