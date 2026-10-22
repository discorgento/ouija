package engine

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestLevelString(t *testing.T) {
	tests := []struct {
		l    Level
		want string
	}{
		{Deny, "deny"},
		{Ask, "ask"},
		{Allow, "allow"},
		{Level(99), "deny"},
	}
	for _, tc := range tests {
		if got := tc.l.String(); got != tc.want {
			t.Errorf("Level(%d).String() = %q, want %q", tc.l, got, tc.want)
		}
	}
}

func TestParseLevel(t *testing.T) {
	tests := []struct {
		s    string
		want Level
	}{
		{"allow", Allow},
		{"Allow", Allow},
		{"ALLOW", Allow},
		{"ask", Ask},
		{"Ask", Ask},
		{"deny", Deny},
		{"Deny", Deny},
		{"unknown", Ask},
		{"", Ask},
	}
	for _, tc := range tests {
		if got := ParseLevel(tc.s); got != tc.want {
			t.Errorf("ParseLevel(%q) = %v, want %v", tc.s, got, tc.want)
		}
	}
}

func TestPermissionConfigCheck(t *testing.T) {
	skipOnWindows(t, "POSIX paths")
	cfg := NewPermissionConfig(Ask)
	cfg.Tools["read_file"] = Allow
	cfg.Tools["delete_all"] = Deny

	tests := []struct {
		name    string
		tool    string
		absPath string
		want    Level
	}{
		{"explicit allow", "read_file", "", Allow},
		{"explicit deny", "delete_all", "", Deny},
		{"default ask", "write_file", "", Ask},
		{"default ask unknown", "unknown_tool", "", Ask},
		{"protected path", "read_file", "/etc/passwd", Deny},
		{"protected dotenv", "read_file", "/project/.env", Deny},
		{"protected dotenv variant", "read_file", "/project/.env.staging", Deny},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := cfg.Check(tc.tool, tc.absPath); got != tc.want {
				t.Errorf("Check(%q, %q) = %v, want %v", tc.tool, tc.absPath, got, tc.want)
			}
		})
	}
}

