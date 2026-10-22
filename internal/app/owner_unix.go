//go:build unix

package app

import (
	"os"
	"syscall"
)

// foreignOwner reports whether a discovered config file belongs to another
// user: neither the current user nor root. Discovery walks up through
// directories other users can write (/tmp, a shared /srv), where anyone can
// plant a .ouija.json that would apply to every project below it.
func foreignOwner(st os.FileInfo) bool {
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	return sys.Uid != 0 && int(sys.Uid) != os.Getuid()
}
