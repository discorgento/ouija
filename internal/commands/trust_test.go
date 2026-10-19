package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/discorgento/ouija/internal/app"
)

func isolateHome(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)
}

// `ouija trust <path> --revoke` revokes: flag parsing used to stop at the
// path, so the flag was ignored and the file got trusted instead.
func TestTrustRevokeAfterPath(t *testing.T) {
	isolateHome(t)
	path := filepath.Join(t.TempDir(), "ouija.json")
	if err := os.WriteFile(path, []byte(`{"permission":{"default":"allow"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := app.Trust(path); err != nil {
		t.Fatal(err)
	}
	if err := Trust([]string{path, "--revoke"}); err != nil {
		t.Fatal(err)
	}
	if app.IsTrusted(path) {
		t.Fatal("trust <path> --revoke left the file trusted")
	}
	if err := Trust([]string{path, "extra"}); err == nil {
		t.Fatal("a stray argument was accepted")
	}
}
