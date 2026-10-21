package engine

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
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

// TCP is closed by default under the sandbox: a confined command cannot reach
// even a server on localhost. A port the config lists opens that port only,
// and `all` lifts the restriction.
func TestBashSandboxNetwork(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not found (the test connects with bash's /dev/tcp)")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	port := uint16(ln.Addr().(*net.TCPAddr).Port)
	dial := fmt.Sprintf(`bash -c 'exec 3<>/dev/tcp/127.0.0.1/%d && echo CONNECTED'`, port)

	for _, tc := range []struct {
		name    string
		network *SandboxNetwork
		want    bool
	}{
		{"default: no TCP", nil, false},
		{"another port allowed", &SandboxNetwork{Connect: []uint16{port + 1}}, false},
		{"this port allowed", &SandboxNetwork{Connect: []uint16{port}}, true},
		{"all allowed", &SandboxNetwork{All: true}, true},
	} {
		_, _, run := sandboxFixture(t, &SandboxConfig{Enabled: true, Network: tc.network})
		got := strings.Contains(fmtData(run(dial)), "CONNECTED")
		if got != tc.want {
			t.Errorf("%s: connected = %v, want %v", tc.name, got, tc.want)
		}
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

// Signals to processes outside the sandbox are refused (the scopes were never
// applied: a confined command could kill any of the user's processes).
func TestBashSandboxScopesSignals(t *testing.T) {
	_, _, run := sandboxFixture(t, &SandboxConfig{Enabled: true})
	res := run(fmt.Sprintf("kill -0 %d && echo SIGNALLED || echo refused", os.Getpid()))
	if strings.Contains(fmtData(res), "SIGNALLED") {
		t.Fatalf("a confined command signalled a process outside the sandbox: %q", fmtData(res))
	}
}

// A grant may not cover a credential directory: not the home directory, not
// ~/.config, not the filesystem root.
func TestCheckSandboxGrant(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // os.UserHomeDir on Windows
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("AppData", filepath.Join(home, ".config"))
	for path, ok := range map[string]bool{
		home:                                  false,
		filepath.Join(home, ".config"):        false,
		"/":                                   false,
		filepath.Join(home, ".ssh", "id_rsa"): false,
		filepath.Join(home, ".cache", "go-build"): true,
		filepath.Join(home, "projects", "app"):    true,
	} {
		if err := checkSandboxGrant(path); (err == nil) != ok {
			t.Errorf("checkSandboxGrant(%s) = %v, want ok=%v", path, err, ok)
		}
	}
}
