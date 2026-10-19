package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A bash command that plants or changes a git hook, or the repo config, says
// so in its result; one that leaves them alone adds nothing.
func TestGitGuard(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses /bin/sh")
	}
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git", "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "config"), []byte("[core]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(cmd string) string {
		res := bashTool(context.Background(), root, map[string]any{"command": cmd})
		return res.String()
	}

	if out := run("echo quiet"); strings.Contains(out, GitGuardMarker) {
		t.Fatalf("an innocent command was flagged: %q", out)
	}
	out := run(`printf '#!/bin/sh\necho pwn\n' > .git/hooks/pre-commit && chmod +x .git/hooks/pre-commit`)
	if !strings.Contains(out, GitGuardMarker) || !strings.Contains(out, filepath.Join(".git", "hooks", "pre-commit")) {
		t.Fatalf("a planted hook was not reported: %q", out)
	}
	out = run(`printf '[core]\n\tfsmonitor = sh -c x\n' > .git/config`)
	if !strings.Contains(out, filepath.Join(".git", "config")) {
		t.Fatalf("a changed git config was not reported: %q", out)
	}
	if out := run("chmod -x .git/hooks/pre-commit"); !strings.Contains(out, "pre-commit") {
		t.Fatalf("a mode change was not reported: %q", out)
	}

	// Not a git work tree: nothing to guard.
	if out := bashTool(context.Background(), t.TempDir(), map[string]any{"command": "echo hi"}).String(); strings.Contains(out, GitGuardMarker) {
		t.Fatalf("flagged outside a repo: %q", out)
	}
}

// A linked worktree's .git file points at the real git directory.
func TestGitDirOfWorktree(t *testing.T) {
	root, gitdir := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".git"), []byte("gitdir: "+gitdir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := gitDirOf(root); got != gitdir {
		t.Fatalf("gitDirOf = %q, want %q", got, gitdir)
	}
}
