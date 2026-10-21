//go:build !linux && !darwin

package engine

import (
	"context"
	"os/exec"
)

// sandboxAvailable fails closed where the OS has no sandbox ouija can use: a
// sandbox the user asked for is never replaced by an unconfined run.
func sandboxAvailable() error { return errSandboxUnsupported }

func applySandbox(sandboxPolicy) error { return errSandboxUnsupported }

func confinedShell(context.Context, sandboxPolicy, string) (*exec.Cmd, error) {
	return nil, errSandboxUnsupported
}

var sandboxReadDirs, sandboxReadFiles, sandboxWriteFiles []string
