package engine

import (
	"context"
	"fmt"
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

// A symlink inside the project must not reach a protected file or directory
// that is also inside it: the policy holds for where the path physically
// lands, not only for its name.
func TestProtectedPathsHoldThroughSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	mustWrite := func(rel, content string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite(".env", "SECRET=1")
	mustWrite(".ouija/config.json", `{"apiKey":"sk"}`)
	mustWrite("ok.txt", "public")
	for link, target := range map[string]string{"notes.txt": ".env", "cfgdir": ".ouija", "alias.txt": "ok.txt"} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}
	eng := newTestEngine(&mockProvider{}, root)
	run := func(name string, in map[string]any) ToolResult {
		return eng.tools.Run(context.Background(), ToolCall{Name: name, Input: in})
	}

	for _, p := range []string{"notes.txt", "cfgdir/config.json"} {
		if res := run("read_file", map[string]any{"path": p}); res.Error == nil {
			t.Errorf("read_file %s reached a protected file: %q", p, res.Data)
		}
		if _, err := SafeResolve(context.Background(), root, p); err == nil {
			t.Errorf("SafeResolve %s (MCP resources) allowed a protected file", p)
		}
	}
	// A file that does not exist yet, written through a symlinked directory.
	if res := run("write_file", map[string]any{"path": "cfgdir/new.json", "content": "x"}); res.Error == nil {
		t.Error("write_file through a symlink into .ouija was allowed")
	}
	if _, err := os.Stat(filepath.Join(root, ".ouija", "new.json")); err == nil {
		t.Error("a file was created inside .ouija through the symlink")
	}
	// A symlink to an ordinary file keeps working.
	if res := run("read_file", map[string]any{"path": "alias.txt"}); res.Error != nil || !strings.Contains(fmt.Sprint(res.Data), "public") {
		t.Errorf("read_file through a harmless symlink failed: %+v", res)
	}
}

func TestPhysicalPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if err := os.Mkdir(filepath.Join(root, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		filepath.Join(root, "link"):             filepath.Join(root, "real"),
		filepath.Join(root, "link", "new", "f"): filepath.Join(root, "real", "new", "f"),
		filepath.Join(root, "missing", "a.txt"): filepath.Join(root, "missing", "a.txt"),
	}
	for in, want := range cases {
		if got := physicalPath(in); got != want {
			t.Errorf("physicalPath(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestSkipPath(t *testing.T) {
	sep := string(filepath.Separator)
	for p, want := range map[string]bool{
		"web" + sep + "app.min.js": true, // filepath.Ext sees only ".js"
		"web" + sep + "APP.MIN.JS": true,
		"web" + sep + "app.js":     false,
		"img.PNG":                  true,
		"main.go":                  false,
	} {
		if got := skipPath(p); got != want {
			t.Errorf("skipPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// A dangling symlink (its target outside the root does not exist yet) must
// not let a write land outside: EvalSymlinks fails on it, and the walk used to
// take that for "not created yet".
func TestResolveRejectsDanglingSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "authorized_keys") // does not exist
	if err := os.Symlink(target, filepath.Join(root, "out")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(context.Background(), root, "out"); err == nil {
		t.Fatal("resolve accepted a dangling symlink pointing outside the root")
	}
	res := writeFileTool(context.Background(), root, map[string]any{"path": "out", "content": "x"})
	if res.Error == nil {
		t.Fatal("write_file wrote through a dangling symlink")
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("a file was created outside the root")
	}
	// A dangling link that stays inside the root is still writable.
	if err := os.Symlink(filepath.Join(root, "new.txt"), filepath.Join(root, "in")); err != nil {
		t.Fatal(err)
	}
	if _, err := resolve(context.Background(), root, "in"); err != nil {
		t.Fatalf("in-root dangling link rejected: %v", err)
	}
}

// A symlink swapped in after resolve approved the path is not followed: the
// open goes through the project's os.Root (no way out) and the opened file is
// checked against the policy again (no protected file inside).
func TestOpenResolvedRefusesASymlinkSwappedInAfterTheCheck(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	ctx := context.Background()
	root, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "secret.txt")
	writeTree(t, outside, map[string]string{"secret.txt": "OUTSIDE"})
	writeTree(t, root, map[string]string{"notes.txt": "notes", "out.txt": "out", ".env": "TOKEN=1"})
	swap := func(name, target string) string {
		full, err := resolve(ctx, root, name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(full); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, full); err != nil {
			t.Fatal(err)
		}
		return full
	}

	out := swap("out.txt", secret)
	if f, err := openResolved(root, out, os.O_RDONLY, 0); err == nil {
		f.Close()
		t.Error("a read followed a link swapped in to a file outside the project")
	}
	if f, err := openResolved(root, out, os.O_WRONLY|os.O_CREATE, 0o644); err == nil {
		f.Close()
		t.Error("a write followed a link swapped in to a file outside the project")
	}
	notes := swap("notes.txt", filepath.Join(root, ".env"))
	if f, err := openResolved(root, notes, os.O_RDONLY, 0); err == nil {
		f.Close()
		t.Error("a read followed a link swapped in to .env")
	}
	if f, err := openResolved(root, notes, os.O_RDWR, 0); err == nil {
		f.Close()
		t.Error("a write followed a link swapped in to .env")
	}
	if b, _ := os.ReadFile(secret); string(b) != "OUTSIDE" {
		t.Errorf("the outside file changed: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".env")); string(b) != "TOKEN=1" {
		t.Errorf(".env changed: %q", b)
	}
}

// os.Root refuses absolute symlinks; a project's own absolute link to a file
// inside it still reads, since the open uses the path resolve judged.
func TestReadFileThroughAnAbsoluteSymlinkInsideTheProject(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	writeTree(t, root, map[string]string{"src/real.txt": "content"})
	if err := os.Symlink(filepath.Join(root, "src"), filepath.Join(root, "alias")); err != nil {
		t.Fatal(err)
	}
	res := readFileTool(context.Background(), root, map[string]any{"path": "alias/real.txt"})
	if res.Error != nil || !strings.Contains(res.Data, "content") {
		t.Fatalf("read through an absolute link inside the project: %+v", res)
	}
	if res := writeFileTool(context.Background(), root, map[string]any{"path": "alias/new.txt", "content": "x"}); res.Error != nil {
		t.Fatalf("write through an absolute link inside the project: %+v", res.Error)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "src", "new.txt")); string(b) != "x" {
		t.Fatalf("src/new.txt = %q", b)
	}
}
