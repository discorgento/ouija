package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWriteFileCreatesFile(t *testing.T) {
	root := t.TempDir()
	res := writeFileTool(context.Background(), root, map[string]any{
		"path":    "test.txt",
		"content": "hello world",
	})
	if res.Error != nil {
		t.Fatalf("writeFile: %v", res.Error)
	}
	if !strings.Contains(res.Data, "✓ wrote") {
		t.Fatalf("unexpected output: %s", res.Data)
	}
	b, err := os.ReadFile(filepath.Join(root, "test.txt"))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(b) != "hello world" {
		t.Fatalf("content = %q, want %q", string(b), "hello world")
	}
}

func TestWriteFileCreatesDirectories(t *testing.T) {
	root := t.TempDir()
	res := writeFileTool(context.Background(), root, map[string]any{
		"path":    "a/b/c/test.txt",
		"content": "nested",
	})
	if res.Error != nil {
		t.Fatalf("writeFile nested: %v", res.Error)
	}
	if _, err := os.Stat(filepath.Join(root, "a", "b", "c", "test.txt")); err != nil {
		t.Fatalf("nested file not created: %v", err)
	}
}

func TestWriteFileRejectsEscape(t *testing.T) {
	root := t.TempDir()
	res := writeFileTool(context.Background(), root, map[string]any{
		"path":    "../../../etc/evil.txt",
		"content": "bad",
	})
	if res.Error == nil {
		t.Fatal("expected error for path escape")
	}
}

func TestWriteFileRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()

	// Create a symlink inside root that points outside.
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Skip("symlinks unavailable:", err) // Windows without the privilege
	}

	res := writeFileTool(context.Background(), root, map[string]any{
		"path":    "escape/evil.txt",
		"content": "should not be written here",
	})
	if res.Error == nil {
		t.Fatal("expected error for symlink path escape")
	}

	// Verify the file does NOT exist outside.
	if _, err := os.Stat(filepath.Join(outside, "evil.txt")); !os.IsNotExist(err) {
		t.Fatal("file was written outside root despite symlink escape check")
	}
}

func TestWriteFileOverwritesExisting(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test.txt"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := writeFileTool(context.Background(), root, map[string]any{
		"path":    "test.txt",
		"content": "new content",
	})
	if res.Error != nil {
		t.Fatalf("writeFile overwrite: %v", res.Error)
	}
	b, _ := os.ReadFile(filepath.Join(root, "test.txt"))
	if string(b) != "new content" {
		t.Fatalf("overwritten content = %q, want %q", string(b), "new content")
	}
}

func TestEditFileReplace(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := editFileTool(context.Background(), root, map[string]any{
		"path":      "test.txt",
		"oldString": "hello",
		"newString": "goodbye",
	})
	if res.Error != nil {
		t.Fatalf("editFile: %v", res.Error)
	}
	if !strings.Contains(res.Data, "✓ edited") {
		t.Fatalf("unexpected output: %s", res.Data)
	}
	b, _ := os.ReadFile(filepath.Join(root, "test.txt"))
	if string(b) != "goodbye world" {
		t.Fatalf("edited content = %q, want %q", string(b), "goodbye world")
	}
}

func TestEditFileNotFound(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := editFileTool(context.Background(), root, map[string]any{
		"path":      "test.txt",
		"oldString": "notfound",
		"newString": "anything",
	})
	if res.Error == nil {
		t.Fatal("expected error for non-matching oldString")
	}
}

func TestEditFileMultipleMatches(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test.txt"), []byte("foo foo foo"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := editFileTool(context.Background(), root, map[string]any{
		"path":      "test.txt",
		"oldString": "foo",
		"newString": "bar",
	})
	if res.Error == nil {
		t.Fatal("expected error for multiple matches")
	}
}

func TestEditFileRejectsEscape(t *testing.T) {
	root := t.TempDir()
	res := editFileTool(context.Background(), root, map[string]any{
		"path":      "../../../etc/passwd",
		"oldString": "root",
		"newString": "nobody",
	})
	if res.Error == nil {
		t.Fatal("expected error for path escape")
	}
}

