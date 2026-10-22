package engine

import (
	"context"
	"fmt"
	"io"
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
	// file doesn't exist yet; the directories and the file are then made
	// and opened through the project's os.Root (openResolved), so a link
	// swapped in after the check is not followed.
	full, err := resolve(ctx, root, p)
	if err != nil {
		return errPermissionDenied(err.Error())
	}
	if err := checkGitWrite(full); err != nil {
		return errPermissionDenied(err.Error())
	}

	dir := filepath.Dir(full)
	if err := mkdirResolved(root, dir); err != nil {
		return errInternal(fmt.Sprintf("mkdir %s: %v", dir, err))
	}

	f, err := openResolved(root, full, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return errInternal(fmt.Sprintf("write %s: %v", p, err))
	}
	defer f.Close()
	if err := rewrite(f, []byte(content)); err != nil {
		return errInternal(fmt.Sprintf("write %s: %v", p, err))
	}

	return ToolResult{Data: fmt.Sprintf("✓ wrote %s (%d bytes)", p, len(content))}
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
	if err := checkGitWrite(full); err != nil {
		return errPermissionDenied(err.Error())
	}

	// One open for the read and the write: the file edited is the one read.
	f, err := openResolved(root, full, os.O_RDWR, 0)
	if err != nil {
		return errNotFound(fmt.Sprintf("read %s: %v", p, err))
	}
	defer f.Close()
	b, err := readEditable(f)
	if err != nil {
		return errInvalidInput(fmt.Sprintf("edit %s: %v", p, err))
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
	if err := rewrite(f, []byte(updated)); err != nil {
		return errInternal(fmt.Sprintf("write %s: %v", p, err))
	}

	// Build a unified diff snippet with context.
	diff := BuildDiffSnippet(orig, oldStr, newStr)
	return ToolResult{Data: fmt.Sprintf("✓ edited %s\n%s", p, diff)}
}

// rewrite replaces the contents of f, an open file, with data. Opened, not
// recreated: an existing file keeps its permissions (an executable script
// stays executable); a new one got 0644 when it was opened.
func rewrite(f *os.File, data []byte) error {
	if err := f.Truncate(0); err != nil {
		return err
	}
	_, err := f.WriteAt(data, 0)
	return err
}

// deleteFileTool removes a file or a directory with its contents (the
// approval preview warns how many items a directory holds).
// Path must be within the project root. Refuses to delete the project root.
// Prevents deletion outside the sandbox via symlink traversal.
func deleteFileTool(ctx context.Context, root string, in map[string]any) ToolResult {
	p, _ := in["path"].(string)

	full, err := resolve(ctx, root, p)
	if err != nil {
		return errPermissionDenied(err.Error())
	}
	if err := checkGitWrite(full); err != nil {
		return errPermissionDenied(err.Error())
	}

	// Never delete the project root. canonRoot, like resolve: with a symlink
	// in the root's own path (macOS /tmp → /private/tmp) an Abs root never
	// matched, and every delete was refused as a sandbox escape.
	rootAbs, err := canonRoot(root)
	if err != nil {
		return errInternal(err.Error())
	}
	if full == rootAbs {
		return errInvalidInput("refusing to delete project root")
	}

	// Resolve symlinks before deletion so that os.RemoveAll does not follow
	// a symlink outside the sandbox and delete unintended files.
	real, realErr := filepath.EvalSymlinks(full)
	if realErr == nil && !within(rootAbs, real) {
		return errSandboxEscape(fmt.Sprintf("refusing to delete outside sandbox via symlink: %s", p))
	}

	// A directory may hold protected files (app/.env): resolve only checked
	// the directory's own path, and RemoveAll took them with it.
	if held := protectedInside(full); len(held) > 0 {
		return errPermissionDenied(fmt.Sprintf("refusing to delete %s: it holds protected files (%s)", p, strings.Join(held, ", ")))
	}
	if err := removeResolved(rootAbs, full); err != nil {
		return errInternal(fmt.Sprintf("delete %s: %v", p, err))
	}
	return ToolResult{Data: fmt.Sprintf("✓ deleted %s", p)}
}

// removeResolved removes full and what it holds; inside the project through
// an os.Root, so a parent directory swapped for a symlink after the checks
// cannot redirect the removal out of the project.
func removeResolved(rootAbs, full string) error {
	// The parent through rootRel; the last name is removed as itself (a
	// link, not its target).
	dir, ok := rootRel(rootAbs, filepath.Dir(full))
	if !ok {
		return os.RemoveAll(full) // an approved escape
	}
	rel := filepath.Join(dir, filepath.Base(full))
	r, err := os.OpenRoot(rootAbs)
	if err != nil {
		return err
	}
	defer r.Close()
	return r.RemoveAll(rel)
}

// protectedInside lists (up to five) the protected entries under dir, relative
// to it; nil for a file or a directory without any. WalkDir does not follow
// symlinks, so a link inside is checked as the link, which RemoveAll removes
// without touching its target.
func protectedInside(dir string) []string {
	var held []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || path == dir {
			return nil
		}
		// A nested repository's .git goes with it: its config and hooks are
		// protected against being written, not against the whole clone
		// being deleted.
		if d.IsDir() && d.Name() == ".git" {
			return fs.SkipDir
		}
		if isProtectedPath(path) {
			rel, _ := filepath.Rel(dir, path)
			held = append(held, rel)
			if len(held) == 5 {
				return fs.SkipAll
			}
		}
		return nil
	})
	return held
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

	// Where the write lands, not the name asked: a symlink notes.txt →
	// Makefile previewed as "overwrite notes.txt".
	preview := fmt.Sprintf("will %s %s (%s, %d bytes%s)", action, targetLabel(root, p), lineInfo, len(content), existing)
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
	f, err := openResolved(root, full, os.O_RDONLY, 0)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := readEditable(f)
	if err != nil {
		return ""
	}
	return BuildDiffSnippet(string(b), oldStr, newStr)
}

