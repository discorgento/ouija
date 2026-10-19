package engine

// The bash sandbox (opt-in: tools.bash.sandbox). The static bash gates read a
// command's text, and a path built at run time escapes any reading of text.
// With the sandbox on, a command runs in a re-executed copy of the current
// binary that first confines itself with the OS — on Linux, Landlock: write
// only inside the project and the temp dir, read only system directories, the
// project and the paths the config lists, so never $HOME's credentials — and
// then execs the shell. The confinement is inherited by every process the
// command starts and cannot be lifted.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

const (
	// sandboxExecArg marks the re-executed child (os.Args[1]).
	sandboxExecArg = "__ouija-sandboxed-exec"
	// sandboxPolicyEnv carries the child's policy; it is removed before the
	// shell starts.
	sandboxPolicyEnv = "OUIJA_SANDBOX_POLICY"
)

// sandboxPolicy is everything the confined command may touch.
type sandboxPolicy struct {
	ReadDirs   []string `json:"readDirs"`   // read and execute, recursively
	ReadFiles  []string `json:"readFiles"`  // read single files
	WriteDirs  []string `json:"writeDirs"`  // read and write, recursively
	WriteFiles []string `json:"writeFiles"` // read and write single files (devices)
}

// The child side runs before main, from any binary that links the engine
// (the ouija CLI, ouija-mcp, an external totem host), so the re-exec works
// without the host's cooperation.
func init() {
	if len(os.Args) > 2 && os.Args[1] == sandboxExecArg {
		os.Exit(runSandboxedChild(os.Args[2:]))
	}
}

// runSandboxedChild applies the policy from the environment, then execs argv
// (the shell). It only returns on failure.
func runSandboxedChild(argv []string) int {
	var p sandboxPolicy
	if err := json.Unmarshal([]byte(os.Getenv(sandboxPolicyEnv)), &p); err != nil {
		fmt.Fprintf(os.Stderr, "ouija sandbox: bad policy: %v\n", err)
		return 126
	}
	os.Unsetenv(sandboxPolicyEnv)
	if err := applySandbox(p); err != nil {
		fmt.Fprintf(os.Stderr, "ouija sandbox: %v\n", err)
		return 126
	}
	if err := syscall.Exec(argv[0], argv, os.Environ()); err != nil {
		fmt.Fprintf(os.Stderr, "ouija sandbox: exec %s: %v\n", argv[0], err)
		return 126
	}
	return 0 // unreachable
}

// sandboxedShell builds the command that runs `sh -c command` confined for a
// project at root.
func sandboxedShell(ctx context.Context, root string, sb *SandboxConfig, command string) (*exec.Cmd, error) {
	if err := sandboxAvailable(); err != nil {
		return nil, err
	}
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate the ouija binary: %w", err)
	}
	policy, err := buildSandboxPolicy(root, sb)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, self, sandboxExecArg, "/bin/sh", "-c", command)
	cmd.Env = append(sanitizeEnv(), sandboxPolicyEnv+"="+string(b))
	return cmd, nil
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

// buildSandboxPolicy resolves the policy for root and the config's extra
// paths (`~/` is the home directory; relative paths are relative to root).
func buildSandboxPolicy(root string, sb *SandboxConfig) (sandboxPolicy, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return sandboxPolicy{}, err
	}
	p := sandboxPolicy{
		ReadDirs:   append([]string(nil), sandboxReadDirs...),
		ReadFiles:  append([]string(nil), sandboxReadFiles...),
		WriteDirs:  []string{rootAbs, os.TempDir()},
		WriteFiles: append([]string(nil), sandboxWriteFiles...),
	}
	// DNS: /etc/resolv.conf often links into /run, which is not readable.
	if target, err := filepath.EvalSymlinks("/etc/resolv.conf"); err == nil {
		p.ReadFiles = append(p.ReadFiles, target)
	}
	expand := func(paths []string) ([]string, error) {
		var out []string
		for _, raw := range paths {
			path := raw
			if path == "~" || strings.HasPrefix(path, "~/") {
				home, err := os.UserHomeDir()
				if err != nil {
					return nil, err
				}
				path = filepath.Join(home, path[1:])
			} else if !filepath.IsAbs(path) {
				path = filepath.Join(rootAbs, path)
			}
			if isProtectedPath(path) {
				return nil, fmt.Errorf("sandbox path %q is a protected location (credentials, ouija's config)", raw)
			}
			out = append(out, path)
		}
		return out, nil
	}
	if sb != nil {
		read, err := expand(sb.Read)
		if err != nil {
			return sandboxPolicy{}, err
		}
		write, err := expand(sb.Write)
		if err != nil {
			return sandboxPolicy{}, err
		}
		p.ReadDirs = append(p.ReadDirs, read...)
		p.WriteDirs = append(p.WriteDirs, write...)
	}
	return p, nil
}

// errSandboxUnsupported is returned where the OS cannot confine a command.
var errSandboxUnsupported = errors.New("the bash sandbox needs Linux with Landlock")
