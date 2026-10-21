//go:build darwin

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The macOS profile names paths only through parameters, so a path with a
// quote or a paren cannot rewrite the profile.
func TestSeatbeltProfileKeepsPathsOutOfTheText(t *testing.T) {
	odd := filepath.Join(t.TempDir(), `a") (allow default) ("`)
	if err := os.MkdirAll(odd, 0o700); err != nil {
		t.Fatal(err)
	}
	profile, params := seatbeltProfile(sandboxPolicy{WriteDirs: []string{odd}})
	if strings.Contains(profile, "allow default") {
		t.Fatalf("a path reached the profile text:\n%s", profile)
	}
	if len(params) == 0 || params[0] != odd {
		t.Fatalf("params = %q, want the odd path first", params)
	}
}

// open and osascript start programs outside the sandbox; the project's git
// hooks and config run code the next time the user runs git. All refused.
func TestSeatbeltRefusesEscapes(t *testing.T) {
	project, _, run := sandboxFixture(t, DefaultSandbox())
	if err := os.MkdirAll(filepath.Join(project, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, ".git", "config"), []byte("[core]\n\tbare = false\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []string{
		"osascript -e 'return 42'",
		`osascript -e 'tell application "Finder" to get name'`,
		"open -g -a TextEdit",
		`printf '#!/bin/sh\n' > .git/hooks/pre-commit`,
		`printf '[core]\n' > .git/config`,
		`mv .git g && printf '#!/bin/sh\n' > g/hooks/pre-commit; mv g .git`,
	} {
		if res := run(c + " && echo ESCAPED"); strings.Contains(fmtData(res), "ESCAPED") || strings.Contains(fmtData(res), "42") {
			t.Errorf("%q ran in the sandbox: %s", c, fmtData(res))
		}
	}
	if _, err := os.Stat(filepath.Join(project, ".git", "hooks", "pre-commit")); err == nil {
		t.Error("a git hook was written")
	}
	// A hard link is the config under another name, past the path-based
	// deny; what the command left is put back after it all the same.
	run(`ln .git/config x && printf '[core]\n\tfsmonitor = touch /tmp/pwned\n' >> x`)
	if b, _ := os.ReadFile(filepath.Join(project, ".git", "config")); strings.Contains(string(b), "fsmonitor") {
		t.Errorf("a hard link wrote the git config:\n%s", b)
	}
	profile, _ := seatbeltProfile(sandboxPolicy{NetAll: true})
	for _, banned := range []string{"launchservicesd", "lsd.mapdb", "cfprefsd", "(allow network*)"} {
		if strings.Contains(profile, banned) {
			t.Errorf("the profile grants %s", banned)
		}
	}
}
