package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestBashSimpleCommand(t *testing.T) {
	root := t.TempDir()
	res := bashTool(context.Background(), root, map[string]any{
		"command": "echo hello",
	})
	if res.Error != nil {
		t.Fatalf("bash: %v", res.Error)
	}
	if !strings.Contains(res.Data, "hello") {
		t.Fatalf("output = %q, want hello", res.Data)
	}
}

func TestBashWorkingDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "marker.txt"), []byte("here"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := bashTool(context.Background(), root, map[string]any{
		"command": "ls marker.txt",
	})
	if res.Error != nil {
		t.Fatalf("bash: %v", res.Error)
	}
	if !strings.Contains(res.Data, "marker.txt") {
		t.Fatalf("output = %q, want marker.txt", res.Data)
	}
}

func TestBashNonZeroExit(t *testing.T) {
	root := t.TempDir()
	// Non-zero exit should return stderr, not a Go error.
	res := bashTool(context.Background(), root, map[string]any{
		"command": "echo errmsg >&2; exit 1",
	})
	if res.Error != nil {
		t.Fatalf("bash should not return error for non-zero exit: %v", res.Error)
	}
	if !strings.Contains(res.Data, "errmsg") {
		t.Fatalf("output = %q, want errmsg", res.Data)
	}
}

func TestBashEmptyCommand(t *testing.T) {
	root := t.TempDir()
	res := bashTool(context.Background(), root, map[string]any{
		"command": "",
	})
	if res.Error == nil {
		t.Fatal("expected error for empty command")
	}
}

func TestBashTimeout(t *testing.T) {
	root := t.TempDir()
	res := bashTool(context.Background(), root, map[string]any{
		"command": "sleep 10",
		"timeout": float64(1), // 1 second
	})
	if res.Error == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(res.Error.Error(), "timed out") {
		t.Fatalf("error = %q, want 'timed out'", res.Error.Error())
	}
}

func TestBashCapTimeout(t *testing.T) {
	root := t.TempDir()
	res := bashTool(context.Background(), root, map[string]any{
		"command": "sleep 0.1",
		"timeout": float64(999), // > 120s cap
	})
	// Command should still execute (capped to 120s but this is fast).
	if res.Error != nil {
		t.Fatalf("bash: %v", res.Error)
	}
}

func TestBashStderrCapture(t *testing.T) {
	root := t.TempDir()
	res := bashTool(context.Background(), root, map[string]any{
		"command": "echo stdout && echo stderr >&2",
	})
	if res.Error != nil {
		t.Fatalf("bash: %v", res.Error)
	}
	if !strings.Contains(res.Data, "stdout") || !strings.Contains(res.Data, "stderr") {
		t.Fatalf("output = %q, want both stdout and stderr", res.Data)
	}
}

func TestBashNoOutput(t *testing.T) {
	root := t.TempDir()
	res := bashTool(context.Background(), root, map[string]any{
		"command": "true",
	})
	if res.Error != nil {
		t.Fatalf("bash: %v", res.Error)
	}
	if res.Data != "(no output)" {
		t.Fatalf("output = %q, want '(no output)'", res.Data)
	}
}

func TestSanitizeEnvFiltersCredentials(t *testing.T) {
	t.Setenv("OUIJA_API_KEY", "sk-secret")
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIA123")
	t.Setenv("PATH", "/usr/bin:/bin")
	t.Setenv("HOME", "/home/user")
	t.Setenv("EDITOR", "vim")

	clean := sanitizeEnv()

	// Check blocked vars are filtered out.
	for _, kv := range clean {
		key := strings.SplitN(kv, "=", 2)[0]
		if key == "OUIJA_API_KEY" {
			t.Fatal("OUIJA_API_KEY should be filtered")
		}
		if key == "AWS_ACCESS_KEY_ID" {
			t.Fatal("AWS_ACCESS_KEY_ID should be filtered")
		}
	}

	// Check safe vars are preserved.
	hasPath := false
	hasHome := false
	hasEditor := false
	for _, kv := range clean {
		key := strings.SplitN(kv, "=", 2)[0]
		switch key {
		case "PATH":
			hasPath = true
		case "HOME":
			hasHome = true
		case "EDITOR":
			hasEditor = true
		}
	}
	if !hasPath {
		t.Fatal("PATH should be preserved")
	}
	if !hasHome {
		t.Fatal("HOME should be preserved")
	}
	if !hasEditor {
		t.Fatal("EDITOR should be preserved")
	}
}

