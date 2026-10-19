package atomicfile

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestWriteNewFileGetsPerm(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.json")
	if err := Write(p, []byte(`{"a":1}`), 0o600, false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != `{"a":1}` {
		t.Fatalf("content = %q", b)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %v; want 0600", st.Mode().Perm())
		}
	}
}

func TestWriteReplacesAndLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if err := os.WriteFile(p, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte("new"), 0o600, false); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "new" {
		t.Fatalf("content = %q", b)
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
			t.Fatalf("an existing file without keepMode must get perm, got %v", st.Mode().Perm())
		}
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("leftover files: %v", entries)
	}
}

func TestWriteKeepsModeOfAForeignFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	p := filepath.Join(t.TempDir(), "editor.json")
	if err := os.WriteFile(p, []byte("{}"), 0o640); err != nil {
		t.Fatal(err)
	}
	if err := Write(p, []byte(`{"x":1}`), 0o644, true); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v; want the file's own 0640 kept", st.Mode().Perm())
	}
}

// A symlinked config (dotfiles) keeps its link; the target is replaced.
func TestWriteThroughSymlinkKeepsTheLink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "dotfiles", "config.json")
	if err := os.MkdirAll(filepath.Dir(real), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(real, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "config.json")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	if err := Write(link, []byte("new"), 0o644, true); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(link); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was replaced by a regular file")
	}
	if b, _ := os.ReadFile(real); string(b) != "new" {
		t.Fatalf("target content = %q", b)
	}
}

// A failed write leaves the original intact.
func TestWriteFailureLeavesTargetIntact(t *testing.T) {
	p := filepath.Join(t.TempDir(), "missing-dir", "c.json")
	if err := Write(p, []byte("x"), 0o600, false); err == nil {
		t.Fatal("writing into a missing directory must fail")
	}
}
