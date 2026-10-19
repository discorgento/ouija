//go:build !linux

package engine

// sandboxAvailable fails closed outside Linux: a sandbox the user asked for
// is never replaced by an unconfined run.
func sandboxAvailable() error { return errSandboxUnsupported }

func applySandbox(sandboxPolicy) error { return errSandboxUnsupported }