func TestExtractBaseCommands(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want []string
	}{
		{"simple", "git status", []string{"git"}},
		{"chain &&", "git status && rm -rf /", []string{"git", "rm"}},
		{"chain ||", "false || echo ok", []string{"false", "echo"}},
		{"chain ;", "git status; rm -rf /", []string{"git", "rm"}},
		{"pipe", "git status | head -5", []string{"git", "head"}},
		{"newline", "git status\nrm -rf /", []string{"git", "rm"}},
		{"command subst $()", "echo $(rm -rf /)", []string{"echo", "rm"}},
		{"command subst backtick", "echo `ls -la`", []string{"echo", "ls"}},
		{"env var prefix", "OLLAMA_HOST=localhost ollama run", []string{"ollama"}},
		{"quoted separators", "git 'status && safe'", []string{"git"}},
		{"empty", "", nil},
		{"just whitespace", "   ", nil},
		{"eval with dangerous arg", "eval \"rm -rf /\"", []string{"eval"}},
		{"nested command subst", "echo $(cat $(ls))", []string{"echo", "cat", "ls"}},
		{"single command with args", "npm run build --prod", []string{"npm"}},
		{"multiple env vars", "VAR1=a VAR2=b make test", []string{"make"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractBaseCommands(tt.cmd)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractBaseCommands(%q) =\n  %v\n  want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestExtractCommands(t *testing.T) {
	tests := []struct {
		name string
		cmd  string
		want []string
	}{
		{"simple", "git status", []string{"git status"}},
		{"chain &&", "git status -s && rm -rf /tmp", []string{"git status -s", "rm -rf /tmp"}},
		{"chain ||", "false || echo ok", []string{"false", "echo ok"}},
		{"chain ;", "cd /tmp; rm -rf *", []string{"cd /tmp", "rm -rf *"}},
		{"pipe", "git status | head -5", []string{"git status", "head -5"}},
		{"command subst $()", "echo $(whoami)", []string{"echo $(whoami)", "whoami"}},
		{"env var prefix", "OLLAMA_HOST=localhost ollama run model", []string{"ollama run model"}},
		{"quoted separators", "git 'status && safe'", []string{"git 'status && safe'"}},
		{"empty", "", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractCommands(tt.cmd)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractCommands(%q) =\n  %v\n  want %v", tt.cmd, got, tt.want)
			}
		})
	}
}

func TestBashDenylist(t *testing.T) {
	denied := []string{
		"rm -rf /",
		"rm -rf /*",
		"rm -rf ~",
		"rm -rf $HOME",
		"dd if=/dev/zero of=/dev/sda",
		":(){ :|:& };:",
		"mkfs.ext4 /dev/sda",
	}
	for _, c := range denied {
		t.Run(c, func(t *testing.T) {
			if !IsDenied(c) {
				t.Errorf("IsDenied(%q) = false, want true", c)
			}
		})
	}

	allowed := []string{
		"rm -rf /tmp",
		"rm -rf ./build",
		"dd if=/dev/zero of=file.img count=1024",
		"echo ok",
	}
	for _, c := range allowed {
		t.Run(c, func(t *testing.T) {
			if IsDenied(c) {
				t.Errorf("IsDenied(%q) = true, want false", c)
			}
		})
	}
}

func TestBashPreview(t *testing.T) {
	preview := bashPreview(context.Background(), "/tmp", map[string]any{
		"command": "git status -s",
	})
	if !strings.Contains(preview, "git") {
		t.Fatalf("preview should contain command name, got %q", preview)
	}
	if !strings.Contains(preview, "git status -s") {
		t.Fatalf("preview should contain full command, got %q", preview)
	}
}

func TestBashPreviewEmpty(t *testing.T) {
	preview := bashPreview(context.Background(), "/tmp", map[string]any{
		"command": "",
	})
	if preview != "" {
		t.Fatalf("expected empty preview for empty command, got %q", preview)
	}
}

func TestBashPreviewTimeout(t *testing.T) {
	preview := bashPreview(context.Background(), "/tmp", map[string]any{
		"command": "sleep 30",
		"timeout": float64(60),
	})
	if !strings.Contains(preview, "60s") {
		t.Fatalf("preview should contain timeout, got %q", preview)
	}
}

func TestBashPreviewDenied(t *testing.T) {
	preview := bashPreview(context.Background(), "/tmp", map[string]any{
		"command": "rm -rf /",
	})
	if !strings.Contains(preview, "denylist") {
		t.Fatalf("preview should warn about denylist, got %q", preview)
	}
}

func TestBashPreviewTruncated(t *testing.T) {
	long := "echo " + strings.Repeat("a", 600)
	preview := bashPreview(context.Background(), "/tmp", map[string]any{
		"command": long,
	})
	if len(preview) > 500 {
		t.Fatalf("preview should be truncated to ~400+ chars, got %d chars: %s", len(preview), preview)
	}
	if !strings.Contains(preview, "…") {
		t.Fatalf("truncated preview should be marked with an ellipsis, got %q", preview)
	}
	// A 300-char command fits under the cap and must be shown in full: the
	// approval decision needs the payload, not a teaser.
	medium := "echo " + strings.Repeat("b", 294)
	if p := bashPreview(context.Background(), "/tmp", map[string]any{"command": medium}); p != "will run: "+medium+"  [echo]" {
		t.Fatalf("medium command should not be truncated, got %q", p)
	}
}

// The static denylist only knows exact flag orders; the semantic layer must
// cover the variants a model actually writes.
func TestBashDenylistSemantic(t *testing.T) {
	denied := []string{
		"rm -fr /",
		"sudo rm -fr /",
		"xargs -n1 rm -fr /",
		"sudo -u root rm -fr /",
		"env VAR=1 rm -rf /",
		"nohup rm -fr / &",
		"rm -r -f $HOME",
		"rm --recursive --force /",
		"bash -c 'rm -rf /'",
		"sh -c \"rm -fr ~\"",
		"eval 'rm -fr /'",
		"mkfs /dev/sda",
		"mkfs.xfs /dev/sdb",
		"dd of=/dev/nvme0n1 bs=1M",
		"fdisk /dev/sdb",
		"parted /dev/sda",
		"wipefs /dev/sda",
		"chmod -R 777 /",
		"chown -R root /",
		":(){:|:&};:",
	}
	for _, c := range denied {
		t.Run(c, func(t *testing.T) {
			if !IsDenied(c) {
				t.Errorf("IsDenied(%q) = false, want true", c)
			}
		})
	}

	allowed := []string{
		"rm -rf /tmp",
		"rm -rf ./build",
		"rm build/*.o",
		"timeout 30 rm -rf /tmp", // wrappers don't blanket-block, only targets do
		"sudo apt install ripgrep",
		"dd if=/dev/zero of=file.img count=1024",
		"fdisk -l",
		"chmod -R 755 src",
		"chown app:app ./data",
		"timeout 30 ls",
		"echo ok",
	}
	for _, c := range allowed {
		t.Run(c, func(t *testing.T) {
			if IsDenied(c) {
				t.Errorf("IsDenied(%q) = true, want false", c)
			}
		})
	}
}

// bash has no `path` argument for the permission gate to inspect — the command
// itself must not reach the credential files read_file refuses.
func TestBashProtectedPathRefs(t *testing.T) {
	denied := []string{
		"cat .env",
		"cat ~/.ssh/id_rsa",
		"cat /etc/passwd",
		"cp .env /tmp/x",
		"grep -rn password ~/.aws/credentials",
		"cat $(echo .env)",
		"docker run -v /etc/localtime:/etc/localtime alpine",
	}
	for _, c := range denied {
		t.Run("denied/"+c, func(t *testing.T) {
			if ref, found := commandRefsProtectedPath(c); !found {
				t.Errorf("commandRefsProtectedPath(%q) = _, false; want protected ref (%q)", c, ref)
			}
		})
	}

	allowed := []string{
		"cat README.md",
		"git status",
		"ls -la",
		"npm run build",
		"go test ./...",
		"find . -name '*.go'",
	}
	for _, c := range allowed {
		t.Run("allowed/"+c, func(t *testing.T) {
			if ref, found := commandRefsProtectedPath(c); found {
				t.Errorf("commandRefsProtectedPath(%q) = %q, true; want false", c, ref)
			}
		})
	}
}

func TestBashAllowlistPatterns(t *testing.T) {
	w := NewBashAllowlist([]string{"git", "npm run build", "cat"})
	tests := []struct {
		name string
		cmd  string
		want bool
	}{
		{"git status allowed", "git status", true},
		{"git push allowed", "git push", true}, // blanket "git" allows all
		{"npm run build allowed", "npm run build", true},
		{"npm run build --prod allowed", "npm run build --prod", true},
		{"npm run dev denied", "npm run dev", false},
		{"npm denied (no match)", "npm", false},
		{"cat file allowed", "cat /etc/passwd", true},
		{"rm denied", "rm -rf /", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := w.IsAllowed(tt.cmd)
			if got != tt.want {
				t.Errorf("IsAllowed(%q) = %v, want %v", tt.cmd, got, tt.want)
			}
		})
	}

	// Empty allowlist → all allowed.
	empty := NewBashAllowlist(nil)
	if !empty.IsAllowed("rm -rf /") {
		t.Error("empty allowlist should allow everything")
	}
	if empty.Restricts() {
		t.Error("empty allowlist must not report itself as restricting")
	}
	if !NewBashAllowlist([]string{"git"}).Restricts() {
		t.Error("non-empty allowlist must report itself as restricting")
	}
}

