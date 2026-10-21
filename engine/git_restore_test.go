package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A confined command cannot leave behind what git runs on its own: a hook or
// a core.fsmonitor planted through a path built at run time is put back after
// the command, while ordinary changes (a remote, user.name) stay.
func TestConfinedCommandCannotPlantGitCode(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	project, _, run := sandboxFixture(t, DefaultSandbox())
	if out, err := exec.Command("git", "-C", project, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	own := filepath.Join(project, ".git", "hooks", "pre-push")
	if err := os.WriteFile(own, []byte("#!/bin/sh\necho mine\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res := run(`g=.gi; g=${g}t; h=hoo; h=${h}ks; printf '#!/bin/sh\ntouch /tmp/pwned\n' > $g/$h/pre-commit; chmod +x $g/$h/pre-commit; ` +
		`printf 'evil\n' > $g/$h/pre-push; ` +
		`c=conf; c=${c}ig; git config -f $g/$c core.fsmonitor 'touch /tmp/pwned'; git config -f $g/$c alias.st '!sh -c x'; ` +
		`git remote add origin https://example.com/r.git; git config user.name Ada`)
	if _, err := os.Stat(filepath.Join(project, ".git", "hooks", "pre-commit")); err == nil {
		t.Error("a planted hook survived the command")
	}
	if b, _ := os.ReadFile(own); string(b) != "#!/bin/sh\necho mine\n" {
		t.Errorf("the user's own hook was not put back: %q", b)
	}
	cfg, _ := os.ReadFile(filepath.Join(project, ".git", "config"))
	if strings.Contains(string(cfg), "fsmonitor") || strings.Contains(string(cfg), "sh -c x") {
		t.Errorf("a planted config entry survived:\n%s", cfg)
	}
	if !strings.Contains(fmtData(res), "ouija put back") {
		t.Errorf("the result does not say what was put back: %s", fmtData(res))
	}
	// The planted entries went with the whole config edit, ordinary ones in
	// the same command included: the config is put back as it was.
	res = run(`git remote add origin https://example.com/r.git && git config user.name Ada && git init -q sub`)
	cfg, _ = os.ReadFile(filepath.Join(project, ".git", "config"))
	if !strings.Contains(string(cfg), "example.com/r.git") || !strings.Contains(string(cfg), "Ada") {
		t.Errorf("ordinary config changes were undone:\n%s\n%s", cfg, fmtData(res))
	}
}

// A repository the confined command creates keeps everything but what runs.
func TestStripRunnable(t *testing.T) {
	in := "[core]\n\trepositoryformatversion = 0\n\tfsmonitor = touch x\n[alias]\n\tst = status\n\tx = !sh\n[filter \"lfs\"]\n\tclean = evil\n[user]\n\tname = Ada\n"
	got := string(stripRunnable([]byte(in)))
	for _, gone := range []string{"fsmonitor", "!sh", "clean = evil"} {
		if strings.Contains(got, gone) {
			t.Errorf("%q kept:\n%s", gone, got)
		}
	}
	for _, kept := range []string{"repositoryformatversion", "st = status", "name = Ada"} {
		if !strings.Contains(got, kept) {
			t.Errorf("%q dropped:\n%s", kept, got)
		}
	}
	if added := newRunnableEntries([]byte(in), []byte(in)); len(added) != 0 {
		t.Errorf("an unchanged config reported %v", added)
	}
	if added := newRunnableEntries(nil, []byte("[CORE]\n  HooksPath=/x\n[includeIf \"gitdir:/\"]\n path=/y\n")); len(added) != 2 {
		t.Errorf("case and includeIf not caught: %v", added)
	}
}
