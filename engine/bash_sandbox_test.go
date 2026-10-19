package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// sandboxFixture lays out a project, a temp dir and a "secret" outside both,
// and returns a bash runner confined to the project.
func sandboxFixture(t *testing.T, sb *SandboxConfig) (project, secretDir string, run func(cmd string) ToolResult) {
	t.Helper()
	if err := sandboxAvailable(); err != nil {
		t.Skipf("no sandbox here: %v", err)
	}
	base := t.TempDir()
	project = filepath.Join(base, "project")
	tmp := filepath.Join(base, "tmp")
	secretDir = filepath.Join(base, "secret")
	for _, d := range []string{project, tmp, secretDir} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(secretDir, "key"), []byte("TOPSECRET"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", tmp) // so the sandbox's writable temp dir is not base
	tools := NewTools(project)
	tools.sandbox = sb
	return project, secretDir, func(cmd string) ToolResult {
		return tools.Run(context.Background(), ToolCall{Name: "bash", Input: map[string]any{"command": cmd}})
	}
}

func TestBashSandboxConfinesTheCommand(t *testing.T) {
	project, secretDir, run := sandboxFixture(t, &SandboxConfig{Enabled: true})

	// Inside the project: normal work goes on.
	if res := run("echo built > out.txt && cat out.txt && ls /usr/bin >/dev/null && echo ok"); res.Error != nil ||
		!strings.Contains(fmtData(res), "built") || !strings.Contains(fmtData(res), "ok") {
		t.Fatalf("work inside the project failed: %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(project, "out.txt")); strings.TrimSpace(string(b)) != "built" {
		t.Fatalf("project write did not land: %q", b)
	}

	// Outside: reading and writing are refused by the kernel, also for the
	// processes the command starts and for paths the text never names.
	key := filepath.Join(secretDir, "key")
	for _, cmd := range []string{
		"cat " + key,
		"sh -c 'cat \"$1\"' _ " + key,
		`f=$(printf '%s/%s' "` + secretDir + `" key); cat "$f"`,
	} {
		if res := run(cmd); strings.Contains(fmtData(res), "TOPSECRET") {
			t.Errorf("%q read a file outside the sandbox: %q", cmd, fmtData(res))
		}
	}
	run("echo pwned > " + filepath.Join(secretDir, "new"))
	if _, err := os.Stat(filepath.Join(secretDir, "new")); err == nil {
		t.Error("a write outside the sandbox landed")
	}
}

// Paths the config lists become readable or writable; protected ones are
// refused outright.
func TestBashSandboxExtraPaths(t *testing.T) {
	base := t.TempDir()
	cache := filepath.Join(base, "cache")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	_, _, run := sandboxFixture(t, &SandboxConfig{Enabled: true, Write: []string{cache}})
	run("echo cached > " + filepath.Join(cache, "c"))
	if b, _ := os.ReadFile(filepath.Join(cache, "c")); strings.TrimSpace(string(b)) != "cached" {
		t.Fatalf("a listed writable path was not writable: %q", b)
	}

	if _, err := buildSandboxPolicy(base, &SandboxConfig{Enabled: true, Read: []string{"~/.ssh"}}); err == nil {
		t.Fatal("listing ~/.ssh must be refused")
	}
}

// Where the OS cannot confine the command, bash refuses instead of running
// unconfined.
func TestBashSandboxFailsClosed(t *testing.T) {
	if runtime.GOOS == "linux" && sandboxAvailable() == nil {
		t.Skip("sandbox available here; the fail-closed path is covered on other OSes")
	}
	tools := NewTools(t.TempDir())
	tools.sandbox = &SandboxConfig{Enabled: true}
	res := tools.Run(context.Background(), ToolCall{Name: "bash", Input: map[string]any{"command": "echo hi"}})
	if res.Error == nil || res.Error.Code != ErrCodeNotAllowed {
		t.Fatalf("got %+v; want a NOT_ALLOWED refusal", res)
	}
}

func fmtData(r ToolResult) string {
	if r.Error != nil {
		return r.Error.Message
	}
	return r.String()
}
