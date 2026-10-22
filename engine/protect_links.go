package engine

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Hard links. The protected-path policy judges a file by its names, and a
// hard link is the same file under another name: `ln ~/.ssh/id_rsa notes.txt`
// (bash run outside the sandbox can make one) left the key readable as
// notes.txt. A file with more than one link is compared, by identity, with the
// credential files ouija knows of.

// protectedFileNames are the credential files, relative to the home
// directory, a hard link must not reach. Directories list their files.
var protectedFileNames = []string{
	".ssh", ".gnupg", ".aws", ".kube", ".docker", ".azure",
	".netrc", ".git-credentials", ".gitconfig", ".npmrc", ".pgpass", ".pypirc", ".my.cnf",
	filepath.Join(".config", "gh"), filepath.Join(".config", "gcloud"),
	filepath.Join(".config", "kube"), filepath.Join(".config", "ouija"),
	filepath.Join(".cargo", "credentials"), filepath.Join(".cargo", "credentials.toml"),
	filepath.Join(".m2", "settings.xml"), filepath.Join(".m2", "settings-security.xml"),
	filepath.Join(".gradle", "gradle.properties"), filepath.Join(".cache", "huggingface", "token"),
	filepath.Join(".huggingface", "token"), filepath.Join(".local", "share", "uv", "credentials"),
	filepath.Join(".config", "git", "credentials"), filepath.Join(".terraform.d", "credentials.tfrc.json"),
	".vault-token", filepath.Join(".config", "rclone", "rclone.conf"), ".password-store",
	filepath.Join(".local", "share", "keyrings"), filepath.Join(".local", "share", "kwalletd"),
	filepath.Join(".config", "composer", "auth.json"), filepath.Join(".composer", "auth.json"),
	filepath.Join(".config", "pip", "pip.conf"), filepath.Join(".gem", "credentials"),
	filepath.Join(".config", "containers", "auth.json"), ".s3cfg", ".terraformrc",
	filepath.Join(".config", "hub"), filepath.Join(".config", "glab-cli"),
}

// maxProtectedScan bounds the walk of the credential directories (a huge
// ~/.docker or ~/.kube cache must not stall every check).
const maxProtectedScan = 5000

var protectedFiles struct {
	sync.Mutex
	infos []os.FileInfo
	dirs  map[string]time.Time // every directory walked, with its mtime
}

// knownProtectedFiles returns the credential files that exist now. The list
// is kept while none of the directories it came from has changed: a key added
// or replaced (an atomic rename gives it a new inode) changes its directory's
// mtime, and the next check walks them again. A cache by time left a window.
func knownProtectedFiles() []os.FileInfo {
	protectedFiles.Lock()
	defer protectedFiles.Unlock()
	if protectedFiles.dirs != nil && dirsUnchanged(protectedFiles.dirs) {
		return protectedFiles.infos
	}
	var roots []string
	home, _ := os.UserHomeDir()
	if home != "" {
		for _, name := range protectedFileNames {
			roots = append(roots, filepath.Join(home, name))
		}
		roots = append(roots, home) // its mtime changes when a dotfile is added
	}
	if dir, err := os.UserConfigDir(); err == nil {
		roots = append(roots, filepath.Join(dir, "ouija"), filepath.Join(dir, "gcloud"))
	}
	var infos []os.FileInfo
	dirs := map[string]time.Time{}
	seen := 0
	for _, root := range roots {
		fi, err := os.Stat(root)
		switch {
		case err != nil:
			dirs[root] = time.Time{} // absent: watched for appearing
		case fi.Mode().IsRegular():
			infos = append(infos, fi)
		case root == home:
			dirs[root] = fi.ModTime() // the home itself is watched, not walked
		case fi.IsDir():
			_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil || seen >= maxProtectedScan {
					return fs.SkipAll
				}
				seen++
				info, err := d.Info()
				if err != nil {
					return nil
				}
				switch {
				case d.IsDir():
					dirs[p] = info.ModTime()
				case info.Mode().IsRegular():
					infos = append(infos, info)
				}
				return nil
			})
		}
	}
	protectedFiles.infos, protectedFiles.dirs = infos, dirs
	return infos
}

