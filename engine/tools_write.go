package engine

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/discorgento/ouija/schema"
)

// writeFileTool creates or overwrites a file with the given content.
// The path is relative to the project root. Parent directories are created
// automatically. Returns the absolute path of the written file.
func writeFileTool(ctx context.Context, root string, in map[string]any) ToolResult {
	p, _ := in["path"].(string)
	content, _ := in["content"].(string)

	// Containment check (handles both lexical and symlink escapes, plus
	// pre-approved escapes via context). resolve() walks intermediate path
	// components so symlink directories are detected even when the final
	// file doesn't exist yet — no TOCTOU gap.
	full, err := resolve(ctx, root, p)
	if err != nil {
		return errPermissionDenied(err.Error())
	}

	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return errInternal(fmt.Sprintf("mkdir %s: %v", dir, err))
	}

	mode := fs.FileMode(0o644)
	if fi, fiErr := os.Stat(full); fiErr == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.WriteFile(full, []byte(content), mode); err != nil {
		return errInternal(fmt.Sprintf("write %s: %v", p, err))
	}

	return ToolResult{Data: fmt.Sprintf("✓ wrote %s (%d bytes)", p, len(content)), Mime: "text/plain"}
}

// editFileTool applies a search-and-replace edit to an existing file.
// oldString must match exactly once; newString replaces it.
// Returns the result description and a git-style diff snippet on success.
func editFileTool(ctx context.Context, root string, in map[string]any) ToolResult {
	p, _ := in["path"].(string)
	oldStr, _ := in["oldString"].(string)
	newStr, _ := in["newString"].(string)

	full, err := resolve(ctx, root, p)
	if err != nil {
		return errPermissionDenied(err.Error())
	}

	b, err := os.ReadFile(full)
	if err != nil {
		return errNotFound(fmt.Sprintf("read %s: %v", p, err))
	}
	orig := string(b)

	// Count occurrences.
	count := strings.Count(orig, oldStr)
	switch {
	case count == 0:
		return errInvalidInput(fmt.Sprintf("edit %s: oldString not found", p))
	case count > 1:
		return errInvalidInput(fmt.Sprintf("edit %s: oldString appears %d times (must match exactly once)", p, count))
	}

	updated := strings.Replace(orig, oldStr, newStr, 1)
	mode := fs.FileMode(0o644)
	if fi, fiErr := os.Stat(full); fiErr == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.WriteFile(full, []byte(updated), mode); err != nil {
		return errInternal(fmt.Sprintf("write %s: %v", p, err))
	}

	// Build a unified diff snippet with context.
	diff := BuildDiffSnippet(orig, oldStr, newStr)
	return ToolResult{Data: fmt.Sprintf("✓ edited %s\n%s", p, diff), Mime: "text/plain"}
}

// deleteFileTool removes a file or empty directory.
// Path must be within the project root. Refuses to delete the project root.
// Prevents deletion outside the sandbox via symlink traversal.
func deleteFileTool(ctx context.Context, root string, in map[string]any) ToolResult {
	p, _ := in["path"].(string)

	full, err := resolve(ctx, root, p)
	if err != nil {
		return errPermissionDenied(err.Error())
	}

	// Never delete the project root.
	rootAbs, _ := filepath.Abs(root)
	if full == rootAbs {
		return errInvalidInput("refusing to delete project root")
	}

	// Resolve symlinks before deletion so that os.RemoveAll does not follow
	// a symlink outside the sandbox and delete unintended files.
	real, realErr := filepath.EvalSymlinks(full)
	if realErr == nil && !within(rootAbs, real) {
		return errSandboxEscape(fmt.Sprintf("refusing to delete outside sandbox via symlink: %s", p))
	}

	if err := os.RemoveAll(full); err != nil {
		return errInternal(fmt.Sprintf("delete %s: %v", p, err))
	}
	return ToolResult{Data: fmt.Sprintf("✓ deleted %s", p), Mime: "text/plain"}
}

