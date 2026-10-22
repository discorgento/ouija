package engine

import (
	"bytes"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Undoing what a confined command planted for git to run. On Linux the
// sandbox must leave the project writable, .git included (Landlock cannot
// carve a read-only subtree out of a writable one), so a confined command
// could write a hook or a core.fsmonitor that runs, unconfined, the next time
// the user runs git. After every confined run ouija puts back what git would
// execute, or read commands from, on its own: the hooks as they were, the
// config entries that run a command (in config, config.worktree and each
// submodule's and linked worktree's config), the files that send git to
// another directory (commondir, a .git pointer) and info/attributes.
// Ordinary changes (git init, git remote add, user.name) stay.

// gitState is what git executes on its own, captured before a command.
type gitState struct {
	gitDir    string
	commonDir string             // a linked worktree's main git directory, "" otherwise
	dotGit    gitFile            // root/.git itself
	files     map[string]gitFile // the watched paths that existed, by absolute path
	// extra are the work-tree paths git runs through a hooks directory the
	// repository already set (core.hooksPath: husky, …) and the hook
	// managers' configs, as they were judged before the command: a
	// hooksPath the command set is undone, never followed.
	extra map[string]gitFileKind
}

// gitFile is one watched path as it was: a file, a link or a directory.
type gitFile struct {
	data []byte
	mode os.FileMode
	link string // a symlink: its target
	dir  bool
}

// gitFileKind is how a watched path is judged and put back.
type gitFileKind int

const (
	gitKindWhole             gitFileKind = iota // any change is undone (commondir, info/attributes)
	gitKindConfig                               // only new entries that run a command are undone
	gitKindHook                                 // any change is undone; git init's samples may appear
	gitKindWorktreeCommondir                    // may appear when it points back at the git directory
)

// gitDataDirs hold a git directory's data, never what it runs; skipped when
// looking for submodule git directories under modules/.
var gitDataDirs = []string{"objects", "refs", "logs", "lfs", "rr-cache"}

// gitWatched lists every path in gitDir (and the linked worktree's commonDir)
// git runs, or reads commands or another git directory from, on its own.
// Paths that do not exist are listed too: a command may create them.
func gitWatched(gitDir, commonDir string) map[string]gitFileKind {
	out := map[string]gitFileKind{}
	top := func(dir string) {
		out[filepath.Join(dir, "config")] = gitKindConfig
		out[filepath.Join(dir, "config.worktree")] = gitKindConfig
		out[filepath.Join(dir, "commondir")] = gitKindWhole
		out[filepath.Join(dir, "info", "attributes")] = gitKindWhole
		hooks := filepath.Join(dir, "hooks")
		out[hooks] = gitKindHook
		_ = filepath.WalkDir(hooks, func(p string, _ fs.DirEntry, err error) error {
			// git init's samples are never run: skipping them keeps a
			// project with many repositories quick to check.
			if err == nil && !strings.HasSuffix(p, ".sample") {
				out[p] = gitKindHook
			}
			return nil
		})
	}
	top(gitDir)
	if commonDir != "" && commonDir != gitDir {
		top(commonDir)
	}
	if wts, err := os.ReadDir(filepath.Join(gitDir, "worktrees")); err == nil {
		for _, w := range wts {
			dir := filepath.Join(gitDir, "worktrees", w.Name())
			out[filepath.Join(dir, "config.worktree")] = gitKindConfig
			out[filepath.Join(dir, "commondir")] = gitKindWorktreeCommondir
		}
	}
	// Submodules: modules/<name>/ is a git directory of its own, and a name
	// may hold slashes (modules/lib/x/), so every directory below is one.
	modules := filepath.Join(gitDir, "modules")
	_ = filepath.WalkDir(modules, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() || p == modules {
			return nil
		}
		if slices.Contains(gitDataDirs, d.Name()) {
			if _, err := os.Lstat(filepath.Join(filepath.Dir(p), "HEAD")); err == nil {
				return fs.SkipDir // a submodule's objects, refs, logs
			}
		}
		top(p)
		return nil
	})
	return out
}

// gitHookNames are the hooks git runs (githooks(5)): in a hooks directory
// the repository points core.hooksPath at, which lies in the work tree, only
// these are watched, so the restore never touches the project's own files.
var gitHookNames = []string{"applypatch-msg", "pre-applypatch", "post-applypatch", "pre-commit",
	"pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-commit", "pre-rebase", "post-checkout",
	"post-merge", "pre-push", "pre-receive", "update", "proc-receive", "post-receive", "post-update",
	"reference-transaction", "push-to-checkout", "pre-auto-gc", "post-rewrite", "sendemail-validate",
	"fsmonitor-watchman", "p4-changelist", "p4-prepare-changelist", "p4-post-changelist", "p4-pre-submit",
	"post-index-change"}

