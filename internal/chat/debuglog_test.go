package chat

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// The debug log (prompts, file contents) lives in the user's own cache
// directory, owner-only, and is never opened through a planted link.
func TestDebugLogIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix modes and the XDG cache dir")
	}
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	if runtime.GOOS == "darwin" {
		t.Setenv("HOME", cache)
	}
	dir, _ := os.UserCacheDir()
	if err := os.MkdirAll(filepath.Join(dir, "ouija"), 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "ouija", "chat-debug.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	f, got, err := openDebugLog()
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	if got != path {
		t.Fatalf("debug log at %s, want %s", got, path)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("debug log mode %v, want 0600", fi.Mode().Perm())
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Errorf("debug log dir mode %v, want 0700", fi.Mode().Perm())
	}

	os.Remove(path)
	if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), path); err != nil {
		t.Fatal(err)
	}
	if f, _, err := openDebugLog(); err == nil {
		f.Close()
		t.Fatal("opened the debug log through a symlink")
	}
}
