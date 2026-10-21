//go:build windows

package engine

import (
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

// linkCount is the number of hard links to the file at p. Windows' FileInfo
// does not carry it, so the file is opened for its attributes only.
func linkCount(p string, _ os.FileInfo) uint64 {
	name, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return 1
	}
	h, err := syscall.CreateFile(name, 0, syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return 1
	}
	defer syscall.CloseHandle(h)
	var info syscall.ByHandleFileInformation
	if syscall.GetFileInformationByHandle(h, &info) != nil {
		return 1
	}
	return uint64(info.NumberOfLinks)
}

// shortComponent matches an 8.3 short name's "~N" (SSH~1, OUIJA~1.JSO).
var shortComponent = regexp.MustCompile(`~[0-9]`)

// longName expands the 8.3 short names in p (C:\Users\me\SSH~1\id_rsa is
// ~\.ssh\id_rsa): the policy matches long names, and Windows opens either.
// The longest existing prefix is expanded; components that do not exist yet
// are kept as they are.
func longName(p string) string {
	if !shortComponent.MatchString(p) {
		return p
	}
	existing, rest := p, ""
	for {
		if long, ok := getLongPathName(existing); ok {
			return filepath.Join(long, rest)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return p
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
}

func getLongPathName(p string) (string, bool) {
	short, err := syscall.UTF16PtrFromString(p)
	if err != nil {
		return "", false
	}
	buf := make([]uint16, syscall.MAX_PATH)
	for {
		n, err := syscall.GetLongPathName(short, &buf[0], uint32(len(buf)))
		if err != nil || n == 0 {
			return "", false
		}
		if int(n) <= len(buf) {
			return syscall.UTF16ToString(buf[:n]), true
		}
		buf = make([]uint16, n)
	}
}