// hookManagers are the configs a hook manager's installed hook reads its
// commands from, by a text its hook carries: editing the config changes what
// the hook runs as surely as editing the hook.
var hookManagers = map[string][]string{
	"pre-commit.com": {".pre-commit-config.yaml"},
	"lefthook": {"lefthook.yml", "lefthook.yaml", ".lefthook.yml", ".lefthook.yaml",
		"lefthook-local.yml", "lefthook-local.yaml", ".lefthook-local.yml", ".lefthook-local.yaml"},
}

// hookPathsOf lists the work-tree paths git runs, or a hook reads commands
// from, outside the git directory: the hooks in a core.hooksPath inside root
// (with husky's helpers and, for its `.husky/_` layout, the scripts in
// `.husky` they run) and the config of a hook manager whose hook is
// installed. The hooksPath is read from the config as it is now, so call it
// before the command.
func hookPathsOf(root, gitDir, commonDir string) map[string]gitFileKind {
	out := map[string]gitFileKind{}
	cfgDir := gitDir
	if commonDir != "" {
		cfgDir = commonDir
	}
	hooksPath := ""
	for _, f := range []string{filepath.Join(cfgDir, "config"), filepath.Join(gitDir, "config.worktree")} {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		entries, _ := parseGitConfig(b)
		for _, e := range entries {
			if e.name == "core.hookspath" {
				hooksPath = e.value
			}
		}
	}
	// A config file the repository's config already includes from the work
	// tree (a shared config in a monorepo): the include itself is not new,
	// so only watching the included file keeps a command from adding a
	// core.fsmonitor through it.
	for _, f := range includedConfigs(root, []string{filepath.Join(cfgDir, "config"), filepath.Join(gitDir, "config.worktree")}) {
		out[f] = gitKindConfig
	}
	var hookDirs []string
	if hooksPath != "" && !strings.HasPrefix(hooksPath, "~") {
		dir := hooksPath
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(root, dir)
		}
		// Outside the project the sandbox cannot write it.
		if dir = filepath.Clean(dir); within(root, dir) {
			hookDirs = append(hookDirs, dir)
			// husky's helpers, which its hooks source; not at the project
			// root, where an `h` would be an ordinary file.
			for _, helper := range []string{"h", "husky.sh", filepath.Join("_", "h"), filepath.Join("_", "husky.sh")} {
				if dir != root {
					out[filepath.Join(dir, helper)] = gitKindHook
				}
			}
			if filepath.Base(dir) == "_" && filepath.Dir(dir) != root {
				hookDirs = append(hookDirs, filepath.Dir(dir)) // husky 9: .husky/_/<hook> runs .husky/<hook>
			}
		}
	}
	hookDirs = append(hookDirs, filepath.Join(gitDir, "hooks"))
	if commonDir != "" {
		hookDirs = append(hookDirs, filepath.Join(commonDir, "hooks"))
	}
	for _, dir := range hookDirs {
		for _, name := range gitHookNames {
			p := filepath.Join(dir, name)
			if within(root, p) {
				out[p] = gitKindHook
			}
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			for marker, configs := range hookManagers {
				if bytes.Contains(b, []byte(marker)) {
					for _, c := range configs {
						out[filepath.Join(root, c)] = gitKindWhole
					}
				}
			}
		}
	}
	return out
}

// includedConfigs lists the files inside root that the configs include
// (include.path, includeIf.<cond>.path, whatever the condition), following
// includes in them too, a few levels deep like git.
func includedConfigs(root string, configs []string) []string {
	var out []string
	seen := map[string]bool{}
	var visit func(cfg string, depth int)
	visit = func(cfg string, depth int) {
		b, err := os.ReadFile(cfg)
		if err != nil || depth > 10 {
			return
		}
		entries, _ := parseGitConfig(b)
		for _, e := range entries {
			top, _, _ := strings.Cut(e.name, ".")
			if (top != "include" && top != "includeif") || !strings.HasSuffix(e.name, ".path") || e.value == "" ||
				strings.HasPrefix(e.value, "~") {
				continue
			}
			p := e.value
			if !filepath.IsAbs(p) {
				p = filepath.Join(filepath.Dir(cfg), p) // git: relative to the including file
			}
			if p = filepath.Clean(p); !within(root, p) || seen[p] {
				continue
			}
			seen[p] = true
			out = append(out, p)
			visit(p, depth+1)
		}
	}
	for _, c := range configs {
		visit(c, 0)
	}
	return out
}

