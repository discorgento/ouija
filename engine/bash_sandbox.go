package engine

// The bash sandbox (on by default where the OS has one; tools.bash.sandbox).
// The static bash gates read a command's text, and a path built at run time
// escapes any reading of text. With the sandbox on, the OS confines the
// command — on macOS with Seatbelt (sandbox-exec), on Linux in a re-executed
// copy of the current binary that first confines itself with Landlock: write
// only inside the project and the temp dir, read only system directories, the
// project and the paths the config lists, so never $HOME's credentials; no TCP
// beyond the ports the config lists — and then execs the shell. The confinement is inherited by every process the
// command starts and cannot be lifted.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
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

	NetAll     bool     `json:"netAll"`     // TCP is not restricted
	ConnectTCP []uint16 `json:"connectTCP"` // ports it may connect to
	BindTCP    []uint16 `json:"bindTCP"`    // ports it may listen on

	// Env points the command at its private temp and cache directories.
	// The parent sets it; the child never needs it.
	Env []string `json:"-"`
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
	if err := checkSandbox(); err != nil {
		return nil, err
	}
	policy, err := buildSandboxPolicy(root, sb)
	if err != nil {
		return nil, err
	}
	cmd, err := confinedShell(ctx, policy, command)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(append(cmd.Env, policy.Env...), sandboxGitEnv()...)
	return cmd, nil
}

// sandboxGitEnv points git away from the global config the sandbox cannot
// read: an unreadable ~/.gitconfig is fatal to git ("unknown error occurred
// while reading the configuration files"), and opening it up would hand out
// what it may hold (a token in url.insteadOf). The user's name and email,
// read outside the sandbox, come along so commits keep their author.
func sandboxGitEnv() []string {
	if _, set := os.LookupEnv("GIT_CONFIG_GLOBAL"); set {
		return nil // the user chose a global config already
	}
	env := []string{"GIT_CONFIG_GLOBAL=" + os.DevNull}
	if _, set := os.LookupEnv("GIT_CONFIG_COUNT"); set {
		return env
	}
	name, email := gitIdentity()
	var keys, values []string
	if name != "" {
		keys, values = append(keys, "user.name"), append(values, name)
	}
	if email != "" {
		keys, values = append(keys, "user.email"), append(values, email)
	}
	for i := range keys {
		env = append(env, fmt.Sprintf("GIT_CONFIG_KEY_%d=%s", i, keys[i]), fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", i, values[i]))
	}
	if len(keys) > 0 {
		env = append(env, fmt.Sprintf("GIT_CONFIG_COUNT=%d", len(keys)))
	}
	return env
}

// gitIdentity is the user's git user.name and user.email, read once.
var gitIdentity = sync.OnceValues(func() (string, string) {
	get := func(key string) string {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "git", "config", "--get", key)
		cmd.Dir = os.TempDir() // no repository: the user's own settings only
		cmd.Env = sanitizeEnv()
		out, err := cmd.Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	return get("user.name"), get("user.email")
})

// reexecShell is the Linux confinement: the command runs in a re-executed
// copy of this binary that applies the policy (passed in the environment) to
// itself, then execs the shell.
func reexecShell(ctx context.Context, policy sandboxPolicy, command string) (*exec.Cmd, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate the ouija binary: %w", err)
	}
	b, err := json.Marshal(policy)
	if err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, self, sandboxExecArg, "/bin/sh", "-c", command)
	cmd.Env = append(sanitizeEnv(), sandboxPolicyEnv+"="+string(b))
	return cmd, nil
}

