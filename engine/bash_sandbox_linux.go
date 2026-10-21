//go:build linux

package engine

import (
	"fmt"

	"github.com/landlock-lsm/go-landlock/landlock"
	llsyscall "github.com/landlock-lsm/go-landlock/landlock/syscall"
)

// minSandboxABI is the oldest Landlock ABI the sandbox accepts: V9 is the
// first that confines connecting to Unix sockets by path. Below it a confined
// command could still reach the ssh-agent or tmux socket under /tmp and act
// outside the sandbox — refusing is better than a sandbox in name only.
const minSandboxABI = 9

// sandboxAvailable fails closed: a sandbox the user asked for but the kernel
// cannot enforce is an error, never a silent unconfined run.
func sandboxAvailable() error {
	v, err := llsyscall.LandlockGetABIVersion()
	if err != nil {
		return fmt.Errorf("%w: Landlock is not available on this kernel (%v)", errSandboxUnsupported, err)
	}
	if v < minSandboxABI {
		return fmt.Errorf("%w: Landlock ABI %d is too old, need %d (a recent kernel: older ones cannot confine Unix sockets)", errSandboxUnsupported, v, minSandboxABI)
	}
	return nil
}

// applySandbox confines the current process (all its threads) to p, with the
// strongest Landlock ABI the kernel offers and at least minSandboxABI, which
// sandboxAvailable checked in the parent: the filesystem (connecting to Unix
// sockets by path included) and the scopes — abstract Unix sockets and
// signals to processes outside the sandbox, which RestrictPaths alone left
// open (an X11 abstract socket, `kill` of any of the user's processes) — and,
// unless the policy allows all of it, TCP: connecting and listening only on
// the ports it lists (none by default). UDP is not filtered by Landlock.
func applySandbox(p sandboxPolicy) error {
	cfg := landlock.Config{HandledAccessFS: landlock.V10.HandledAccessFS, Scoped: landlock.V10.Scoped}
	rules := []landlock.Rule{
		landlock.RODirs(p.ReadDirs...).IgnoreIfMissing(),
		landlock.ROFiles(p.ReadFiles...).IgnoreIfMissing(),
		landlock.RWDirs(p.WriteDirs...).IgnoreIfMissing(),
		landlock.RWFiles(p.WriteFiles...).IgnoreIfMissing(),
	}
	if !p.NetAll {
		cfg.HandledAccessNet = landlock.V10.HandledAccessNet
		for _, port := range p.ConnectTCP {
			rules = append(rules, landlock.ConnectTCP(port))
		}
		for _, port := range p.BindTCP {
			rules = append(rules, landlock.BindTCP(port))
		}
	}
	return cfg.BestEffort().Restrict(rules...)
}