// commonDirOf is where gitDir's commondir file points ("" when it has none):
// a linked worktree's hooks and config live there.
func commonDirOf(gitDir string) string {
	b, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return ""
	}
	dir := strings.TrimSpace(string(b))
	if dir == "" {
		return ""
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(gitDir, dir)
	}
	return filepath.Clean(dir)
}

// captureGitState records what git runs on its own in root's repository; nil
// when root is not a git work tree.
func captureGitState(root string) *gitState {
	gitDir := gitDirOf(root)
	if gitDir == "" {
		return nil
	}
	st := &gitState{gitDir: gitDir, commonDir: commonDirOf(gitDir), files: map[string]gitFile{}}
	st.dotGit, _ = readGitFile(filepath.Join(root, ".git"))
	st.extra = hookPathsOf(root, gitDir, st.commonDir)
	watched := gitWatched(gitDir, st.commonDir)
	maps.Copy(watched, st.extra)
	for p := range watched {
		if f, ok := readGitFile(p); ok {
			st.files[p] = f
		}
	}
	return st
}

// Repositories nested in the project (a library cloned into vendor/, a
// package installed from source) run their own hooks and config when the
// user runs git in them, and the sandbox can write them as it writes the
// project. They are found to a bounded depth, without descending into
// node_modules or a git directory, and checked like the project's own.
const (
	nestedRepoDepth   = 8
	nestedRepoMaxDirs = 50000
)

// nestedRepos lists the work trees below root that hold a .git (directory or
// pointer), root itself excluded; complete is false when the walk stopped at
// nestedRepoMaxDirs, so some may be missing.
func nestedRepos(root string) (out []string, complete bool) {
	complete = true
	dirs := 0
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if dirs++; dirs > nestedRepoMaxDirs {
			complete = false
			return fs.SkipAll
		}
		switch d.Name() {
		case ".git":
			return fs.SkipDir
		case "node_modules":
			// Too large to walk, but a package installed from git keeps its
			// repository: node_modules/<pkg> and node_modules/@scope/<pkg>.
			out = append(out, nodeModulesRepos(p)...)
			return fs.SkipDir
		}
		rel, _ := filepath.Rel(root, p)
		if p != root {
			if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
				out = append(out, p)
			}
		}
		if rel != "." && strings.Count(rel, string(filepath.Separator))+1 >= nestedRepoDepth {
			return fs.SkipDir
		}
		return nil
	})
	return out, complete
}

// nodeModulesRepos lists the packages right under a node_modules directory
// that are git work trees.
func nodeModulesRepos(dir string) []string {
	var out []string
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		pkgs := []string{filepath.Join(dir, e.Name())}
		if strings.HasPrefix(e.Name(), "@") {
			scoped, _ := os.ReadDir(pkgs[0])
			pkgs = nil
			for _, s := range scoped {
				if s.IsDir() {
					pkgs = append(pkgs, filepath.Join(dir, e.Name(), s.Name()))
				}
			}
		}
		for _, p := range pkgs {
			if _, err := os.Lstat(filepath.Join(p, ".git")); err == nil {
				out = append(out, p)
			}
		}
	}
	return out
}

// gitStates are the states of the project's repository and of those nested
// in it, by work tree.
type gitStates map[string]*gitState

// captureGitStates captures root's repository and the nested repos.
func captureGitStates(root string, nested []string) gitStates {
	out := gitStates{root: captureGitState(root)}
	for _, wt := range nested {
		out[wt] = captureGitState(wt)
	}
	return out
}

// restoreGitStates restores every repository captured before, and checks
// the nested repos found now (nestedNow) — one the command created has no
// earlier state: its new hooks and runnable config entries are undone. The
// names are relative to root.
func restoreGitStates(root string, before gitStates, nestedNow []string) []string {
	trees := slices.Collect(maps.Keys(before))
	for _, wt := range append([]string{root}, nestedNow...) {
		if !slices.Contains(trees, wt) {
			trees = append(trees, wt)
		}
	}
	var undone []string
	for _, wt := range slices.Sorted(slices.Values(trees)) {
		prefix := ""
		if wt != root {
			prefix = filepath.ToSlash(relTo(root, wt)) + "/"
		}
		for _, u := range restoreGitState(wt, before[wt]) {
			undone = append(undone, prefix+u)
		}
	}
	return undone
}

