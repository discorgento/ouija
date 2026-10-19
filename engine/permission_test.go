package engine

import (
	"path/filepath"
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
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	for _, p := range []string{
		"/home/u/.config/ouija/config.json",
		filepath.Join(cfgHome, "ouija", "config.json"),
		filepath.Join(cfgHome, "ouija"),
		"/proj/.ouija.json",
		"/proj/ouija.json",
		"/proj/.ouija/config.json",
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
