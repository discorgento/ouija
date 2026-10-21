package engine

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
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
	if sandboxAvailable() == nil {
		t.Skip("sandbox available here; the fail-closed path is covered where the OS has none")
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

// With no sandbox in any config, the engine turns the default one on.
func TestEngineDefaultsToTheSandbox(t *testing.T) {
	e := New(EngineConfig{Config: DefaultConfig()})
	if sb := e.tools.sandbox; sb == nil || !sb.Enabled || !sb.Optional {
		t.Fatalf("default sandbox = %+v, want enabled and optional", sb)
	}
	off := DefaultConfig()
	off.Tools = &ToolsConfig{Bash: &BashConfig{Sandbox: &SandboxConfig{Enabled: false}}}
	if sb := New(EngineConfig{Config: off}).tools.sandbox; sb == nil || sb.Enabled {
		t.Fatalf("an explicit enabled:false was not kept: %+v", sb)
	}
}

// The default sandbox on an OS that cannot enforce it runs bash unconfined;
// a sandbox a config asked for is refused there instead.
func TestOptionalSandboxFallsBack(t *testing.T) {
	saved := checkSandbox
	checkSandbox = func() error { return errSandboxUnsupported }
	t.Cleanup(func() { checkSandbox = saved })

	tools := NewTools(t.TempDir())
	tools.sandbox = DefaultSandbox()
	run := func() ToolResult {
		return tools.Run(context.Background(), ToolCall{Name: "bash", Input: map[string]any{"command": "echo hi"}})
	}
	if res := run(); res.Error != nil || !strings.Contains(res.String(), "hi") {
		t.Fatalf("default sandbox without OS support: %+v, want the command to run", res)
	}
	tools.sandbox = &SandboxConfig{Enabled: true}
	if res := run(); res.Error == nil || res.Error.Code != ErrCodeNotAllowed {
		t.Fatalf("asked-for sandbox without OS support: %+v, want a refusal", res)
	}
}

// The default sandbox reads the toolchains in the home directory (~/go's
// module cache, ~/.cargo's registry), never the credentials kept next to them;
// and it writes to a private cache, never the user's real ones, which tools
// run later outside the sandbox trust.
func TestSandboxReachesHomeToolchainsNotCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	for p, body := range map[string]string{
		"go/pkg/mod/m.txt":                  "MODULE",
		".cargo/registry/index/r":           "REGISTRY",
		".nvm/versions/node/v22/bin/node-x": "NODE",
		".nvm/versions/node/v22/etc/npmrc":  "TOPSECRET",
		".ssh/id_rsa":                       "TOPSECRET",
		"Documents/x":                       "TOPSECRET",
		".cargo/credentials.toml":           "TOPSECRET",
		".cache/huggingface/token":          "TOPSECRET",
		".m2/settings.xml":                  "TOPSECRET",
		".gradle/gradle.properties":         "TOPSECRET",
		".local/share/uv/credentials/c":     "TOPSECRET",
		".cache/go-build/keep":              "",
	} {
		full := filepath.Join(home, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, _, run := sandboxFixture(t, DefaultSandbox())
	for p, want := range map[string]string{"go/pkg/mod/m.txt": "MODULE", ".cargo/registry/index/r": "REGISTRY", ".nvm/versions/node/v22/bin/node-x": "NODE"} {
		if res := run("cat " + filepath.Join(home, p)); !strings.Contains(fmtData(res), want) {
			t.Errorf("%s is not readable: %q", p, fmtData(res))
		}
	}
	for _, p := range []string{".ssh/id_rsa", "Documents/x", ".cargo/credentials.toml", ".cache/huggingface/token", ".nvm/versions/node/v22/etc/npmrc",
		".m2/settings.xml", ".gradle/gradle.properties", ".local/share/uv/credentials/c"} {
		// A path built at run time: only the kernel can stop this one.
		res := run(`f=$(printf '%s/%s' "` + home + `" "` + p + `"); cat "$f"`)
		if strings.Contains(fmtData(res), "TOPSECRET") {
			t.Errorf("%s was readable in the sandbox", p)
		}
	}
	// The real caches are read-only; the private one, which GOCACHE and
	// XDG_CACHE_HOME point to, is not.
	run(`f=$(printf '%s/%s' "` + home + `" .cache/go-build/planted); echo x > "$f"`)
	if _, err := os.Stat(filepath.Join(home, ".cache", "go-build", "planted")); err == nil {
		t.Error("the user's real build cache was writable")
	}
	res := run(`mkdir -p "$GOCACHE" && echo ok > "$GOCACHE/x" && case "$GOCACHE" in "` + home + `/.cache/go-build"*) echo REAL;; esac; echo "$XDG_CACHE_HOME"`)
	if s := fmtData(res); !strings.Contains(s, filepath.Join(home, ".cache", "ouija", "sandbox")) || strings.Contains(s, "REAL") {
		t.Errorf("GOCACHE/XDG_CACHE_HOME are not the private cache: %q", s)
	}
	// TMPDIR is private too.
	if res := run(`echo "$TMPDIR"`); !strings.Contains(fmtData(res), "ouija-sandbox-") {
		t.Errorf("TMPDIR is not the private temp dir: %q", fmtData(res))
	}
}

// The everyday commands work in the default sandbox: a profile missing a
// system service (macOS) or a system directory (Linux) shows up here, not as
// every user's bash failing.
func TestSandboxRunsEverydayCommands(t *testing.T) {
	project, _, run := sandboxFixture(t, DefaultSandbox())
	if err := os.WriteFile(filepath.Join(project, "a.txt"), []byte("hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmds := []string{
		"ls -la", "cat a.txt", "grep -n hello a.txt", "sed -n 1p a.txt", "find . -name a.txt",
		"date", "uname -a", "env | head -n 1", "whoami", "mkdir -p d && touch d/x && rm -r d",
		"echo x | sort | uniq | wc -l", "printf '%s' ok > out && cat out",
		"echo err > /dev/stderr", "echo in | cat /dev/stdin",
	}
	if _, err := exec.LookPath("bash"); err == nil {
		cmds = append(cmds, `bash -c 'cat <(echo substituted)'`) // opens /dev/fd/N
	}
	if _, err := exec.LookPath("git"); err == nil {
		cmds = append(cmds, "git --version", "git init -q repo && git -C repo status --short")
	}
	for _, c := range cmds {
		if res := run(c); res.Error != nil || strings.Contains(fmtData(res), "exit status") {
			t.Errorf("%q failed in the sandbox: %s", c, fmtData(res))
		}
	}
}

// git in the sandbox commits under the user's name and email, though it
// cannot read ~/.gitconfig.
func TestSandboxedGitKeepsTheAuthor(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	saved := gitIdentity
	gitIdentity = func() (string, string) { return "Ada Lovelace", "ada@example.com" }
	t.Cleanup(func() { gitIdentity = saved })
	_, _, run := sandboxFixture(t, DefaultSandbox())
	res := run("git init -q r && git -C r commit -q --allow-empty -m x && git -C r log --format='%an <%ae>'")
	if !strings.Contains(fmtData(res), "Ada Lovelace <ada@example.com>") {
		t.Fatalf("sandboxed commit: %s", fmtData(res))
	}
}

// "unsandboxed": true runs the command outside the sandbox only after the
// user approves that exact command — whatever the permission level or the
// allowlist — and never where the config forbids it or nobody can approve.
func TestUnsandboxedNeedsTheUsersApproval(t *testing.T) {
	if err := sandboxAvailable(); err != nil {
		t.Skipf("no sandbox here: %v", err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "data"), []byte("OUTSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := "cat " + filepath.Join(outside, "data")
	newEngine := func(sb *SandboxConfig, answer *bool) (*Engine, *[]string) {
		eng := newTestEngine(&mockProvider{}, t.TempDir())
		eng.tools.sandbox = sb
		perm := NewPermissionConfig(Allow) // bash would run unasked
		eng.SetPermissionConfig(perm)
		var asked []string
		if answer != nil {
			am := NewApprovalManager()
			am.SetCallback(func(_ context.Context, key string, _ map[string]any) (ApprovalResult, error) {
				asked = append(asked, key)
				return ApprovalResult{Allowed: *answer}, nil
			})
			eng.SetApprovalManager(am)
		}
		return eng, &asked
	}
	call := ToolCall{Name: "bash", Input: map[string]any{"command": cmd, "unsandboxed": true}}
	yes, no := true, false

	// Confined, the command cannot read the file.
	eng, _ := newEngine(DefaultSandbox(), &yes)
	if res := eng.runner.Run(context.Background(), "t", ToolCall{Name: "bash", Input: map[string]any{"command": cmd}}, nil, nil); strings.Contains(res.String(), "OUTSIDE") {
		t.Fatal("the sandbox did not confine the command")
	}
	// Approved: it runs outside, and the user was asked even under Allow.
	res := eng.runner.Run(context.Background(), "t", call, nil, nil)
	if !strings.Contains(res.String(), "OUTSIDE") {
		t.Fatalf("approved unsandboxed run: %s", res.String())
	}
	eng, asked := newEngine(DefaultSandbox(), &yes)
	eng.runner.Run(context.Background(), "t", call, nil, nil)
	if len(*asked) != 1 || !strings.Contains((*asked)[0], "unsandboxed") {
		t.Fatalf("asked = %q, want one unsandboxed prompt", *asked)
	}
	// Rejected, no approval UI, or forbidden by the config: it does not run.
	forbid := false
	for name, eng := range map[string]*Engine{
		"rejected": func() *Engine { e, _ := newEngine(DefaultSandbox(), &no); return e }(),
		"no UI":    func() *Engine { e, _ := newEngine(DefaultSandbox(), nil); return e }(),
		"forbidden": func() *Engine {
			e, _ := newEngine(&SandboxConfig{Enabled: true, AllowUnsandboxed: &forbid}, &yes)
			return e
		}(),
	} {
		if res := eng.runner.Run(context.Background(), "t", call, nil, nil); strings.Contains(res.String(), "OUTSIDE") {
			t.Errorf("%s: ran outside the sandbox: %s", name, res.String())
		}
	}
	// Straight to the tool, without the runner's approval: stays confined.
	tools := NewTools(t.TempDir())
	tools.sandbox = DefaultSandbox()
	if res := tools.Run(context.Background(), call); strings.Contains(res.String(), "OUTSIDE") {
		t.Fatal("the bash tool left the sandbox without an approval")
	}
}

// An ordinary approval cannot be the escape's: a command spelling the escape
// key ("unsandboxed\x00…") is refused before any prompt, and a deny level or
// an allow level's allowlist holds for unsandboxed calls too.
func TestUnsandboxedApprovalCannotBeBorrowed(t *testing.T) {
	if err := sandboxAvailable(); err != nil {
		t.Skipf("no sandbox here: %v", err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "data"), []byte("OUTSIDE"), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := "cat " + filepath.Join(outside, "data")
	eng := newTestEngine(&mockProvider{}, t.TempDir())
	eng.tools.sandbox = DefaultSandbox()
	perm := NewPermissionConfig(Ask)
	eng.SetPermissionConfig(perm)
	var asked []string
	am := NewApprovalManager()
	am.SetCallback(func(_ context.Context, key string, _ map[string]any) (ApprovalResult, error) {
		asked = append(asked, key)
		return ApprovalResult{Allowed: true, ForSession: true}, nil
	})
	eng.SetApprovalManager(am)
	run := func(in map[string]any) string {
		return eng.runner.Run(context.Background(), "t", ToolCall{Name: "bash", Input: in}, nil, nil).String()
	}
	if out := run(map[string]any{"command": "unsandboxed\x00" + cmd}); !strings.Contains(out, "NUL") {
		t.Fatalf("a command with a NUL byte was not refused: %s", out)
	}
	if len(asked) != 0 {
		t.Fatalf("a prompt was shown for it: %q", asked)
	}
	run(map[string]any{"command": cmd, "unsandboxed": true})
	if len(asked) != 1 {
		t.Fatalf("the unsandboxed run was not asked for on its own: %q", asked)
	}

	for name, perm := range map[string]*PermissionConfig{
		"deny":            NewPermissionConfig(Deny),
		"allow+allowlist": NewPermissionConfig(Allow),
	} {
		e := newTestEngine(&mockProvider{}, t.TempDir())
		e.tools.sandbox = DefaultSandbox()
		e.SetPermissionConfig(perm)
		e.SetApprovalManager(am)
		if name == "allow+allowlist" {
			e.allowlist.Set([]string{"echo"})
		}
		res := e.runner.Run(context.Background(), "t", ToolCall{Name: "bash", Input: map[string]any{"command": cmd, "unsandboxed": true}}, nil, nil)
		if strings.Contains(res.String(), "OUTSIDE") {
			t.Errorf("%s: ran outside the sandbox: %s", name, res.String())
		}
	}
}