// maxEditBytes is the largest file edit_file reads whole. The preview reads
// the file before the approval prompt, so a multi-gigabyte file in a
// repository would otherwise exhaust memory before anyone was asked.
const maxEditBytes = 16 << 20

// readEditable reads a file for edit_file, refusing one past maxEditBytes.
func readEditable(f *os.File) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(f, maxEditBytes+1))
	if err == nil && len(b) > maxEditBytes {
		return nil, fmt.Errorf("the file is larger than %d MB; edit it with bash instead", maxEditBytes>>20)
	}
	return b, err
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

	// What is removed: the last name as itself (a link, not its target), in
	// its parent directory as reached on disk.
	name := filepath.Join(physicalPath(filepath.Dir(full)), filepath.Base(full))
	if rootAbs, err := canonRoot(root); err == nil && within(rootAbs, name) {
		name, _ = filepath.Rel(rootAbs, name)
	}
	fi, fiErr := os.Lstat(full)
	if fiErr != nil {
		return "will delete " + name + " (not found locally — may already be gone)"
	}

	if fi.IsDir() {
		entries, _ := os.ReadDir(full)
		if len(entries) == 0 {
			return fmt.Sprintf("will delete empty directory %s", name)
		}
		return fmt.Sprintf("⚠ will delete directory %s (%d items inside)", name, len(entries))
	}

	return fmt.Sprintf("will delete %s (%d bytes, mod %s)", name, fi.Size(), fi.ModTime().Format("06-01-02 15:04"))
}

func deleteFileDef() toolEntry {
	return toolEntry{
		def: ToolDef{
			Name:        "delete_file",
			Description: "Delete a file, or a directory with everything in it (refused if it holds a protected file).",
			Schema: schema.Obj(map[string]any{
				"path": schema.Str("file path"),
			}, "path"),
		},
		fn:      deleteFileTool,
		preview: deleteFilePreview,
	}
}