// readGitFile reads p without following a link; false when it is missing.
func readGitFile(p string) (gitFile, bool) {
	fi, err := os.Lstat(p)
	switch {
	case err != nil:
		return gitFile{}, false
	case fi.Mode()&os.ModeSymlink != 0:
		target, err := os.Readlink(p)
		return gitFile{link: target, mode: fi.Mode()}, err == nil
	case fi.IsDir():
		return gitFile{dir: true}, true
	}
	b, err := os.ReadFile(p)
	return gitFile{data: b, mode: fi.Mode().Perm()}, err == nil
}

func sameGitFile(a, b gitFile) bool {
	if a.dir || b.dir {
		return a.dir == b.dir
	}
	return a.link == b.link && a.mode == b.mode && bytes.Equal(a.data, b.data)
}

func writeGitFile(p string, f gitFile) {
	switch {
	case f.dir:
		_ = os.MkdirAll(p, 0o755)
	case f.link != "":
		_ = os.Symlink(f.link, p)
	default:
		_ = os.WriteFile(p, f.data, f.mode)
	}
}

// restoreGitState puts back, after a confined command, what it changed that
// git runs on its own, and names what it put back. before == nil (no
// repository then) still checks a repository the command created.
func restoreGitState(root string, before *gitState) []string {
	var undone []string
	if before != nil {
		if note := restoreDotGit(root, before.dotGit); note != "" {
			undone = append(undone, note)
		}
	}
	gitDir := gitDirOf(root)
	if gitDir == "" {
		return undone
	}
	if before == nil || before.gitDir != gitDir {
		before = &gitState{gitDir: gitDir, files: map[string]gitFile{}}
	}
	watched := gitWatched(gitDir, before.commonDir)
	maps.Copy(watched, before.extra)
	for p := range before.files {
		if _, ok := watched[p]; !ok {
			watched[p] = kindOf(p) // under a directory the command replaced
		}
	}
	name := func(p string) string { return filepath.ToSlash(relTo(root, p)) }
	var replaced []string // directories put back: what they held comes back too
	under := func(p string) bool {
		return slices.ContainsFunc(replaced, func(d string) bool { return strings.HasPrefix(p, d+string(filepath.Separator)) })
	}
	for _, p := range slices.Sorted(maps.Keys(watched)) {
		now, exists := readGitFile(p)
		was, existed := before.files[p]
		switch {
		case !exists && !existed:
			continue
		case !exists:
			if under(p) {
				writeGitFile(p, was)
			}
			continue
		case existed && sameGitFile(now, was):
			continue
		}
		switch watched[p] {
		case gitKindConfig:
			if now.link == "" && !now.dir && (!existed || was.link == "" && !was.dir) {
				added := newRunnableEntries(was.data, now.data)
				if len(added) == 0 {
					continue
				}
				fixed, mode := was.data, was.mode
				if !existed {
					fixed, mode = stripRunnable(now.data), now.mode // git init made it: keep the rest
				}
				if os.WriteFile(p, fixed, mode) == nil {
					undone = append(undone, name(p)+" ("+strings.Join(added, ", ")+")")
				}
				continue
			}
		case gitKindHook:
			if !existed && strings.HasSuffix(p, ".sample") && now.link == "" && !now.dir {
				continue // git init's samples: git never runs them
			}
			if !existed && now.dir {
				// A new directory runs nothing (git init makes hooks/);
				// the files in it are judged one by one.
				continue
			}
		case gitKindWorktreeCommondir:
			// git worktree add: worktrees/<name>/commondir points back at
			// the git directory.
			if !existed && now.link == "" && !now.dir &&
				commonDirOf(filepath.Dir(p)) == filepath.Clean(gitDir) {
				continue
			}
		}
		if os.RemoveAll(p) != nil {
			continue
		}
		if existed {
			writeGitFile(p, was)
			if was.dir {
				replaced = append(replaced, p)
			}
		}
		switch {
		case now.link != "" && !existed:
			undone = append(undone, name(p)+" (a new link)")
		case now.link != "" || now.dir != was.dir && existed:
			undone = append(undone, name(p)+" (was replaced)")
		default:
			undone = append(undone, name(p))
		}
	}
	return undone
}

// kindOf judges a path gitWatched no longer lists (its directory was
// replaced) by its name.
func kindOf(p string) gitFileKind {
	switch filepath.Base(p) {
	case "config", "config.worktree":
		return gitKindConfig
	case "commondir", "attributes":
		return gitKindWhole
	}
	return gitKindHook
}

