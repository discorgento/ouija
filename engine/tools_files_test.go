package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// ** matches any depth (filepath.Glob read it as *), skipped dirs stay out.
func TestGlobDoubleStar(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{
		"top.go": "", "a/b/c/deep.go": "", "a/x.txt": "", "node_modules/m/i.go": "", ".git/h.go": "",
	})
	res := globTool(context.Background(), root, map[string]any{"pattern": "**/*.go"})
	for _, want := range []string{"top.go", filepath.Join("a", "b", "c", "deep.go")} {
		if !strings.Contains(res.Data, want) {
			t.Errorf("**/*.go missed %s: %q", want, res.Data)
		}
	}
	if strings.Contains(res.Data, "node_modules") || strings.Contains(res.Data, ".git") || strings.Contains(res.Data, "x.txt") {
		t.Errorf("unexpected matches: %q", res.Data)
	}
	if res := globTool(context.Background(), root, map[string]any{"pattern": "a/**/*.go"}); !strings.Contains(res.Data, "deep.go") || strings.Contains(res.Data, "top.go") {
		t.Errorf("a/**/*.go = %q", res.Data)
	}
}

// read_file reads windows of a long file and refuses binaries.
func TestReadFileWindows(t *testing.T) {
	root := t.TempDir()
	var b strings.Builder
	for i := 1; i <= 20000; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	writeTree(t, root, map[string]string{"big.txt": b.String(), "bin.dat": "ab\x00cd"})
	first := readFileTool(context.Background(), root, map[string]any{"path": "big.txt"})
	if !strings.Contains(first.Data, "line 1\n") || !strings.Contains(first.Data, "read on with offset") {
		t.Fatalf("first window lacks the continuation note: %q", first.Data[len(first.Data)-80:])
	}
	part := readFileTool(context.Background(), root, map[string]any{"path": "big.txt", "offset": float64(19999), "limit": float64(1)})
	if !strings.HasPrefix(part.Data, "line 19999\n") || strings.Contains(part.Data, "line 20000\n") {
		t.Fatalf("offset window = %q", part.Data)
	}
	if res := readFileTool(context.Background(), root, map[string]any{"path": "bin.dat"}); res.Error == nil {
		t.Fatal("a binary file was returned")
	}
}

// grep clips a long matching line, searches large files, and searches inside
// node_modules when asked to.
func TestGrepAtScale(t *testing.T) {
	root := t.TempDir()
	long := strings.Repeat("a", 100000) + "NEEDLE" + strings.Repeat("b", 100000)
	large := strings.Repeat("filler\n", maxFileBytes/7+10) + "NEEDLE here\n"
	writeTree(t, root, map[string]string{
		"bundle.js": long, "large.txt": large, "src/x.go": "NEEDLE in code",
		"node_modules/pkg/i.js": "NEEDLE in dep",
	})
	res := grepTool(context.Background(), root, map[string]any{"pattern": "NEEDLE"})
	if len(res.Data) > 5000 {
		t.Fatalf("a long line filled the result (%d bytes)", len(res.Data))
	}
	for _, want := range []string{"large.txt", filepath.Join("src", "x.go")} {
		if !strings.Contains(res.Data, want) {
			t.Errorf("grep missed %s: %q", want, res.Data)
		}
	}
	if strings.Contains(res.Data, "node_modules") {
		t.Error("grep walked into node_modules from the root")
	}
	dep := grepTool(context.Background(), root, map[string]any{"pattern": "NEEDLE", "path": "node_modules/pkg"})
	if !strings.Contains(dep.Data, "in dep") {
		t.Fatalf("grep inside node_modules = %q", dep.Data)
	}
	one := grepTool(context.Background(), root, map[string]any{"pattern": "NEEDLE", "path": "src/x.go"})
	if !strings.Contains(one.Data, "x.go") || strings.Contains(one.Data, "bundle") {
		t.Fatalf("grep on one file = %q", one.Data)
	}
}

// A cancelled search stops instead of walking the whole tree.
func TestGrepHonoursCancel(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"a.txt": "x"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res := grepTool(ctx, root, map[string]any{"pattern": "x"}); res.Error == nil {
		t.Fatalf("a cancelled grep returned %q", res.Data)
	}
}

// The file picker sees what the search tools see, and attached files are read
// through the sandbox.
func TestProjectFilesAndRead(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"a.go", "dir/b.go", ".git/HEAD", "node_modules/x/y.js"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), []byte("package x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	files, err := ProjectFiles(context.Background(), root, 100)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(files, ",") != "a.go,dir/b.go" {
		t.Fatalf("files = %v, want a.go and dir/b.go only", files)
	}
	if got, err := ReadProjectFile(context.Background(), root, "dir/b.go"); err != nil || got != "package x\n" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if _, err := ReadProjectFile(context.Background(), root, "../outside"); err == nil {
		t.Fatal("read outside the project")
	}
	if few, _ := ProjectFiles(context.Background(), root, 1); len(few) != 1 {
		t.Fatalf("limit 1 gave %d files", len(few))
	}
}
