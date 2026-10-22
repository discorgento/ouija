//go:build darwin

package engine

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sandboxExec is macOS's Seatbelt front end. Apple marks it deprecated, but
// it ships with every macOS and is what other coding agents confine their
// commands with; the profile language is the one the system's own daemons use.
const sandboxExec = "/usr/bin/sandbox-exec"

// sandboxReadDirs are the system directories a shell and common tools need:
// the system, Homebrew (/opt/homebrew, /usr/local) and the developer tools
// git and make run from. Seatbelt matches real paths, so /etc is
// /private/etc.
var sandboxReadDirs = []string{
	"/usr", "/bin", "/sbin", "/System", "/Library", "/opt", "/Applications/Xcode.app",
	"/private/etc", "/private/var/db/timezone", "/private/var/select", "/nix",
}

// sandboxReadFiles are single files tools commonly read (none on macOS).
var sandboxReadFiles []string

// sandboxWriteFiles are the devices a shell writes to.
var sandboxWriteFiles = []string{
	"/dev/null", "/dev/zero", "/dev/random", "/dev/urandom", "/dev/tty", "/dev/dtracehelper",
}

func sandboxAvailable() error {
	if _, err := os.Stat(sandboxExec); err != nil {
		return fmt.Errorf("%w: %s is missing", errSandboxUnsupported, sandboxExec)
	}
	return nil
}

// applySandbox is the Linux re-exec's half; macOS confines through
// sandbox-exec instead.
func applySandbox(sandboxPolicy) error { return errSandboxUnsupported }

// confinedShell runs the command under a Seatbelt profile built from the
// policy. The paths go in as -D parameters, never into the profile's text, so
// a quote in a path cannot change the profile.
func confinedShell(ctx context.Context, policy sandboxPolicy, command string) (*exec.Cmd, error) {
	profile, params := seatbeltProfile(policy)
	args := []string{"-p", profile}
	for i, p := range params {
		args = append(args, "-D", fmt.Sprintf("P%d=%s", i, p))
	}
	args = append(args, "/bin/sh", "-c", command)
	cmd := exec.CommandContext(ctx, sandboxExec, args...)
	cmd.Env = sanitizeEnv()
	return cmd, nil
}

