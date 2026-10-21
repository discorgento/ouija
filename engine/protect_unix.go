//go:build !windows

package engine

import (
	"os"
	"syscall"
)

// linkCount is the number of hard links to the file fi describes.
func linkCount(_ string, fi os.FileInfo) uint64 {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return uint64(st.Nlink)
	}
	return 1
}

// longName is the identity on Unix, which has no short names.
func longName(p string) string { return p }
