package engine

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Undoing what a confined command planted for git to run. On Linux the
// sandbox must leave the project writable, .git included (Landlock cannot
// carve a read-only subtree out of a writable one), so a confined command
// could write a hook or a core.fsmonitor that runs, unconfined, the next time
// the user runs git. After every confined run ouija puts back what git would
// execute on its own: the hooks as they were, and the config entries that
// run a command. Ordinary changes (git init, git remote add, user.name) stay.

// gitState is what git executes on its own, captured before a command.
type gitState struct {
	gitDir string
	config []byte // nil: no config
	hooks  map[string]hookFile
}

type hookFile struct {
	data []byte
	mode os.FileMode
	link string // a hook that is a symlink: its target
}

// captureGitState records root's hooks and config; nil when root is not a
// git work tree.
func captureGitState(root string) *gitState {
	gitDir := gitDirOf(root)
	if gitDir == "" {
		return nil
	}
	st := &gitState{gitDir: gitDir, hooks: map[string]hookFile{}}
	st.config, _ = os.ReadFile(filepath.Join(gitDir, "config"))
	if entries, err := os.ReadDir(filepath.Join(gitDir, "hooks")); err == nil {
		for _, e := range entries {
			if h, ok := readHook(filepath.Join(gitDir, "hooks", e.Name())); ok {
				st.hooks[e.Name()] = h
			}
		}
	}
	return st
}

func readHook(p string) (hookFile, bool) {
	fi, err := os.Lstat(p)
	if err != nil || fi.IsDir() {
		return hookFile{}, false
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		target, err := os.Readlink(p)
		return hookFile{link: target, mode: fi.Mode()}, err == nil
	}
	b, err := os.ReadFile(p)
	return hookFile{data: b, mode: fi.Mode().Perm()}, err == nil
}

// restoreGitState puts back, after a confined command, what it changed that
// git runs on its own, and names what it put back. before == nil (no
// repository then) still checks a repository the command created.
func restoreGitState(root string, before *gitState) []string {
	gitDir := gitDirOf(root)
	if gitDir == "" {
		return nil
	}
	if before == nil || before.gitDir != gitDir {
		before = &gitState{gitDir: gitDir, hooks: map[string]hookFile{}}
	}
	var undone []string

	hooksDir := filepath.Join(gitDir, "hooks")
	if fi, err := os.Lstat(hooksDir); err == nil && !fi.IsDir() {
		// The hooks directory itself swapped for a link (or a file).
		if os.Remove(hooksDir) == nil {
			_ = os.Mkdir(hooksDir, 0o755)
			undone = append(undone, ".git/hooks (was replaced)")
		}
	}
	if entries, err := os.ReadDir(hooksDir); err == nil {
		for _, e := range entries {
			name := e.Name()
			p := filepath.Join(hooksDir, name)
			now, _ := readHook(p)
			was, existed := before.hooks[name]
			switch {
			case existed && sameHook(now, was):
				continue
			case !existed && strings.HasSuffix(name, ".sample") && !e.IsDir() && now.link == "":
				continue // git init's samples: git never runs them
			}
			_ = os.RemoveAll(p)
			if existed {
				writeHook(p, was)
			}
			undone = append(undone, ".git/hooks/"+name)
		}
	}
	cfgPath := filepath.Join(gitDir, "config")
	if fi, err := os.Lstat(cfgPath); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(cfgPath)
		if before.config != nil {
			_ = os.WriteFile(cfgPath, before.config, 0o644)
		}
		return append(undone, ".git/config (was replaced by a link)")
	}
	now, err := os.ReadFile(cfgPath)
	if err != nil {
		return undone
	}
	if added := newRunnableEntries(before.config, now); len(added) > 0 {
		fixed := before.config
		if fixed == nil {
			fixed = stripRunnable(now) // git init made it: keep the rest
		}
		if os.WriteFile(cfgPath, fixed, 0o644) == nil {
			undone = append(undone, ".git/config ("+strings.Join(added, ", ")+")")
		}
	}
	return undone
}

func sameHook(a, b hookFile) bool {
	return a.link == b.link && a.mode == b.mode && bytes.Equal(a.data, b.data)
}