// A multi-word pattern is an exact action: it may be extended with further
// flags, never with new operands (which would be a different action).
func TestBashAllowlistOperandExtension(t *testing.T) {
	w := NewBashAllowlist([]string{"npm run build"})
	tests := []struct {
		cmd  string
		want bool
	}{
		{"npm run build", true},
		{"npm run build --prod", true},
		{"npm run build ./dist", false},
		{"npm run build -- ./dist", true}, // explicit flag/operand separator
		{"npm run build-clean", false},
	}
	for _, tt := range tests {
		if got := w.IsAllowed(tt.cmd); got != tt.want {
			t.Errorf("IsAllowed(%q) = %v, want %v", tt.cmd, got, tt.want)
		}
	}
}

func TestBashEnvSanitizationApplied(t *testing.T) {
	root := t.TempDir()
	t.Setenv("OUIJA_TEST_SECRET", "supersecret")

	res := bashTool(context.Background(), root, map[string]any{
		"command": "echo $OUIJA_TEST_SECRET",
	})
	if res.Error != nil {
		t.Fatalf("bash: %v", res.Error)
	}
	if strings.Contains(res.Data, "supersecret") {
		t.Fatal("secret env var leaked to bash subprocess")
	}
}

// Quoting is a free way around a lexical scan: the shell removes the quotes,
// so the scan must judge each word by its value, not its spelling.
func TestBashProtectedPathRefsSurviveQuoting(t *testing.T) {
	denied := []string{
		`cat .e""nv`,
		`cat '.e'nv`,
		`cat .e\nv`,
		`cat ~/.s"s"h/id_rsa`,
		`cat "/e"tc/passwd`,
		`cat < .e''nv`,
		`cat $(echo .e""nv)`,
		`cat $'\x2eenv'`,
	}
	for _, c := range denied {
		t.Run(c, func(t *testing.T) {
			if _, found := commandRefsProtectedPath(c); !found {
				t.Errorf("commandRefsProtectedPath(%q) = false; want the quoted protected path refused", c)
			}
		})
	}
	allowed := []string{
		`echo "hello world"`,
		`git commit -m 'fix: handle env'`,
		`grep -rn "TODO" .`,
		`printf '%s\n' a b`,
	}
	for _, c := range allowed {
		t.Run(c, func(t *testing.T) {
			if ref, found := commandRefsProtectedPath(c); found {
				t.Errorf("commandRefsProtectedPath(%q) = %q; want allowed", c, ref)
			}
		})
	}
}

