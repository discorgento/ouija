//go:build unix

package engine

import (
	"os"
	"os/exec"
	"syscall"
)

// ownProcessGroup starts the command in a process group of its own, so
// killProcessGroup reaches every process it started. A background job (`cmd &`,
// `(sleep 5; …) &`) outlived the shell: it went on after the git state was put
// back, and could still plant a hook that runs unconfined on the next commit.
// Cancellation (timeout, Esc) kills the whole group too, not only the shell.
func ownProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.Cancel = func() error {
		killProcessGroup(cmd)
		return cmd.Process.Kill()
	}
}

// killProcessGroup kills every process left in the command's group. A process
// that left the group (setsid, a double-forking daemon) is out of its reach.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil || cmd.Process.Pid <= 0 {
		return
	}
	// The group's id is the shell's pid (Setpgid with Pgid 0). The kernel does
	// not hand a pid out again while a group of that id still has members.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil && err != syscall.ESRCH {
		_ = cmd.Process.Signal(os.Kill)
	}
}