func TestIsProtectedPath(t *testing.T) {
	// The token files outside dot directories count only in the home
	// directory: the table's home is /home/u.
	t.Setenv("HOME", "/home/u")
	t.Setenv("USERPROFILE", "/home/u")
	skipOnWindows(t, "POSIX paths")
	tests := []struct {
		path string
		want bool
	}{
		// System directories.
		{"/etc/passwd", true},
		{"/etc/ssl/certs/ca-certificates.crt", true},
		{"/var/lib/something", true},

		// Protected filenames regardless of location.
		{"/project/.env", true},
		{"/project/.env.local", true},
		{"/project/.env.secret", true},
		// The `.env` rule is a prefix, not a fixed list: dotenv variants not
		// in any enumeration must be protected too.
		{"/project/.env.production", true},
		{"/project/.env.staging", true},
		{"/project/.envrc", true},
		{"/project/.env.example", true},
		{"/project/.netrc", true},
		{"/project/.gitconfig", true},

		// Credentials the audit found readable over MCP resources/read.
		{"/home/u/.git-credentials", true},
		{"/home/u/.config/gh/hosts.yml", true},
		{"/home/u/.pgpass", true},
		{"/home/u/.pypirc", true},
		{"/home/u/.my.cnf", true},
		{"/project/.git/config", true},
		{"/project/.git/hooks/pre-commit", true},
		{"/project/.git/HEAD", false},
		{"/project/backup/id_ed25519", true},
		{"/project/infra/terraform.tfstate", true},
		{"/project/infra/terraform.tfstate.backup", true},
		{"/proc/self/environ", true},
		{"/home/u/.cargo/credentials.toml", true},
		{"/home/u/.m2/settings.xml", true},
		{"/home/u/.gradle/gradle.properties", true},
		{"/home/u/.cache/huggingface/token", true},
		{"/home/u/.local/share/uv/credentials/x.json", true},
		{"/home/u/.cargo/registry/index/x", false},
		{"/home/u/.nvm/versions/node/v22/etc/npmrc", true},
		{"/home/u/.config/git/credentials", true},
		{"/home/u/.terraform.d/credentials.tfrc.json", true},
		{"/home/u/.vault-token", true},
		{"/home/u/.config/rclone/rclone.conf", true},
		{"/home/u/.password-store/x.gpg", true},
		{"/home/u/.local/share/keyrings/login.keyring", true},
		{"/home/u/.config/composer/auth.json", true},
		{"/home/u/.composer/auth.json", true},
		{"/home/u/.config/pip/pip.conf", true},
		{"/home/u/.gem/credentials", true},
		{"/home/u/.config/containers/auth.json", true},
		{"/project/.yarnrc.yml", true},
		{"/home/u/.s3cfg", true},
		{"/home/u/.terraformrc", true},
		{"/home/u/.config/hub", true},
		{"/home/u/.config/glab-cli/config.yml", true},
		{"/home/u/.mozilla/firefox/x/cookies.sqlite", true},
		{"/home/u/.config/google-chrome/Default/Cookies", true},
		{"/home/u/.local/share/kwalletd/kdewallet.kwl", true},
		// The plain pairs are ordinary paths in a project.
		{"/project/internal/git/credentials/store.go", false},
		{"/project/debian/usr/share/keyrings/archive.gpg", false},
		{"/project/src/uv/credentials/x.py", false},
		{"/project/etc/npmrc", false},
		{"/sys/kernel/notes", true},

		// Protected directory names at ANY path level (absolute).
		{"/workspace/.ssh/id_rsa", true},
		{"/workspace/.gnupg/pubring.gpg", true},
		{"/workspace/.aws/credentials", true},
		{"/workspace/.config/gcloud/creds.json", true},
		{"/project/.ouija/config.json", true},
		{"/project/.ouija/history.jsonl", true},

		// Protected directory names at ANY path level (relative).
		// These would bypass the old matcher which only checked "/.ssh/".
		{".ssh/id_rsa", true},
		{".gnupg/pubring.gpg", true},
		{".aws/credentials", true},
		{".config/gcloud/creds.json", true},
		{".ouija/config.json", true},
		{".ouija/history.jsonl", true},

		// Single-component path (no separator).
		{".ssh", true},
		{".ouija", true},

		// Safe paths (should NOT match).
		{"/project/composer.json", false},
		{"/project/src/main.go", false},
		{"/tmp/random-file", false},
		// The `.env` prefix must not swallow lookalikes that are not dotenv
		// files: ".dotenv" is a different first component (".dot").
		{"/project/.dotenv", false},
		{"/project/Documents/notes.txt", false},
		{".ssh_backup/notes.txt", false}, // substring, not component
		{"/project/.sshkeys", false},     // no trailing separator
		{"/project/.github/config.yml", false},
		{"/project/src/gh/main.go", false},
		{"/project/procedures.md", false},
		{"notprotected/file.txt", false},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := isProtectedPath(tc.path)
			if got != tc.want {
				t.Errorf("isProtectedPath(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

// ouija's own secrets and permission grants are protected: the global config
// (every provider's API key) and the project config files the model could
// otherwise rewrite to grant itself permissions.
func TestOuijaConfigsAreProtected(t *testing.T) {
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home) // Linux
	t.Setenv("HOME", home)            // macOS: ~/Library/Application Support
	t.Setenv("AppData", home)         // Windows
	cfgHome, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{
		filepath.FromSlash("/home/u/.config/ouija/config.json"),
		filepath.Join(cfgHome, "ouija", "config.json"),
		filepath.Join(cfgHome, "ouija"),
		filepath.FromSlash("/proj/.ouija.json"),
		filepath.FromSlash("/proj/ouija.json"),
		filepath.FromSlash("/proj/.ouija/config.json"),
	} {
		if !isProtectedPath(p) {
			t.Errorf("isProtectedPath(%q) = false; want protected", p)
		}
	}
	for _, p := range []string{"/proj/ouija/main.go", "/proj/docs/ouija.md", filepath.Join(cfgHome, "other", "x.json")} {
		if isProtectedPath(p) {
			t.Errorf("isProtectedPath(%q) = true; want allowed", p)
		}
	}
	if _, found := commandRefsProtectedPath("cat ~/.config/ouija/config.json"); !found {
		t.Error("bash `cat ~/.config/ouija/config.json` was not refused")
	}
}

// SaveConfigAt must not copy the resolved API key (from the global store)
// into a project config, which is often committed.
func TestSaveConfigAtDoesNotLeakResolvedKey(t *testing.T) {
	cfg := DefaultConfig()
	cfg.APIKey = "sk-global-secret"
	eng := New(EngineConfig{Provider: &mockProvider{}, Config: cfg, Tracer: NewTracer(false)})
	dir := t.TempDir()

	fresh := filepath.Join(dir, "fresh", "config.json")
	if err := eng.SaveConfigAt(fresh); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fresh); strings.Contains(string(b), "sk-global-secret") {
		t.Fatal("the global key was written into a new project config")
	}

	// A key the project file itself declares is kept as it is.
	own := filepath.Join(dir, "own.json")
	if err := os.WriteFile(own, []byte(`{"apiKey":"sk-project-own"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveConfigAt(own); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(own)
	if strings.Contains(string(b), "sk-global-secret") || !strings.Contains(string(b), "sk-project-own") {
		t.Fatalf("project key not preserved / global key leaked: %s", b)
	}
	if eng.Config().APIKey != "sk-global-secret" {
		t.Fatal("SaveConfigAt changed the in-memory key")
	}
}

// A project key belongs to the provider the file names: saving after a switch
// to another provider drops it, or the next start would send it there.
func TestSaveConfigAtDropsKeyOfOtherProvider(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Provider = "anthropic"
	eng := New(EngineConfig{Provider: &mockProvider{}, Config: cfg, Tracer: NewTracer(false)})
	path := filepath.Join(t.TempDir(), "config.json")
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	write(`{"provider":"openrouter","apiKey":"sk-or-client"}`)
	if err := eng.SaveConfigAt(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); strings.Contains(string(b), "sk-or-client") {
		t.Fatalf("openrouter key kept for anthropic: %s", b)
	}

	write(`{"provider":"anthropic","apiKey":"sk-ant-client"}`)
	if err := eng.SaveConfigAt(path); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "sk-ant-client") {
		t.Fatalf("same-provider key dropped: %s", b)
	}
}

// On a case-insensitive filesystem every spelling of a protected name is
// protected.
func TestProtectedPathFoldsCase(t *testing.T) {
	old := foldCase
	foldCase = true
	t.Cleanup(func() { foldCase = old })
	for _, p := range []string{"/p/.ENV", "/p/.Env.local", "/home/u/.SSH/id_rsa", "/p/.OUIJA/config.json", "/home/u/.Config/Ouija/config.json"} {
		if !isProtectedPath(p) {
			t.Errorf("%s not protected with case folding", p)
		}
	}
	if isProtectedPath("/p/README.md") {
		t.Error("README.md protected")
	}
}

// Folding is on for every OS: Linux has case-insensitive directories too
// (ext4 casefold, vfat, exFAT), where `.ENV` opens `.env`.
func TestProtectedPathFoldsCaseByDefault(t *testing.T) {
	if !foldCase {
		t.Fatal("case folding is off")
	}
	for _, p := range []string{"/p/.ENV", "/p/APP/ETC/env.php", "/p/server.KEY", "/home/u/.SSH/id_rsa"} {
		if !isProtectedPath(p) {
			t.Errorf("%s not protected", p)
		}
	}
}

// Secrets projects keep in their own tree: read tools are allowed without
// approval, so anything left off the list goes to the provider.
func TestProjectSecretsAreProtected(t *testing.T) {
	for _, p := range []string{
		"/p/app/etc/env.php", "/p/wp-config.php", "/p/blog/wp-config.php",
		"/p/config/database.yml", "/p/config/master.key", "/p/config/credentials/production.key",
		"/p/config/credentials.yml.enc", "/p/auth.json", "/p/sub/auth.json",
		"/p/certs/server.pem", "/p/tls.key", "/p/sign.p12", "/p/sign.pfx",
		"/p/android/release.jks", "/p/android/debug.keystore",
		"/p/credentials.json", "/p/deploy-sa.json", "/p/service-account.json",
		"/p/infra/terraform.tfvars", "/p/infra/prod.auto.tfvars", "/p/.htpasswd",
		"/p/.yarnrc", "/p/.bundle/config", "/p/.dev.vars", "/p/local.settings.json",
		"/p/kubeconfig", "/p/.kube/config", "/p/.docker/config.json",
		"/p/config/secrets.yml", "/p/secrets.yaml",
	} {
		if !isProtectedPath(p) {
			t.Errorf("%s not protected", p)
		}
	}
	// Ordinary source next to them stays readable.
	for _, p := range []string{
		"/p/package.json", "/p/composer.json", "/p/tsconfig.json", "/p/app/etc/config.php",
		"/p/app/etc/di.xml", "/p/config/routes.rb", "/p/config/application.yml",
		"/p/src/keys.go", "/p/src/monkey.ts", "/p/docs/key.md", "/p/infra/variables.tf",
		"/p/src/auth.ts", "/p/README.md",
		".key", "/p/.key", // a jq filter in a bash command, not a key file
	} {
		if isProtectedPath(p) {
			t.Errorf("%s protected", p)
		}
	}
}

// Every git config and hook directory runs code on the next git command once
// written, not only the top-level .git/config and .git/hooks.
func TestGitConfigsAndHooksAreProtected(t *testing.T) {
	for _, p := range []string{
		"/p/.git/config", "/p/.git/hooks", "/p/.git/hooks/pre-commit", "/p/.git/config.worktree",
		"/p/.git/modules/lib/config", "/p/.git/modules/lib/hooks/post-checkout",
		"/p/.git/modules/a/modules/b/config", "/p/.git/worktrees/wt/config.worktree",
		"/p/vendor/lib/.git/config", "/p/vendor/lib/.git/hooks/pre-push", ".git/config",
	} {
		if !isProtectedPath(p) {
			t.Errorf("%s not protected", p)
		}
	}
	for _, p := range []string{"/p/.git/HEAD", "/p/.git/modules/lib/HEAD", "/p/src/config", "/p/hooks/useAuth.ts", "/p/.github/hooks/x"} {
		if isProtectedPath(p) {
			t.Errorf("%s protected", p)
		}
	}
}

// The running sandbox is the global floor merged in; saving must not write it
// into the project file, where (trusted) it outlived the global setting.
func TestSaveConfigAtKeepsTheProjectsOwnSandbox(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Tools = &ToolsConfig{Bash: &BashConfig{Allowlist: []string{"go"}, Sandbox: &SandboxConfig{
		Enabled: true, Read: []string{"~/go"}, Network: &SandboxNetwork{All: true},
	}}}
	eng := New(EngineConfig{Provider: &mockProvider{}, Config: cfg, Tracer: NewTracer(false)})
	dir := t.TempDir()

	fresh := filepath.Join(dir, "fresh.json")
	if err := eng.SaveConfigAt(fresh); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(fresh); strings.Contains(string(b), "sandbox") || !strings.Contains(string(b), `"go"`) {
		t.Fatalf("a new project file got the running sandbox, or lost the allowlist: %s", b)
	}

	own := filepath.Join(dir, "own.json")
	if err := os.WriteFile(own, []byte(`{"tools":{"bash":{"sandbox":{"enabled":true,"write":["build"]}}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := eng.SaveConfigAt(own); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(own)
	if !strings.Contains(string(b), `"build"`) || strings.Contains(string(b), `"all"`) || strings.Contains(string(b), "~/go") {
		t.Fatalf("the project's own sandbox was not kept as it was: %s", b)
	}
	if sb := eng.Config().Tools.Bash.Sandbox; sb == nil || sb.Network == nil || !sb.Network.All {
		t.Fatal("SaveConfigAt changed the running sandbox")
	}
}

// With XDG_CONFIG_HOME a symlink, the real path of ouija's own config is
// protected too: a project symlink reaches it by that path (#26).
func TestOuijaConfigBehindASymlinkedConfigDir(t *testing.T) {
	skipOnWindows(t, "XDG_CONFIG_HOME")
	if runtime.GOOS != "linux" {
		t.Skip("os.UserConfigDir reads XDG_CONFIG_HOME on Linux")
	}
	real := filepath.Join(t.TempDir(), "real-config")
	if err := os.MkdirAll(filepath.Join(real, "ouija"), 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(t.TempDir(), "cfg")
	if err := os.Symlink(real, link); err != nil {
		t.Skip("no symlinks here:", err)
	}
	t.Setenv("XDG_CONFIG_HOME", link)
	if !isProtectedPath(filepath.Join(real, "ouija", "config.json")) {
		t.Fatal("the real path of ouija's config is not protected")
	}
}
