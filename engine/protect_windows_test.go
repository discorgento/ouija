//go:build windows

package engine

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

// An 8.3 short name (SSH~1 for .ssh) is the same directory to Windows: the
// policy must see the long name.
func TestShortNameOfAProtectedDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), ".ssh")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	long, _ := syscall.UTF16PtrFromString(dir)
	buf := make([]uint16, syscall.MAX_PATH)
	n, err := syscall.GetShortPathName(long, &buf[0], uint32(len(buf)))
	if err != nil || n == 0 {
		t.Skip("no short names here:", err)
	}
	short := syscall.UTF16ToString(buf[:n])
	if filepath.Base(short) == ".ssh" {
		t.Skip("8.3 names are off on this volume")
	}
	for _, p := range []string{short, filepath.Join(short, "id_rsa")} {
		if !isProtectedPath(p) {
			t.Errorf("isProtectedPath(%q) = false: the short name of .ssh", p)
		}
	}
}
