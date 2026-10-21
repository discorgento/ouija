//go:build linux

package engine

import (
	"context"
	"fmt"
	"os/exec"

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
// the ports it lists (none by default), and since ABI v10 UDP as well: none
// by default, DNS once any TCP port is open.
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
		// Since ABI v10 Landlock filters UDP too, so a closed sandbox sends
		// no DNS query either (no data out through DNS). A port opened for a
		// download needs names resolved: DNS (UDP 53) comes with it, and
		// BindUDP(0) lets the kernel give the query socket its port.
		if len(p.ConnectTCP)+len(p.BindTCP) > 0 {
			rules = append(rules, landlock.BindUDP(0), landlock.ConnectSendUDP(53))
		}
	}
	return cfg.BestEffort().Restrict(rules...)
}

// sandboxReadDirs are the system directories a shell and common tools need.
// Missing ones are skipped. /run and /proc are deliberately absent: /run
// holds sockets (ssh-agent, docker — root-equivalent) and /proc/<pid>/environ
// would expose the environment of the user's shell.
var sandboxReadDirs = []string{
	"/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/libx32",
	"/etc", "/opt", "/sys", "/nix", "/snap", "/proc/sys",
}

// sandboxReadFiles are single files tools commonly read.
var sandboxReadFiles = []string{
	"/proc/cpuinfo", "/proc/meminfo", "/proc/stat", "/proc/loadavg",
	"/proc/version", "/proc/filesystems", "/proc/mounts", "/proc/self/maps",
}

// sandboxWriteFiles are the devices a shell writes to.
var sandboxWriteFiles = []string{
	"/dev/null", "/dev/zero", "/dev/full", "/dev/random", "/dev/urandom", "/dev/tty",
}

// confinedShell runs the command under Landlock, through a re-exec.
func confinedShell(ctx context.Context, policy sandboxPolicy, command string) (*exec.Cmd, error) {
	return reexecShell(ctx, policy, command)
}
