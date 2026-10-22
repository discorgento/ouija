//go:build linux

package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"

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
	// Landlock filters TCP and UDP only: without the seccomp filter, which
	// knows x86-64 and arm64, ICMP, SCTP and MPTCP would carry data out of a
	// closed sandbox.
	if _, ok := seccompArches[runtime.GOARCH]; !ok {
		return fmt.Errorf("%w: the sandbox's socket filter does not support %s", errSandboxUnsupported, runtime.GOARCH)
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
	if err := cfg.BestEffort().Restrict(rules...); err != nil {
		return err
	}
	// Landlock filters only TCP and UDP: seccomp refuses every other socket,
	// and UDP too where no port is open — and, whatever the network, reading
	// another process's memory.
	net := socketsPorts
	switch {
	case p.NetAll:
		net = socketsAll
	case len(p.ConnectTCP)+len(p.BindTCP) == 0:
		net = socketsClosed
	}
	return confineSockets(net)
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

// confinedShell runs the command under Landlock, through a re-exec, in a
// PID namespace of its own where the kernel allows one.
func confinedShell(ctx context.Context, policy sandboxPolicy, command string) (*exec.Cmd, error) {
	cmd, err := reexecShell(ctx, policy, command)
	if err == nil && pidNamespaceAvailable() {
		ownPIDNamespace(cmd)
	}
	return cmd, err
}

// ownPIDNamespace starts cmd in new user and PID namespaces: the shell is
// PID 1 there, and when it exits the kernel kills every process left in the
// namespace before the wait returns — a job that called setsid or forked
// twice, out of the process group's reach, included. The user namespace
// maps the user to itself only, so files keep their owner and the shell,
// started as that user, holds no capabilities.
func ownPIDNamespace(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	uid, gid := os.Getuid(), os.Getgid()
	cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWUSER | syscall.CLONE_NEWPID
	cmd.SysProcAttr.UidMappings = []syscall.SysProcIDMap{{ContainerID: uid, HostID: uid, Size: 1}}
	cmd.SysProcAttr.GidMappings = []syscall.SysProcIDMap{{ContainerID: gid, HostID: gid, Size: 1}}
}

// pidNamespaceAvailable reports whether this user may create the namespaces
// ownPIDNamespace asks for: some distributions (Ubuntu's AppArmor policy),
// containers and kernel.unprivileged_userns_clone=0 forbid it, and then the
// process group is the only bound on the command's processes.
var pidNamespaceAvailable = sync.OnceValue(func() bool {
	shell, err := posixShell()
	if err != nil {
		return false
	}
	cmd := exec.Command(shell, "-c", ":")
	ownPIDNamespace(cmd)
	return cmd.Run() == nil
})