// BuildDiffSnippet creates a unified diff snippet with context lines,
// similar to `git diff -U3`. It shows up to 3 unchanged lines before
// and after the edit, with `-` for removed and `+` for added lines.
// The first parameter is the full original file content.
func BuildDiffSnippet(orig, oldStr, newStr string) string {
	idx := strings.Index(orig, oldStr)
	if idx < 0 {
		return ""
	}

	before := orig[:idx]
	after := orig[idx+len(oldStr):]

	startLine := strings.Count(before, "\n") + 1 // 1-indexed

	oldLines := splitLines(oldStr)
	newLines := splitLines(newStr)

	// Context before: up to 3 unchanged lines preceding the edit.
	beforeLines := splitLines(before)
	ctxBefore := len(beforeLines)
	if ctxBefore > 3 {
		beforeLines = beforeLines[ctxBefore-3:]
		ctxBefore = 3
	}

	// Context after: up to 3 unchanged lines following the edit.
	afterLines := splitLines(after)
	ctxAfter := len(afterLines)
	if ctxAfter > 3 {
		afterLines = afterLines[:3]
		ctxAfter = 3
	}

	hunkStart := startLine - ctxBefore
	if hunkStart < 1 {
		hunkStart = 1
	}

	var b strings.Builder
	fmt.Fprintf(&b, "  @@ -%d,%d +%d,%d @@\n",
		hunkStart, ctxBefore+len(oldLines)+ctxAfter,
		hunkStart, ctxBefore+len(newLines)+ctxAfter)

	for _, line := range beforeLines {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	for _, line := range oldLines {
		fmt.Fprintf(&b, "  -%s\n", line)
	}
	for _, line := range newLines {
		fmt.Fprintf(&b, "  +%s\n", line)
	}
	for _, line := range afterLines {
		fmt.Fprintf(&b, "  %s\n", line)
	}

	return strings.TrimRight(b.String(), "\n")
}

// splitLines splits s into lines, discarding a trailing empty element
// that results from a final newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// writeFilePreview generates a preview for a write_file call — shows the file
// path, size, and whether it will create or overwrite.
func writeFilePreview(ctx context.Context, root string, in map[string]any) string {
	p, _ := in["path"].(string)
	content, _ := in["content"].(string)
	if p == "" {
		return ""
	}
	full, err := resolve(ctx, root, p)
	if err != nil {
		return p + " (outside sandbox)"
	}

	action := "create"
	existing := ""
	if fi, fiErr := os.Stat(full); fiErr == nil {
		action = "overwrite"
		existing = fmt.Sprintf(" (was %d bytes)", fi.Size())
	}

	lineInfo := fmt.Sprintf("%d lines", strings.Count(content, "\n")+1)
	if content == "" {
		lineInfo = "1 line (empty)"
	}

	preview := fmt.Sprintf("will %s %s (%s, %d bytes%s)", action, p, lineInfo, len(content), existing)
	return preview
}

// Tool definitions for write tools.
func writeFileDef() toolEntry {
	return toolEntry{
		def: ToolDef{
			Name:        "write_file",
			Description: "Create or overwrite a file.",
			Schema: schema.Obj(map[string]any{
				"path":    schema.Str("file path"),
				"content": schema.Str("content"),
			}, "path", "content"),
		},
		fn:      writeFileTool,
		preview: writeFilePreview,
	}
}

// editFilePreview generates a diff preview for an edit_file call without
// modifying the file. Returns the unified diff string (not ANSI-colored).
func editFilePreview(ctx context.Context, root string, in map[string]any) string {
	p, _ := in["path"].(string)
	oldStr, _ := in["oldString"].(string)
	newStr, _ := in["newString"].(string)
	if p == "" || oldStr == "" {
		return ""
	}
	full, err := resolve(ctx, root, p)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(full)
	if err != nil {
		return ""
	}
	return BuildDiffSnippet(string(b), oldStr, newStr)
}

func editFileDef() toolEntry {
	return toolEntry{
		def: ToolDef{
			Name:        "edit_file",
			Description: "Search-and-replace in a file.",
			Schema: schema.Obj(map[string]any{
				"path":      schema.Str("file path"),
				"oldString": schema.Str("exact text to replace"),
				"newString": schema.Str("replacement"),
			}, "path", "oldString", "newString"),
		},
		fn:      editFileTool,
		preview: editFilePreview,
	}
}

// deleteFilePreview generates a preview for a delete_file call — shows the
// path, file type, size, and warns about non-empty directories.
func deleteFilePreview(ctx context.Context, root string, in map[string]any) string {
	p, _ := in["path"].(string)
	if p == "" {
		return ""
	}
	full, err := resolve(ctx, root, p)
	if err != nil {
		return "will delete " + p + " (outside sandbox?)"
	}

	fi, fiErr := os.Stat(full)
	if fiErr != nil {
		return "will delete " + p + " (not found locally — may already be gone)"
	}

	if fi.IsDir() {
		entries, _ := os.ReadDir(full)
		if len(entries) == 0 {
			return fmt.Sprintf("will delete empty directory %s", p)
		}
		return fmt.Sprintf("⚠ will delete directory %s (%d items inside)", p, len(entries))
	}

	return fmt.Sprintf("will delete %s (%d bytes, mod %s)", p, fi.Size(), fi.ModTime().Format("06-01-02 15:04"))
}

func deleteFileDef() toolEntry {
	return toolEntry{
		def: ToolDef{
			Name:        "delete_file",
			Description: "Delete a file or empty dir.",
			Schema: schema.Obj(map[string]any{
				"path": schema.Str("file path"),
			}, "path"),
		},
		fn:      deleteFileTool,
		preview: deleteFilePreview,
	}
}