// dirsUnchanged reports whether every directory still has the mtime it had
// (an absent one is still absent).
func dirsUnchanged(dirs map[string]time.Time) bool {
	for d, mtime := range dirs {
		fi, err := os.Stat(d)
		if mtime.IsZero() {
			if err == nil {
				return false
			}
			continue
		}
		if err != nil || !fi.ModTime().Equal(mtime) {
			return false
		}
	}
	return true
}

// linksToProtected reports whether p is a hard link to a credential file.
// Only a regular file with more than one link can be one, so the common case
// costs one stat.
func linksToProtected(p string) bool {
	fi, err := os.Stat(p)
	if err != nil || !fi.Mode().IsRegular() || linkCount(p, fi) < 2 {
		return false
	}
	for _, known := range knownProtectedFiles() {
		if os.SameFile(fi, known) {
			return true
		}
	}
	return false
}

// isProtectedFile is isProtectedPath plus the hard-link check: for paths
// that name a file on disk.
func isProtectedFile(p string) bool {
	return isProtectedPath(p) || linksToProtected(p)
}

// Hard links inside the project. The policy judges a file by its names, so
// `ln .env notes.txt` handed out .env as notes.txt, and `ln .git/config
// notes.txt` let write_file rewrite the git config. Finding every other name
// of a file is not possible, so a file with more than one link is judged by
// where it is:
//   - written: never — a write through it changes every other name too;
//   - read under .git/objects (`git clone --local` links objects): allowed;
//   - read under node_modules (pnpm links every file from its store):
//     allowed unless it is one of the project's protected files, found by
//     a walk of the project made once per call (newLinkCheck);
//   - read anywhere else: refused. Nothing in a checkout makes hard links,
//     so this costs little, and it is what fails closed.

// linkCheck holds the project's protected files for one tool call, walked
// on first need: a grep of a pnpm node_modules opens thousands of linked
// files and must not walk the project for each.
type linkCheck struct {
	done     bool
	infos    []os.FileInfo
	complete bool // false: the walk hit maxProjectScan, nothing is known
}

// maxProjectScan bounds the walk for the project's protected files; past it
// a linked file in node_modules is refused.
const maxProjectScan = 200000

// checkHardLinks applies the rules above to fi, the file opened at real (its
// physical path). Only files inside the project are judged: an approved
// escape keeps the home-credential check of isProtectedFile.
func (lc *linkCheck) checkHardLinks(rootAbs, real string, fi os.FileInfo, write bool) error {
	if !fi.Mode().IsRegular() || !within(rootAbs, real) || linkCount(real, fi) < 2 {
		return nil
	}
	rel, _ := filepath.Rel(rootAbs, real)
	rel = policyName(filepath.ToSlash(rel), foldCase, false)
	switch {
	case write:
		return fmt.Errorf("%s has other names (hard links): writing it would change them too", rel)
	case containsComponentPair(rel, "/", ".git", "objects"):
		return nil
	case containsComponent(rel, "/", "node_modules"):
		if lc.isProtected(rootAbs, fi) {
			return fmt.Errorf("path is protected (a hard link to a protected file): %s", rel)
		}
		return nil
	}
	return fmt.Errorf("%s has other names (hard links), which may be a protected file's: refused", rel)
}

// isProtected reports whether fi is one of the project's protected files —
// true too when the walk could not finish.
func (lc *linkCheck) isProtected(rootAbs string, fi os.FileInfo) bool {
	if !lc.done {
		lc.done = true
		lc.infos, lc.complete = projectProtectedFiles(rootAbs)
	}
	if !lc.complete {
		return true
	}
	for _, known := range lc.infos {
		if os.SameFile(fi, known) {
			return true
		}
	}
	return false
}

// projectProtectedFiles walks the project for the files the policy protects
// (.env, .git/config, a key…), leaving out node_modules (what is linked
// there is checked against this) and git's object stores.
func projectProtectedFiles(rootAbs string) ([]os.FileInfo, bool) {
	var infos []os.FileInfo
	seen, complete := 0, true
	_ = filepath.WalkDir(rootAbs, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if seen++; seen > maxProjectScan {
			complete = false
			return fs.SkipAll
		}
		if d.IsDir() {
			if name := d.Name(); name == "node_modules" || (name == "objects" || name == "lfs") && filepath.Base(filepath.Dir(p)) == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type().IsRegular() && isProtectedPath(p) {
			if info, err := os.Stat(p); err == nil {
				infos = append(infos, info)
			}
		}
		return nil
	})
	return infos, complete
}