// seatbeltBase is what every confined command may do besides the policy's
// paths: run and fork, signal and inspect only processes in its own sandbox,
// stat any path (resolving a path needs its parents' metadata, not their
// contents), and reach the two system services a shell and compilers need —
// the same short list OpenAI's Codex grants. No LaunchServices and no Apple
// events: through them `open` and `osascript` start programs outside the
// sandbox (`open -a Terminal x.command` runs code unconfined, `open URL`
// sends data out with the network off), so they are denied even if a later
// rule allowed them. No preferences daemon either: it serves every app's
// settings. The sysctls are Codex's list: reading every one let a command see
// other processes' arguments (kern.procargs2). A terminal is only one the
// sandbox opened itself (com.apple.sandbox.pty): every /dev/ttysN let a
// command read the keystrokes of the user's other terminal tabs.
const seatbeltBase = `(version 1)
(deny default)
(allow process-exec process-fork)
(allow signal (target same-sandbox))
(allow process-info* (target same-sandbox))
(allow sysctl-read
  (sysctl-name "hw.activecpu") (sysctl-name "hw.busfrequency_compat") (sysctl-name "hw.byteorder")
  (sysctl-name "hw.cacheconfig") (sysctl-name "hw.cachelinesize_compat") (sysctl-name "hw.cpufamily")
  (sysctl-name "hw.cpufrequency_compat") (sysctl-name "hw.cputype") (sysctl-name "hw.l1dcachesize_compat")
  (sysctl-name "hw.l1icachesize_compat") (sysctl-name "hw.l2cachesize_compat") (sysctl-name "hw.l3cachesize_compat")
  (sysctl-name "hw.logicalcpu_max") (sysctl-name "hw.machine") (sysctl-name "hw.model") (sysctl-name "hw.memsize")
  (sysctl-name "hw.ncpu") (sysctl-name "hw.nperflevels") (sysctl-name-prefix "hw.optional.arm.")
  (sysctl-name-prefix "hw.optional.armv8_") (sysctl-name "hw.packages") (sysctl-name "hw.pagesize_compat")
  (sysctl-name "hw.pagesize") (sysctl-name "hw.physicalcpu") (sysctl-name "hw.physicalcpu_max")
  (sysctl-name "hw.logicalcpu") (sysctl-name "hw.cpufrequency") (sysctl-name "hw.tbfrequency_compat")
  (sysctl-name "hw.vectorunit") (sysctl-name "machdep.cpu.brand_string") (sysctl-name "kern.argmax")
  (sysctl-name "kern.hostname") (sysctl-name "kern.maxfilesperproc") (sysctl-name "kern.maxproc")
  (sysctl-name "kern.osproductversion") (sysctl-name "kern.osrelease") (sysctl-name "kern.ostype")
  (sysctl-name "kern.osvariant_status") (sysctl-name "kern.osversion") (sysctl-name "kern.secure_kernel")
  (sysctl-name "kern.sysv.semmns") (sysctl-name "kern.usrstack64") (sysctl-name "kern.version")
  (sysctl-name "sysctl.proc_cputype") (sysctl-name "vm.loadavg") (sysctl-name-prefix "hw.perflevel")
  (sysctl-name-prefix "kern.proc.pgrp.") (sysctl-name-prefix "kern.proc.pid.") (sysctl-name-prefix "net.routetable."))
(allow sysctl-write (sysctl-name "kern.grade_cputype"))
(allow file-read-metadata)
(allow file-read* (literal "/"))
(allow pseudo-tty)
(allow file-read* file-write* file-ioctl (literal "/dev/ptmx"))
(allow file-read* file-write* (require-all (regex #"^/dev/ttys[0-9]+") (extension "com.apple.sandbox.pty")))
(allow file-ioctl (regex #"^/dev/ttys[0-9]+"))
(allow file-read-data file-test-existence file-write-data (subpath "/dev/fd"))
(allow ipc-posix-sem)
(allow iokit-open (iokit-registry-entry-class "RootDomainUserClient"))
(allow mach-lookup
  (global-name "com.apple.system.opendirectoryd.libinfo")
  (global-name "com.apple.PowerManagement.control")
  (global-name "com.apple.bsd.dirhelper")
  (global-name "com.apple.system.notification_center"))
`

// seatbeltNet is what a command that may use the network also needs: DNS,
// TLS certificate checks and the network configuration (Codex's network set).
const seatbeltNet = `(allow system-socket (socket-domain AF_UNIX))
(allow mach-lookup
  (global-name "com.apple.system.opendirectoryd.membership")
  (global-name "com.apple.SecurityServer")
  (global-name "com.apple.networkd")
  (global-name "com.apple.ocspd")
  (global-name "com.apple.trustd.agent")
  (global-name "com.apple.SystemConfiguration.DNSConfiguration")
  (global-name "com.apple.SystemConfiguration.configd"))
(allow network-outbound (literal "/private/var/run/mDNSResponder"))
`

// seatbeltDeny comes last, so no allow above can override it (Seatbelt's
// last match wins): starting programs through LaunchServices or Apple events,
// the Docker socket (root on the host), and the fcntls that change a file
// through a read-only descriptor, past file-write* (Codex denies them too).
const seatbeltDeny = `(deny lsopen)
(deny appleevent-send)
(deny process-exec (literal "/usr/bin/open") (literal "/usr/bin/osascript") (literal "/usr/bin/osacompile"))
(deny network-outbound (literal "/var/run/docker.sock") (literal "/private/var/run/docker.sock") (regex #"/\.docker/run/docker\.sock$") (regex #"/\.colima/.*\.sock$") (regex #"/\.orbstack/run/.*\.sock$"))
(deny system-fcntl (fcntl-command 80 110))
`