func writeHook(p string, h hookFile) {
	if h.link != "" {
		_ = os.Symlink(h.link, p)
		return
	}
	_ = os.WriteFile(p, h.data, h.mode)
}

// gitEntry is one key of a git config file: "section.subsection.key".
type gitEntry struct{ name, value string }

// parseGitConfig reads a git config file's entries, enough to judge them:
// section headers ([core], [alias], [filter "lfs"], [remote.origin]) and
// key = value lines.
func parseGitConfig(b []byte) []gitEntry {
	var out []gitEntry
	section := ""
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 0, 64<<10), 4<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || line[0] == '#' || line[0] == ';' {
			continue
		}
		if line[0] == '[' {
			end := strings.IndexByte(line, ']')
			if end < 0 {
				continue
			}
			head := strings.TrimSpace(line[1:end])
			name, sub, quoted := strings.Cut(head, " ")
			if quoted {
				section = strings.ToLower(name) + "." + strings.Trim(strings.TrimSpace(sub), `"`)
			} else {
				section = strings.ToLower(head)
			}
			if rest := strings.TrimSpace(line[end+1:]); rest != "" {
				line = rest // [core] fsmonitor = x on one line
			} else {
				continue
			}
		}
		key, value, _ := strings.Cut(line, "=")
		out = append(out, gitEntry{section + "." + strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)})
	}
	return out
}

// runnableEntry reports whether a config entry makes git run a command, or
// read config (and so commands) from elsewhere.
func runnableEntry(e gitEntry) bool {
	section, key := e.name, ""
	if i := strings.LastIndexByte(e.name, '.'); i >= 0 {
		section, key = e.name[:i], e.name[i+1:]
	}
	top, _, _ := strings.Cut(section, ".")
	switch top {
	case "core":
		return slices.Contains([]string{"fsmonitor", "hookspath", "sshcommand", "editor", "pager", "askpass",
			"gitproxy", "alternaterefscommand", "worktree"}, key)
	case "alias":
		return strings.HasPrefix(strings.Trim(e.value, `"`), "!")
	case "filter", "credential", "gpg", "include", "includeif", "pager", "difftool", "mergetool",
		"sequence", "uploadpack", "protocol", "url", "submodule":
		return true
	case "diff":
		return key == "textconv" || key == "command" || key == "external"
	case "merge":
		return key == "driver"
	case "remote":
		return key == "uploadpack" || key == "receivepack" || key == "proxy" || key == "vcs"
	}
	return false
}

// newRunnableEntries lists the runnable entries in after that before did not
// have with the same value.
func newRunnableEntries(before, after []byte) []string {
	had := map[gitEntry]bool{}
	for _, e := range parseGitConfig(before) {
		had[e] = true
	}
	var added []string
	for _, e := range parseGitConfig(after) {
		if runnableEntry(e) && !had[e] {
			added = append(added, e.name)
		}
	}
	return added
}

// stripRunnable drops the runnable entries from a config that had no earlier
// version to go back to; the section headers stay.
func stripRunnable(b []byte) []byte {
	var out bytes.Buffer
	section := ""
	for line := range strings.Lines(string(b)) {
		t := strings.TrimSpace(line)
		if strings.HasPrefix(t, "[") {
			entries := parseGitConfig([]byte(t + "\n"))
			if end := strings.IndexByte(t, ']'); end > 0 {
				head := strings.TrimSpace(t[1:end])
				name, sub, quoted := strings.Cut(head, " ")
				section = strings.ToLower(head)
				if quoted {
					section = strings.ToLower(name) + "." + strings.Trim(strings.TrimSpace(sub), `"`)
				}
			}
			if len(entries) > 0 && runnableEntry(entries[0]) {
				out.WriteString(t[:strings.IndexByte(t, ']')+1] + "\n")
				continue
			}
			out.WriteString(line)
			continue
		}
		if t != "" && t[0] != '#' && t[0] != ';' {
			key, value, _ := strings.Cut(t, "=")
			if runnableEntry(gitEntry{section + "." + strings.ToLower(strings.TrimSpace(key)), strings.TrimSpace(value)}) {
				continue
			}
		}
		out.WriteString(line)
	}
	return out.Bytes()
}