// sandboxHomeReadDirs are the parts of the toolchains people install in
// their home directory (Go's module cache, rustup, nvm, pyenv, …) that a build
// or test reads, so it works in the sandbox. Only these, and never a whole
// tool directory: ~/.cargo holds credentials.toml, ~/.m2 settings.xml,
// ~/.gradle gradle.properties, ~/.cache a Hugging Face token. The rest of the
// home directory, credentials and browser profiles among it, stays out of
// reach.
var sandboxHomeReadDirs = []string{
	"go/pkg/mod", "go/bin", ".cargo/bin", ".cargo/registry", ".cargo/git", ".rustup",
	// Version managers: each installed version's bin and lib, not its etc
	// (npm's global npmrc, with an _authToken, lives in <version>/etc).
	".nvm/versions/node/*/bin", ".nvm/versions/node/*/lib", ".nvm/versions/node/*/include",
	".fnm/node-versions/*/installation/bin", ".fnm/node-versions/*/installation/lib",
	".local/share/fnm/node-versions/*/installation/bin", ".local/share/fnm/node-versions/*/installation/lib",
	".volta/bin", ".volta/tools/image/*/*/bin", ".volta/tools/image/*/*/lib",
	".asdf/shims", ".asdf/installs/*/*/bin", ".asdf/installs/*/*/lib",
	".local/share/mise/installs/*/*/bin", ".local/share/mise/installs/*/*/lib",
	".pyenv/shims", ".pyenv/versions/*/bin", ".pyenv/versions/*/lib", ".pyenv/versions/*/include",
	".rbenv/shims", ".rbenv/versions/*/bin", ".rbenv/versions/*/lib",
	".sdkman/candidates/*/*/bin", ".sdkman/candidates/*/*/lib",
	".bun/bin", ".deno/bin", ".gradle/caches", ".gradle/wrapper", ".m2/repository",
	".local/bin", ".local/lib", ".local/share/pnpm", ".local/share/uv/python", ".local/share/uv/tools",
	"Library/pnpm",
}

// sandboxHomeReadFiles are single files in the home directory git reads that
// hold no secrets: the global ignore and attributes files (git warns about
// every one it cannot read, and the user's ignores keep applying).
var sandboxHomeReadFiles = []string{
	".config/git/ignore", ".config/git/attributes", ".gitignore_global", ".gitignore",
}

// privateTemps holds each sandbox temp directory made, by the system temp
// directory it lives in.
var privateTemps struct {
	sync.Mutex
	dirs map[string]string
}

// sandboxTemp is the confined commands' private temp directory: one per ouija
// process, made 0700 inside the system's. The shared /tmp held other
// programs' sockets (an ssh-agent) and files, and what one command left there
// another process of the user's could pick up.
func sandboxTemp() (string, error) {
	privateTemps.Lock()
	defer privateTemps.Unlock()
	base := os.TempDir()
	if d, ok := privateTemps.dirs[base]; ok {
		if fi, err := os.Lstat(d); err == nil && fi.IsDir() {
			return d, nil
		}
	}
	d, err := os.MkdirTemp(base, "ouija-sandbox-")
	if err != nil {
		return "", err
	}
	if privateTemps.dirs == nil {
		privateTemps.dirs = map[string]string{}
	}
	privateTemps.dirs[base] = d
	return d, nil
}

