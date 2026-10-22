package engine

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// One huge line costs a bounded amount of memory: read_file and grep read a
// line whole, so a 300 MB single-line file took 600 MB and a 4 GB one ran
// out of memory.
func TestLongLineReadsAreBounded(t *testing.T) {
	root := t.TempDir()
	const size = 64 << 20
	line := bytes.Repeat([]byte("a"), size)
	copy(line[10:], "NEEDLE")
	if err := os.WriteFile(filepath.Join(root, "min.js"), append(line, "\nnext\n"...), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	alloc := func(fn func()) uint64 {
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		fn()
		runtime.ReadMemStats(&after)
		return after.TotalAlloc - before.TotalAlloc
	}
	var read, grep ToolResult
	if n := alloc(func() { read = readFileTool(ctx, root, map[string]any{"path": "min.js"}) }); n > 8<<20 {
		t.Errorf("read_file allocated %d MB for a %d MB line", n>>20, size>>20)
	}
	if read.Error != nil || !strings.Contains(read.Data, "[line cut:") || !strings.Contains(read.Data, "next") {
		t.Errorf("read_file: %+v", read)
	}
	if n := alloc(func() { grep = grepTool(ctx, root, map[string]any{"pattern": "NEEDLE|next"}) }); n > 8<<20 {
		t.Errorf("grep allocated %d MB for a %d MB line", n>>20, size>>20)
	}
	if !strings.Contains(grep.Data, "min.js:1:") || !strings.Contains(grep.Data, "min.js:2:") {
		t.Errorf("grep: %q", grep.Data)
	}
}

// Many ** in a glob cost polynomial time, not exponential: each tried every
// split of the path.
func TestGlobManyDoubleStarsIsFast(t *testing.T) {
	pat := strings.Split(strings.Repeat("**/a/", 8)+"z", "/")
	segs := strings.Split(strings.Repeat("a/", 60)+"b", "/")
	done := make(chan bool, 1)
	go func() { done <- matchGlob(pat, segs) }()
	select {
	case got := <-done:
		if got {
			t.Fatal("matched a path that does not end in z")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("matchGlob is exponential in the number of **")
	}
	if !matchGlob(strings.Split("**/**/a/**/b", "/"), strings.Split("x/a/y/b", "/")) {
		t.Fatal("** no longer matches")
	}
	root := t.TempDir()
	if res := globTool(context.Background(), root, map[string]any{"pattern": strings.Repeat("**/x/", 9) + "y"}); res.Error == nil {
		t.Fatal("a pattern with 9 ** was accepted")
	}
	writeTree(t, root, map[string]string{"a/b/c.go": ""})
	if res := globTool(context.Background(), root, map[string]any{"pattern": "**/**/**/*.go"}); !strings.Contains(res.Data, "c.go") {
		t.Fatalf("runs of ** merged wrongly: %+v", res)
	}
}