// A symlink in the project must not let bash reach a protected file that the
// file tools refuse.
func TestBashProtectedPathRefsThroughSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	for rel, content := range map[string]string{".env": "SECRET=1", ".ouija/config.json": "{}", "README.md": "hi"} {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for link, target := range map[string]string{"notes.txt": ".env", "cfg": ".ouija", "readme": "README.md"} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Fatal(err)
		}
	}

	for _, c := range []string{"cat notes.txt", "cat ./notes.txt", "cat cfg/config.json", "tar czf /tmp/x.tgz cfg"} {
		if _, found := commandRefsProtectedPathIn(root, c); !found {
			t.Errorf("%q reached a protected file through a symlink", c)
		}
	}
	for _, c := range []string{"cat readme", "cat README.md", "ls -la", "git status"} {
		if ref, found := commandRefsProtectedPathIn(root, c); found {
			t.Errorf("%q refused (%q); want allowed", c, ref)
		}
	}

	// End to end: the ToolRunner gate refuses it even under Allow.
	eng := newTestEngine(&mockProvider{}, root)
	eng.SetPermissionConfig(NewPermissionConfig(Allow))
	res := eng.runner.Run(context.Background(), "t", ToolCall{ID: "1", Name: "bash", Input: map[string]any{"command": "cat notes.txt"}}, nil, nil)
	if res.Error == nil || strings.Contains(fmt.Sprint(res.Data), "SECRET") {
		t.Fatalf("bash read .env through a symlink: %+v", res)
	}
}
