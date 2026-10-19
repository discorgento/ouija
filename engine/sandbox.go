package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// escapeKey is used with context.Context to pass a pre-approved sandbox escape
// path through the tool call chain, replacing the unsafe global toolEscapeAbs.
type escapeKey struct{}

// SafeJoin joins rel onto root and guarantees the result stays within root
// (lexical containment). It does NOT require the path to exist, so it is also
// safe for output paths that will be created.
func SafeJoin(root, rel string) (string, error) {
	if err := checkPathSyntax(rel); err != nil {
		return "", err
	}
	rootAbs, err := canonRoot(root)
	if err != nil {
		return "", err
	}
	full, err := filepath.Abs(filepath.Join(rootAbs, rel))
	if err != nil {
		return "", err
	}
	if !within(rootAbs, full) {
		return "", fmt.Errorf("path escapes the project root: %s", rel)
	}
	return full, nil
}

// resolve is SafeJoin plus a physical symlink check: a symlink inside the root
// must not point read access outside it.
// It also enforces the protected-path policy (credentials, system files): a
// path inside the project that resolves to a protected location is rejected
// even when lexically contained — the ToolRunner permission gate only sees
// `input["path"]`, so this is what makes the policy hold for every tool that
// resolves a path (list_dir, grep, previews…) and for symlink indirection.
// When a path escapes the sandbox but a pre-approved escape is attached to ctx
// (via escapeKey{}), the override path is returned instead: that is an explicit,
// per-path user consent shown in full, which outranks the default policy.
func resolve(ctx context.Context, root, p string) (string, error) {
	full, err := SafeJoin(root, p)
	if err != nil {
		// Lexical escape — check for pre-approved escape in context
		if escaped, _ := ctx.Value(escapeKey{}).(string); escaped != "" {
			return escaped, nil
		}
		return "", fmt.Errorf("path escapes the sandbox: %s", p)
	}

	// The policy applies to where the path physically lands, not only to its
	// name: a symlink inside the project (notes.txt → .env, cfg → .ouija)
	// must not hand out what the lexical check protects.
	if isProtectedPath(full) || isProtectedPath(physicalPath(full)) {
		return "", fmt.Errorf("path is protected (credentials/system files): %s", p)
	}

	// Check intermediate symlinks component by component.
	// filepath.EvalSymlinks on the full path fails when the final component
	// doesn't exist (e.g. writing a new file via a symlinked directory),
	// so we walk each segment instead.
	if escaped, err := resolveSymlinkEscape(ctx, root, full); err != nil {
		return "", err
	} else if escaped != "" {
		return escaped, nil
	}

	return full, nil
}

// SafeResolve is the exported form of resolve, for callers outside this
// package (e.g. the MCP server's resource reader) that would otherwise
// re-implement the join and skip the symlink/protected-path checks.
func SafeResolve(ctx context.Context, root, p string) (string, error) {
	return resolve(ctx, root, p)
}

// resolveSymlinkEscape walks path components from root to full and returns the
// first symlink escape as the approved escape path (from ctx) or an error.
// Returns ("", nil) when no symlink escape is found.
func resolveSymlinkEscape(ctx context.Context, root, full string) (string, error) {
	rootAbs, err := canonRoot(root)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(rootAbs, full)
	if err != nil {
		return "", err
	}
	parts := strings.Split(rel, string(filepath.Separator))
	current := rootAbs
	for _, part := range parts {
		current = filepath.Join(current, part)
		// physicalPath, not EvalSymlinks: a dangling symlink (out →
		// ~/.ssh/authorized_keys, not created yet) fails EvalSymlinks yet a
		// write through it lands outside the root.
		if real := physicalPath(current); !within(rootAbs, real) {
			// Symlink escape — check for pre-approved escape in context
			if escaped, _ := ctx.Value(escapeKey{}).(string); escaped != "" {
				return escaped, nil
			}
			return "", fmt.Errorf("path escapes the project root (symlink): %s", current)
		}
	}
	return "", nil
}

// physicalPath resolves every symlink of p that exists on disk, including a
// dangling one (followed to its missing target, where a write through it
// would land). Trailing components that do not exist yet (a file about to be
// written) are kept as they are, appended to the resolved parent.
func physicalPath(p string) string { return physicalPathDepth(p, 0) }

// maxLinkHops bounds dangling-link chains (and loops), like the kernel's 40.
const maxLinkHops = 40

func physicalPathDepth(p string, hops int) string {
	existing, rest := p, ""
	for {
		if real, err := filepath.EvalSymlinks(existing); err == nil {
			return filepath.Join(real, rest)
		}
		// ModeIrregular too: since Go 1.23 a Windows junction (mklink /J,
		// npm link, pnpm) is reported as irregular, not as a symlink, and
		// EvalSymlinks stops at it — `link\…` with link -> C:\Users\me
		// otherwise looked like a path inside the project.
		if fi, err := os.Lstat(existing); err == nil && fi.Mode()&(os.ModeSymlink|os.ModeIrregular) != 0 && hops < maxLinkHops {
			if target, err := os.Readlink(existing); err == nil {
				if !filepath.IsAbs(target) {
					target = filepath.Join(filepath.Dir(existing), target)
				}
				return physicalPathDepth(filepath.Join(target, rest), hops+1)
			}
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return p
		}
		rest = filepath.Join(filepath.Base(existing), rest)
		existing = parent
	}
}

// checkPathSyntax refuses, on Windows, a ':' in a path component after the
// volume: NTFS reads `ouija.json::$DATA` as ouija.json itself (and
// `.ssh::$INDEX_ALLOCATION\…` as .ssh), which slipped past every name rule.
func checkPathSyntax(p string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	if strings.Contains(p[len(filepath.VolumeName(p)):], ":") {
		return fmt.Errorf("path %q uses an NTFS stream (':'), which is not allowed", p)
	}
	return nil
}

func canonRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if r, e := filepath.EvalSymlinks(abs); e == nil {
		return r, nil
	}
	return abs, nil
}

func within(rootAbs, full string) bool {
	rel, err := filepath.Rel(rootAbs, full)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
