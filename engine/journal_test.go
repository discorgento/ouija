package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Undo puts back an edited file, removes a created one, restores a deleted
// one, and refuses what it cannot (a directory).
func TestJournalUndo(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()
	j := &Journal{}
	write := func(rel, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(rel)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}

	write("a.go", "old")
	j.snapshot(ctx, root, "a.go")
	write("a.go", "new")
	j.snapshot(ctx, root, "new.go")
	write("new.go", "created")
	write("gone.go", "keep me")
	j.snapshot(ctx, root, "gone.go")
	os.Remove(filepath.Join(root, "gone.go"))
	write("dir/x.go", "x")
	j.snapshot(ctx, root, "dir")

	if _, err := j.Undo(); err == nil {
		t.Fatal("undid a directory delete")
	}
	if msg, err := j.Undo(); err != nil || read("gone.go") != "keep me" {
		t.Fatalf("restore deleted: %q %v → %q", msg, err, read("gone.go"))
	}
	if msg, err := j.Undo(); err != nil || read("new.go") != "<missing>" {
		t.Fatalf("remove created: %q %v", msg, err)
	}
	if _, err := j.Undo(); err != nil || read("a.go") != "old" {
		t.Fatalf("restore edited: %v → %q", err, read("a.go"))
	}
	if fi, _ := os.Stat(filepath.Join(root, "a.go")); runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600 { // no Unix modes on Windows
		t.Errorf("mode = %v, want 0600 kept", fi.Mode().Perm())
	}
	if _, err := j.Undo(); err == nil {
		t.Fatal("undo on an empty journal")
	}
	if i := j.snapshot(ctx, root, "../outside"); i != -1 || j.Len() != 0 {
		t.Fatal("recorded a path outside the project")
	}
}

// The runner records a successful change and drops a failed one.
func TestRunnerJournalsWrites(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.go"), []byte("hello"), 0o644)
	eng := New(EngineConfig{Config: &Config{ProjectRoot: root}})
	eng.SetPermissionConfig(&PermissionConfig{Default: Allow})
	run := func(name string, in map[string]any) ToolResult {
		return eng.runner.Run(context.Background(), "p", ToolCall{Name: name, Input: in}, nil, nil)
	}
	if r := run("edit_file", map[string]any{"path": "a.go", "oldString": "hello", "newString": "bye"}); r.Error != nil {
		t.Fatal(r.Error.Message)
	}
	if r := run("edit_file", map[string]any{"path": "a.go", "oldString": "absent", "newString": "x"}); r.Error == nil {
		t.Fatal("the bad edit succeeded")
	}
	if n := eng.Journal().Len(); n != 1 {
		t.Fatalf("journal has %d entries, want the successful edit only", n)
	}
	if _, err := eng.Journal().Undo(); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.go")); string(b) != "hello" {
		t.Fatalf("after undo: %q", b)
	}
}