// sandboxCache is the project's private build cache: Go's, npm's, pip's and
// every XDG_CACHE_HOME user's. The user's real caches stay read-only to the
// sandbox: tools run later outside it trust them (go build reuses a cached
// object without rehashing, npx and pre-commit run cached code), so a
// writable one let a confined command plant code that escapes it. Only
// confined runs read this one.
func sandboxCache(rootAbs string) (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || !filepath.IsAbs(base) {
		t, err := sandboxTemp()
		if err != nil {
			return "", err
		}
		base = t
	}
	sum := sha256.Sum256([]byte(rootAbs))
	dir := filepath.Join(base, "ouija", "sandbox", hex.EncodeToString(sum[:8]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

// sandboxCacheEnv points the common tools' caches into dir.
func sandboxCacheEnv(dir string) []string {
	return []string{
		"XDG_CACHE_HOME=" + dir,
		"GOCACHE=" + filepath.Join(dir, "go-build"),
		"npm_config_cache=" + filepath.Join(dir, "npm"),
		"YARN_CACHE_FOLDER=" + filepath.Join(dir, "yarn"),
		"PIP_CACHE_DIR=" + filepath.Join(dir, "pip"),
		"PRE_COMMIT_HOME=" + filepath.Join(dir, "pre-commit"),
		"BUN_INSTALL_CACHE_DIR=" + filepath.Join(dir, "bun"),
		"DENO_DIR=" + filepath.Join(dir, "deno"),
	}
}

// buildSandboxPolicy resolves the policy for root and the config's extra
// paths (`~/` is the home directory; relative paths are relative to root).
func buildSandboxPolicy(root string, sb *SandboxConfig) (sandboxPolicy, error) {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return sandboxPolicy{}, err
	}
	if err := checkSandboxGrant(rootAbs); err != nil {
		return sandboxPolicy{}, fmt.Errorf("project root %s: %w", rootAbs, err)
	}
	tmp, err := sandboxTemp()
	if err != nil {
		return sandboxPolicy{}, fmt.Errorf("sandbox temp dir: %w", err)
	}
	cache, err := sandboxCache(rootAbs)
	if err != nil {
		return sandboxPolicy{}, fmt.Errorf("sandbox cache dir: %w", err)
	}
	p := sandboxPolicy{
		ReadDirs:   append([]string(nil), sandboxReadDirs...),
		ReadFiles:  append([]string(nil), sandboxReadFiles...),
		WriteDirs:  []string{rootAbs, tmp, cache},
		WriteFiles: append([]string(nil), sandboxWriteFiles...),
		Env:        append([]string{"TMPDIR=" + tmp}, sandboxCacheEnv(cache)...),
	}
	if home, err := os.UserHomeDir(); err == nil && checkSandboxGrant(home) != nil {
		// Only when the home directory itself is out of reach (it always is:
		// it holds .ssh): its toolchains, never the rest of it.
		for _, d := range sandboxHomeReadDirs {
			paths := []string{filepath.Join(home, filepath.FromSlash(d))}
			if strings.Contains(d, "*") {
				paths, _ = filepath.Glob(paths[0]) // the versions installed now
			}
			for _, path := range paths {
				// Landlock grants a symlink's target: ~/.local/lib linked to
				// ~/.config would hand out what is there. Each grant is its
				// real path, and one that reaches credentials is dropped.
				real, err := filepath.EvalSymlinks(path)
				if err != nil || checkSandboxGrant(real) != nil {
					continue
				}
				p.ReadDirs = append(p.ReadDirs, real)
			}
		}
		for _, f := range sandboxHomeReadFiles {
			p.ReadFiles = append(p.ReadFiles, filepath.Join(home, filepath.FromSlash(f)))
		}
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
			if err := checkSandboxGrant(path); err != nil {
				return nil, fmt.Errorf("sandbox path %q: %w", raw, err)
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
		if n := sb.Network; n != nil {
			p.NetAll = n.All
			p.ConnectTCP = append(p.ConnectTCP, n.Connect...)
			p.BindTCP = append(p.BindTCP, n.Bind...)
		}
	}
	return p, nil
}

// guardedDirs are the credential directories a sandbox grant must never
// contain: a grant covers its whole subtree, so allowing `~` or `~/.config`
// (or running with the home directory as the project root) opened them all.
var guardedDirs = []string{".ssh", ".gnupg", ".aws", ".docker", ".kube", ".azure",
	filepath.Join(".config", "ouija"), filepath.Join(".config", "gcloud"), filepath.Join(".config", "gh")}

// checkSandboxGrant refuses a path that is protected itself, or is the
// filesystem root or an ancestor of a credential directory.
func checkSandboxGrant(path string) error {
	clean := filepath.Clean(path)
	if isProtectedPath(clean) {
		return errors.New("a protected location (credentials, ouija's config)")
	}
	if clean == filepath.Dir(clean) { // the filesystem root
		return errors.New("the whole filesystem")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	guarded := []string{}
	for _, d := range guardedDirs {
		guarded = append(guarded, filepath.Join(home, d))
	}
	if dir, err := os.UserConfigDir(); err == nil {
		guarded = append(guarded, filepath.Join(dir, "ouija"), filepath.Join(dir, "gcloud"))
	}
	for _, g := range guarded {
		if rel, err := filepath.Rel(clean, g); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("it contains %s, which the sandbox must keep out of reach", g)
		}
	}
	return nil
}

// checkSandbox is sandboxAvailable; tests replace it to play an OS without one.
var checkSandbox = sandboxAvailable

// SandboxAvailable reports whether this machine can enforce the bash sandbox
// (Linux with a recent enough Landlock), and why not when it cannot.
func SandboxAvailable() error { return sandboxAvailable() }

// errSandboxUnsupported is returned where the OS cannot confine a command.
var errSandboxUnsupported = errors.New("the bash sandbox needs Linux (Landlock) or macOS (Seatbelt)")