// seatbeltProfile renders the policy as a Seatbelt profile and the paths it
// names, in parameter order (P0, P1, …).
func seatbeltProfile(p sandboxPolicy) (string, []string) {
	var b strings.Builder
	b.WriteString(seatbeltBase)
	var params []string
	rule := func(access, kind string, paths []string) {
		var refs []string
		for _, path := range paths {
			if _, err := os.Stat(path); err != nil {
				continue // like Landlock's IgnoreIfMissing
			}
			if real, err := filepath.EvalSymlinks(path); err == nil {
				path = real // Seatbelt sees /private/var, not /var
			}
			refs = append(refs, fmt.Sprintf(`(%s (param "P%d"))`, kind, len(params)))
			params = append(params, path)
		}
		if len(refs) > 0 {
			fmt.Fprintf(&b, "(allow %s %s)\n", access, strings.Join(refs, " "))
		}
	}
	// file-map-executable: dyld maps every library outside the shared cache
	// (Homebrew's, a Python .so, a node addon, a test binary) through it.
	rule("file-read* file-map-executable", "subpath", p.ReadDirs)
	rule("file-read*", "literal", p.ReadFiles)
	rule("file-read* file-write* file-map-executable", "subpath", p.WriteDirs)
	rule("file-read* file-write* file-ioctl", "literal", p.WriteFiles)

	switch {
	case p.NetAll:
		// Any IP traffic, but not Unix sockets: (allow network*) also opened
		// every local socket, the Docker daemon's among them.
		b.WriteString(seatbeltNet)
		b.WriteString("(allow network-outbound (remote ip))\n(allow network-bind network-inbound (local ip))\n")
	case len(p.ConnectTCP)+len(p.BindTCP) > 0:
		b.WriteString(seatbeltNet)
		for _, port := range p.ConnectTCP {
			fmt.Fprintf(&b, "(allow network-outbound (remote tcp \"*:%d\"))\n", port)
		}
		for _, port := range p.BindTCP {
			fmt.Fprintf(&b, "(allow network-bind network-inbound (local tcp \"*:%d\"))\n", port)
		}
	}
	// What git runs on its own stays read-only in the project (the first
	// write dir): the hooks, the config and what points git at other ones
	// (commondir, config.worktree, a submodule's or worktree's git dir).
	// .git itself may not be moved or made: `mv .git g`, a write to
	// g/hooks, `mv g .git` relocated the hooks past the path-based deny
	// (Codex: "Renaming an allowed ancestor relocates its protected
	// descendants past their pathname carveouts").
	if len(p.WriteDirs) > 0 {
		project := p.WriteDirs[0]
		if real, err := filepath.EvalSymlinks(project); err == nil {
			project = real
		}
		param := func(path string) string {
			params = append(params, path)
			return fmt.Sprintf(`(param "P%d")`, len(params)-1)
		}
		git := filepath.Join(project, ".git")
		dotGit := param(git)
		fmt.Fprintf(&b, "(deny file-write* (subpath %s) (literal %s) (literal %s) (literal %s) (subpath %s) (subpath %s))\n",
			param(filepath.Join(git, "hooks")), param(filepath.Join(git, "config")),
			param(filepath.Join(git, "commondir")), param(filepath.Join(git, "config.worktree")),
			param(filepath.Join(git, "modules")), param(filepath.Join(git, "worktrees")))
		fmt.Fprintf(&b, "(deny file-write-unlink (require-all (vnode-type DIRECTORY) (literal %s)))\n", dotGit)
		fmt.Fprintf(&b, "(deny file-write-create (literal %s))\n", dotGit)
	}
	b.WriteString(seatbeltDeny)
	return b.String(), params
}

// pidNamespaceAvailable: there are no PID namespaces here.
func pidNamespaceAvailable() bool { return false }
