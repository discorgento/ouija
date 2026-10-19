package engine

import (
	"crypto/sha256"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// GitGuardMarker starts the notice a bash result carries when the command
// changed what git executes on its own. The chat shows such results to the
// user, and conversation compression keeps them.
const GitGuardMarker = "⚠ git guard:"

// gitGuardSnapshot fingerprints what git runs without being asked: the hooks
// and the repository config (core.fsmonitor, core.pager, aliases…). A command
// — even one confined by the sandbox, which must leave .git writable — can
// plant code there that runs, unconfined, the next time anyone runs git in
// the project. nil when root is not a git work tree.
func gitGuardSnapshot(root string) map[string]string {
	gitDir := gitDirOf(root)
	if gitDir == "" {
		return nil
	}
	snap := map[string]string{}
	add := func(path string) {
		fi, err := os.Lstat(path)
		if err != nil || fi.IsDir() {
			return
		}
		b, err := os.ReadFile(path)
		if err != nil && fi.Mode()&os.ModeSymlink == 0 {
			return
		}
		target, _ := os.Readlink(path) // a hook may be a symlink to a script
		sum := sha256.Sum256(append(b, target...))
		snap[path] = fmt.Sprintf("%x %v", sum[:8], fi.Mode())
	}
	add(filepath.Join(gitDir, "config"))
	if entries, err := os.ReadDir(filepath.Join(gitDir, "hooks")); err == nil {
		for _, e := range entries {
			add(filepath.Join(gitDir, "hooks", e.Name()))
		}
	}
	return snap
}

// gitDirOf is root's git directory: root/.git, or where a .git file points
// (a linked worktree or submodule: "gitdir: <path>"). "" when there is none.
func gitDirOf(root string) string {
	dotGit := filepath.Join(root, ".git")
	fi, err := os.Stat(dotGit)
	switch {
	case err != nil:
		return ""
	case fi.IsDir():
		return dotGit
	}
	b, err := os.ReadFile(dotGit)
	if err != nil {
		return ""
	}
	dir, ok := strings.CutPrefix(strings.TrimSpace(string(b)), "gitdir:")
	if !ok {
		return ""
	}
	dir = strings.TrimSpace(dir)
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, dir)
	}
	return dir
}

// gitGuardNotice describes what changed between two snapshots, "" when
// nothing did.
func gitGuardNotice(root string, before, after map[string]string) string {
	var changed []string
	for _, p := range slices.Sorted(maps.Keys(after)) {
		if before[p] != after[p] {
			changed = append(changed, relTo(root, p))
		}
	}
	for _, p := range slices.Sorted(maps.Keys(before)) {
		if _, ok := after[p]; !ok {
			changed = append(changed, relTo(root, p)+" (removed)")
		}
	}
	if len(changed) == 0 {
		return ""
	}
	return GitGuardMarker + " this command changed " + strings.Join(changed, ", ") +
		" — git runs hooks and config commands outside ouija's checks and sandbox; review them before running git here"
}

func relTo(root, p string) string {
	if r, err := filepath.Rel(root, p); err == nil && !strings.HasPrefix(r, "..") {
		return r
	}
	return p
}
