// Package atomicfile writes files so that a crash, a full disk or a
// concurrent reader never sees a half-written one: the data goes to a
// temporary file in the same directory, is synced, and is renamed over the
// target in one step.
package atomicfile

import (
	"os"
	"path/filepath"
)

// Write replaces path with data. A new file gets perm. An existing file keeps
// its mode when keepMode is set (a file another tool owns, such as an editor's
// config); otherwise it gets perm too (ouija's own secrets stay 0600). When
// path is a symlink — dotfiles managed with stow, say — the file it points to
// is replaced, so the link survives.
func Write(path string, data []byte, perm os.FileMode, keepMode bool) error {
	target := path
	if real, err := filepath.EvalSymlinks(path); err == nil {
		target = real
	}
	mode := perm
	if keepMode {
		if st, err := os.Stat(target); err == nil {
			mode = st.Mode().Perm()
		}
	}

	dir := filepath.Dir(target)
	f, err := os.CreateTemp(dir, "."+filepath.Base(target)+".tmp-*") // created 0600
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer func() {
		if tmp != "" {
			os.Remove(tmp) // any failure before the rename leaves the target untouched
		}
	}()
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, target); err != nil {
		return err
	}
	tmp = ""
	// Persist the rename itself (POSIX); best effort elsewhere.
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
	return nil
}
