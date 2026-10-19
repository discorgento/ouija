package engine

import (
	"os"
	"path/filepath"
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
		{".ssh_backup/id_rsa", false}, // substring, not component
		{"/project/.sshkeys", false},  // no trailing separator
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
