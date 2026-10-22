//go:build !unix

package engine

import "os/exec"

// ownProcessGroup is a no-op where there are no Unix process groups: on
// Windows a background job can outlive the command (see
// bash_sandbox_pgroup_unix.go).
func ownProcessGroup(*exec.Cmd) {}

func killProcessGroup(*exec.Cmd) {}
