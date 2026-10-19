//go:build linux

package engine

import (
	"fmt"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsyscall "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

// minSandboxABI is the oldest Landlock ABI the sandbox accepts: V3 (Linux 6.2)
// also restricts truncating files outside the writable directories.
const minSandboxABI = 3

// sandboxAvailable fails closed: a sandbox the user asked for but the kernel
// cannot enforce is an error, never a silent unconfined run.
func sandboxAvailable() error {
	v, err := llsyscall.LandlockGetABIVersion()
	if err != nil {
		return fmt.Errorf("%w: Landlock is not available on this kernel (%v)", errSandboxUnsupported, err)
	}
	if v < minSandboxABI {
		return fmt.Errorf("%w: Landlock ABI %d is too old, need %d (Linux 6.2+)", errSandboxUnsupported, v, minSandboxABI)
	}
	return nil
}

// applySandbox confines the current process (all its threads) to p, with the
// strongest Landlock ABI the kernel offers — on recent kernels that also
// covers connecting to Unix sockets — and at least minSandboxABI, which
// sandboxAvailable checked in the parent.
func applySandbox(p sandboxPolicy) error {
	return landlock.V10.BestEffort().RestrictPaths(
		landlock.RODirs(p.ReadDirs...).IgnoreIfMissing(),
		landlock.ROFiles(p.ReadFiles...).IgnoreIfMissing(),
		landlock.RWDirs(p.WriteDirs...).IgnoreIfMissing(),
		landlock.RWFiles(p.WriteFiles...).IgnoreIfMissing(),
	)
}