// restoreDotGit puts back root/.git when the command swapped it: a git
// directory replaced by a link or a "gitdir:" pointer (to a directory with
// its own hooks and config), or a pointer rewritten. "" when it is as it was.
func restoreDotGit(root string, was gitFile) string {
	p := filepath.Join(root, ".git")
	now, exists := readGitFile(p)
	if !exists || sameGitFile(now, was) {
		return "" // removed, or untouched: nothing for git to run
	}
	if now.dir {
		if was.dir {
			return ""
		}
		// A pointer replaced by a whole git directory: set it aside.
		if os.Rename(p, p+".replaced-"+time.Now().Format("20060102150405")) != nil {
			return ""
		}
	} else if os.Remove(p) != nil {
		return ""
	}
	if was.dir {
		// The git directory was moved away: the pointer that replaced it is
		// gone, the directory itself is wherever the command put it.
		return ".git (was replaced; the git directory it was is not where it was)"
	}
	writeGitFile(p, was)
	return ".git (was replaced)"
}

// gitEntry is one key of a git config file: "section.subsection.key".
type gitEntry struct{ name, value string }

// gitConfigEntry is an entry and where it is in the file: the key through
// the end of its value (continuation lines included), not the line break.
type gitConfigEntry struct {
	gitEntry
	start, end int
}

// parseGitConfig reads a git config file's entries the way git does:
// section headers ([core], [filter "lfs"], [remote.origin]) — more than one
// on a line, and a key after one, are valid —, keys (case-insensitive),
// values with quotes, escapes, comments and continuation lines. ok is false
// for anything git's grammar does not allow: the caller then trusts none of
// it.
func parseGitConfig(b []byte) (entries []gitConfigEntry, ok bool) {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	i, section := 0, ""
	next := func() (byte, bool) { // git's get_next_char: \r\n is \n
		if i >= len(b) {
			return '\n', false
		}
		c := b[i]
		i++
		if c == '\r' && i < len(b) && b[i] == '\n' {
			i++
			c = '\n'
		}
		return c, true
	}
	isAlpha := func(c byte) bool { return c|0x20 >= 'a' && c|0x20 <= 'z' }
	isKeyChar := func(c byte) bool { return isAlpha(c) || c >= '0' && c <= '9' || c == '-' }
	for i < len(b) {
		start := i
		c, _ := next()
		switch {
		case c == '\n' || c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v':
			continue
		case c == '#' || c == ';':
			for c != '\n' {
				c, _ = next()
			}
			continue
		case c == '[':
			var name []byte
			sub, quoted := "", false
			for {
				c, more := next()
				if !more {
					return entries, false
				}
				if c == ']' {
					break
				}
				if c == ' ' || c == '\t' {
					if sub, quoted = parseSubsection(next); !quoted {
						return entries, false
					}
					break
				}
				if !isKeyChar(c) && c != '.' {
					return entries, false
				}
				name = append(name, c)
			}
			if len(name) == 0 {
				return entries, false
			}
			// [section.sub] is lowercased whole; a quoted subsection keeps
			// its case.
			section = strings.ToLower(string(name))
			if quoted {
				section += "." + sub
			}
			continue
		case !isAlpha(c):
			return entries, false
		}
		if section == "" {
			return entries, false // a key before any section
		}
		key := []byte{c}
		for i < len(b) && isKeyChar(b[i]) {
			key = append(key, b[i])
			i++
		}
		for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
			i++
		}
		value := ""
		switch c, _ := next(); c {
		case '\n':
		case '=':
			v, ok := parseGitValue(next)
			if !ok {
				return entries, false
			}
			value = v
		default:
			return entries, false
		}
		end := i
		if end > start && b[end-1] == '\n' {
			end--
			if end > start && b[end-1] == '\r' {
				end--
			}
		}
		entries = append(entries, gitConfigEntry{gitEntry{section + "." + strings.ToLower(string(key)), value}, start, end})
	}
	return entries, true
}

// parseSubsection reads ` "sub"]`, the space already read: git's quoted
// subsection, where a backslash takes the next character as it is.
func parseSubsection(next func() (byte, bool)) (string, bool) {
	c, _ := next()
	for c == ' ' || c == '\t' {
		c, _ = next()
	}
	if c != '"' {
		return "", false
	}
	var sub strings.Builder
	for {
		c, more := next()
		switch {
		case !more || c == '\n':
			return "", false
		case c == '"':
			c, _ = next()
			return sub.String(), c == ']'
		case c == '\\':
			if c, _ = next(); c == '\n' {
				return "", false
			}
		}
		sub.WriteByte(c)
	}
}