func TestDeleteFileRemovesFile(t *testing.T) {
	root := t.TempDir()
	fp := filepath.Join(root, "test.txt")
	if err := os.WriteFile(fp, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := deleteFileTool(context.Background(), root, map[string]any{"path": "test.txt"})
	if res.Error != nil {
		t.Fatalf("deleteFile: %v", res.Error)
	}
	if !strings.Contains(res.Data, "✓ deleted") {
		t.Fatalf("unexpected output: %s", res.Data)
	}
	if _, err := os.Stat(fp); !os.IsNotExist(err) {
		t.Fatal("file still exists after delete")
	}
}

func TestDeleteFileRejectsRoot(t *testing.T) {
	root := t.TempDir()
	res := deleteFileTool(context.Background(), root, map[string]any{"path": "."})
	if res.Error == nil {
		t.Fatal("expected error deleting project root")
	}
}

func TestDeleteFileRejectsEscape(t *testing.T) {
	root := t.TempDir()
	res := deleteFileTool(context.Background(), root, map[string]any{"path": "../../../etc"})
	if res.Error == nil {
		t.Fatal("expected error for path escape")
	}
}

func TestBuildDiffSnippet(t *testing.T) {
	diff := BuildDiffSnippet("hello world", "hello world", "goodbye world")
	if !strings.Contains(diff, "-hello world") {
		t.Fatal("diff should contain old line")
	}
	if !strings.Contains(diff, "+goodbye world") {
		t.Fatal("diff should contain new line")
	}
}

func TestEditFilePreview(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview := editFilePreview(context.Background(), root, map[string]any{
		"path":      "test.txt",
		"oldString": "hello",
		"newString": "goodbye",
	})
	if !strings.Contains(preview, "-hello") {
		t.Fatal("preview should show old text")
	}
	if !strings.Contains(preview, "+goodbye") {
		t.Fatal("preview should show new text")
	}
}

func TestEditFilePreviewNoMatch(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "test.txt"), []byte("hello world"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview := editFilePreview(context.Background(), root, map[string]any{
		"path":      "test.txt",
		"oldString": "notfound",
		"newString": "anything",
	})
	if preview != "" {
		t.Fatalf("expected empty preview for no match, got %q", preview)
	}
}

func TestEditFilePreviewNoFile(t *testing.T) {
	preview := editFilePreview(context.Background(), t.TempDir(), map[string]any{
		"path":      "nonexistent.txt",
		"oldString": "foo",
		"newString": "bar",
	})
	if preview != "" {
		t.Fatalf("expected empty preview for nonexistent file, got %q", preview)
	}
}

func TestWriteFilePreviewCreate(t *testing.T) {
	preview := writeFilePreview(context.Background(), t.TempDir(), map[string]any{
		"path":    "new.txt",
		"content": "hello\nworld\n",
	})
	if !strings.Contains(preview, "create") {
		t.Fatalf("preview should say 'create', got %q", preview)
	}
	if !strings.Contains(preview, "new.txt") {
		t.Fatalf("preview should contain path, got %q", preview)
	}
}

func TestWriteFilePreviewOverwrite(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "existing.txt"), []byte("old content"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview := writeFilePreview(context.Background(), root, map[string]any{
		"path":    "existing.txt",
		"content": "new content that is longer",
	})
	if !strings.Contains(preview, "overwrite") {
		t.Fatalf("preview should say 'overwrite', got %q", preview)
	}
}

func TestWriteFilePreviewEmptyPath(t *testing.T) {
	preview := writeFilePreview(context.Background(), t.TempDir(), map[string]any{
		"path":    "",
		"content": "hello",
	})
	if preview != "" {
		t.Fatalf("expected empty preview for empty path, got %q", preview)
	}
}

func TestDeleteFilePreviewFile(t *testing.T) {
	root := t.TempDir()
	fp := filepath.Join(root, "todelete.txt")
	if err := os.WriteFile(fp, []byte("delete me"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview := deleteFilePreview(context.Background(), root, map[string]any{
		"path": "todelete.txt",
	})
	if !strings.Contains(preview, "delete") {
		t.Fatalf("preview should say 'delete', got %q", preview)
	}
	if !strings.Contains(preview, "todelete.txt") {
		t.Fatalf("preview should contain path, got %q", preview)
	}
}

func TestDeleteFilePreviewDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "mydir"), 0o755); err != nil {
		t.Fatal(err)
	}
	preview := deleteFilePreview(context.Background(), root, map[string]any{
		"path": "mydir",
	})
	if !strings.Contains(preview, "empty directory") {
		t.Fatalf("preview should say 'empty directory', got %q", preview)
	}
}

func TestDeleteFilePreviewDirWithItems(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "mydir")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}
	preview := deleteFilePreview(context.Background(), root, map[string]any{
		"path": "mydir",
	})
	if !strings.Contains(preview, "2 items") {
		t.Fatalf("preview should say '2 items', got %q", preview)
	}
}

func TestDeleteFilePreviewNotExist(t *testing.T) {
	preview := deleteFilePreview(context.Background(), t.TempDir(), map[string]any{
		"path": "missing.txt",
	})
	if !strings.Contains(preview, "not found") {
		t.Fatalf("preview should note file not found, got %q", preview)
	}
}

// A project root reached through a symlink (macOS /tmp → /private/tmp) still
// allows deleting its files, and still refuses deleting the root itself.
func TestDeleteFileUnderSymlinkedRoot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	real := t.TempDir()
	link := filepath.Join(t.TempDir(), "proj")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(real, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := deleteFileTool(context.Background(), link, map[string]any{"path": "a.txt"}); res.Error != nil {
		t.Fatalf("delete a.txt under a symlinked root: %v", res.Error.Message)
	}
	if res := deleteFileTool(context.Background(), link, map[string]any{"path": "."}); res.Error == nil {
		t.Fatal("deleted the project root")
	}
}
