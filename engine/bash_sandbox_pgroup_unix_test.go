//go:build unix

package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A background job dies with the command: it cannot act after the command
// returned (and after the git state was put back). One that held the output
// open ends the command after WaitDelay, with the shell's status.
func TestBashKillsBackgroundJobs(t *testing.T) {
	root := t.TempDir()
	for i, cmd := range []string{
		"(sleep 2; touch late0) >/dev/null 2>&1 & echo started",
		"(sleep 4; touch late1) & echo started", // past the 3s WaitDelay
		"sh -c 'sleep 2; touch late2' >/dev/null 2>&1 & exit 3",
	} {
		res := bashTool(context.Background(), root, map[string]any{"command": cmd})
		if !strings.Contains(res.String(), []string{"started", "started", "exit status 3"}[i]) {
			t.Errorf("%q: %s", cmd, res.String())
		}
	}
	time.Sleep(3 * time.Second)
	for _, f := range []string{"late0", "late1", "late2"} {
		if _, err := os.Stat(filepath.Join(root, f)); err == nil {
			t.Errorf("a background job outlived its command and wrote %s", f)
		}
	}
}

// The sandboxed case of the same: a delayed hook write is killed before it
// lands, so the hook cannot appear after ouija put the git state back.
func TestSandboxedBackgroundJobCannotPlantAHookLater(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	project, _, run := sandboxFixture(t, DefaultSandbox())
	if out, err := exec.Command("git", "-C", project, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	run(`(sleep 1; printf '#!/bin/sh\ntouch pwned\n' > .git/hooks/pre-commit; chmod +x .git/hooks/pre-commit) >/dev/null 2>&1 &`)
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(project, ".git", "hooks", "pre-commit")); err == nil {
		t.Fatal("a background job planted a hook after the command returned")
	}
}

// A job that leaves the command's process group (setsid, a double fork)
// cannot outlive a confined command either: on Linux the sandbox runs the
// command in a PID namespace of its own, which ends with it.
func TestSandboxedSetsidJobDiesWithTheCommand(t *testing.T) {
	if _, err := exec.LookPath("setsid"); err != nil {
		t.Skip("setsid not found")
	}
	if !pidNamespaceAvailable() {
		t.Skip("no unprivileged PID namespaces here: the process group is all there is")
	}
	project, _, run := sandboxFixture(t, DefaultSandbox())
	run(`setsid sh -c 'sleep 1; touch late-setsid' >/dev/null 2>&1 < /dev/null & ` +
		`(sh -c '(sleep 1; touch late-fork) &' &) >/dev/null 2>&1; sleep 0.3; echo started`) // the pause lets setsid run before the group is killed
	time.Sleep(2500 * time.Millisecond)
	for _, f := range []string{"late-setsid", "late-fork"} {
		if _, err := os.Stat(filepath.Join(project, f)); err == nil {
			t.Errorf("a job that left the process group outlived its command and wrote %s", f)
		}
	}
}
