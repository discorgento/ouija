//go:build !windows

package chat

import (
	"os"
	"syscall"
)

// canRestart reports whether the chat can restart itself in place (a theme
// applies on the spot).
const canRestart = true

// restartSelf replaces this process with ouija run again with args: the
// terminal is the TUI's again, with the new colors.
func restartSelf(args, env []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return syscall.Exec(exe, args, env)
}
