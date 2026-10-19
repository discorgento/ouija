package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Protected paths must be unreachable through every path-resolving tool, not
// only through the ToolRunner's permission gate (which inspects input["path"]).
func TestResolveRejectsProtectedPaths(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=1"), 0o600); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	if _, err := SafeResolve(ctx, root, ".env"); err == nil {
		t.Error("resolve(.env) must be rejected: protected filename inside the root")
	}
	if _, err := SafeResolve(ctx, root, "src/main.go"); err != nil {
		t.Errorf("resolve(src/main.go) = %v, want nil", err)
	}
	if _, err := SafeResolve(ctx, root, "../../etc/passwd"); err == nil {
		t.Error("resolve(../../etc/passwd) must be rejected: escapes the sandbox")
	}
}

// A symlinked directory inside the root that points outside must not become a
// read path (SafeResolve reflects the same check the file tools use).
func TestSafeResolveRefusesSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("s"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "link")); err != nil {
		t.Skipf("cannot create symlink: %v", err)
	}

	if _, err := SafeResolve(context.Background(), root, "link/secret.txt"); err == nil {
		t.Error("resolve must reject a path whose symlink component escapes the root")
	}
}

// grep walks the tree itself, so it needs its own protected-path filter: the
// permission gate only ever sees the top-level `path` argument.
func TestGrepSkipsProtectedFiles(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("TOKEN=NEEDLE123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app.go"), []byte("// NEEDLE123\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	res := grepTool(context.Background(), root, map[string]any{"pattern": "NEEDLE123"})
	if res.Error != nil {
		t.Fatalf("grep failed: %v", res.Error)
	}
	out := res.Data
	if strings.Contains(out, "TOKEN=NEEDLE123") {
		t.Errorf("grep leaked a protected file's contents:\n%s", out)
	}
	if !strings.Contains(out, "app.go") {
		t.Errorf("grep should still match ordinary files, got:\n%s", out)
	}
}

// End-to-end: even with write_file explicitly allowed, the protected-path rule
// denies it (defence in depth behind the ToolRunner's own path check).
func TestWriteFileToProtectedPathDenied(t *testing.T) {
	root := t.TempDir()
	eng := newTestEngine(&mockProvider{}, root)
	cfg := NewPermissionConfig(Allow) // allow everything
	eng.SetPermissionConfig(cfg)

	res := eng.runner.Run(context.Background(), "test",
		ToolCall{Name: "write_file", Input: map[string]any{"path": ".env", "content": "x"}}, nil, nil)
	if res.Error == nil {
		t.Fatal("writing .env must be denied even with allow-everything")
	}
	if _, err := os.Stat(filepath.Join(root, ".env")); !os.IsNotExist(err) {
		t.Error(".env must not have been created")
	}
}
