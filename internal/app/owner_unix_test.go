//go:build unix

package app

import (
	"os"
	"syscall"
	"testing"
	"time"
)

// statOwnedBy is a FileInfo whose owner is uid.
type statOwnedBy uint32

func (statOwnedBy) Name() string       { return ".ouija.json" }
func (statOwnedBy) Size() int64        { return 0 }
func (statOwnedBy) Mode() os.FileMode  { return 0o644 }
func (statOwnedBy) ModTime() time.Time { return time.Time{} }
func (statOwnedBy) IsDir() bool        { return false }
func (s statOwnedBy) Sys() any         { return &syscall.Stat_t{Uid: uint32(s)} }

// A config another user owns is foreign; the user's own and root's are not.
func TestForeignOwner(t *testing.T) {
	me := uint32(os.Getuid())
	other := me + 4242
	if other == 0 {
		other = 4242
	}
	for uid, want := range map[uint32]bool{me: false, 0: false, other: true} {
		if got := foreignOwner(statOwnedBy(uid)); got != want {
			t.Errorf("foreignOwner(uid %d) = %v, want %v (running as %d)", uid, got, want, me)
		}
	}
}