// parseGitValue reads a value after its '=', through the end of its line
// (git's parse_value).
func parseGitValue(next func() (byte, bool)) (string, bool) {
	var v strings.Builder
	quote, comment, spaces := false, false, 0
	for {
		c, _ := next()
		if c == '\n' {
			if quote {
				return "", false
			}
			return v.String(), true
		}
		if comment {
			continue
		}
		if (c == ' ' || c == '\t' || c == '\r' || c == '\f' || c == '\v') && !quote {
			if v.Len() > 0 {
				spaces++
			}
			continue
		}
		if !quote && (c == ';' || c == '#') {
			comment = true
			continue
		}
		for ; spaces > 0; spaces-- {
			v.WriteByte(' ')
		}
		switch c {
		case '\\':
			c, _ = next()
			switch c {
			case '\n':
				continue // a continuation line
			case 't':
				c = '\t'
			case 'b':
				c = '\b'
			case 'n':
				c = '\n'
			case '\\', '"':
			default:
				return "", false
			}
		case '"':
			quote = !quote
			continue
		}
		v.WriteByte(c)
	}
}

// runnableKeys are, by section, the keys that make git run a command;
// sections that hold nothing else are in runnableSections.
var runnableKeys = map[string][]string{
	"core": {"fsmonitor", "hookspath", "sshcommand", "editor", "pager", "askpass",
		"gitproxy", "alternaterefscommand", "worktree"},
	"diff":        {"textconv", "command", "external"},
	"merge":       {"driver"},
	"remote":      {"uploadpack", "receivepack", "proxy", "vcs"},
	"interactive": {"difffilter"},
	"trailer":     {"command", "cmd"},
	"browser":     {"cmd", "path"},
	"man":         {"cmd", "path"},
	"tar":         {"command"},
	"imap":        {"tunnel"},
	"gc":          {"recentobjectshook"},
	"extensions":  {"worktreeconfig"}, // makes git read config.worktree
}

// runnableSections are sections any key of which runs a command, reads
// config from elsewhere or sends git somewhere it would not go.
var runnableSections = []string{"filter", "credential", "gpg", "include", "includeif", "pager",
	"difftool", "mergetool", "sequence", "uploadpack", "protocol", "url", "submodule",
	"guitool", "sendemail", "hook", "instaweb"}

// runnableEntry reports whether a config entry makes git run a command, or
// read config (and so commands) from elsewhere.
func runnableEntry(e gitEntry) bool {
	section, key := e.name, ""
	if i := strings.LastIndexByte(e.name, '.'); i >= 0 {
		section, key = e.name[:i], e.name[i+1:]
	}
	top, _, _ := strings.Cut(section, ".")
	switch {
	case top == "alias":
		return strings.HasPrefix(strings.TrimSpace(e.value), "!")
	case slices.Contains(runnableSections, top):
		return true
	}
	return slices.Contains(runnableKeys[top], key)
}

// gitConfigUnreadable is what newRunnableEntries reports for a config it
// cannot parse: it is put back whole.
const gitConfigUnreadable = "a config ouija cannot read"

// newRunnableEntries lists the runnable entries in after that before did not
// have with the same value. A changed config that does not parse is
// reported whole: git may read it differently.
func newRunnableEntries(before, after []byte) []string {
	if bytes.Equal(before, after) {
		return nil
	}
	now, ok := parseGitConfig(after)
	if !ok {
		return []string{gitConfigUnreadable}
	}
	was, _ := parseGitConfig(before)
	had := map[gitEntry]bool{}
	for _, e := range was {
		had[e.gitEntry] = true
	}
	var added []string
	for _, e := range now {
		if runnableEntry(e.gitEntry) && !had[e.gitEntry] {
			added = append(added, e.name)
		}
	}
	return added
}

// stripRunnable drops the runnable entries from a config that had no earlier
// version to go back to; the section headers stay. One that does not parse
// is dropped whole: git runs with no repository config.
func stripRunnable(b []byte) []byte {
	entries, ok := parseGitConfig(b)
	if !ok {
		return nil
	}
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	out := slices.Clone(b)
	for _, e := range slices.Backward(entries) {
		if runnableEntry(e.gitEntry) {
			out = append(out[:e.start], out[e.end:]...)
		}
	}
	return out
}
