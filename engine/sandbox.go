package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unicode/utf8"
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
	if err := checkPathRunes(p); err != nil {
		return "", err
	}
	full, err := SafeJoin(root, p)
	if err != nil {
		// Lexical escape — check for pre-approved escape in context
		if escaped, _ := ctx.Value(escapeKey{}).(string); escaped != "" {
			return approvedEscape(escaped, p)
		}
		return "", fmt.Errorf("path escapes the sandbox: %s", p)
	}

	// The policy applies to where the path physically lands, not only to its
	// name: a symlink inside the project (notes.txt → .env, cfg → .ouija)
	// must not hand out what the lexical check protects.
	if isProtectedPath(full) || isProtectedFile(physicalPath(full)) {
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

// approvedEscape returns the path the user approved leaving the project for,
// unless it, or where it physically leads, is protected. The approval is
// consent to that path, not to its target: a symlink there pointing at
// ~/.ssh showed an innocent name in the prompt and handed out the key.
func approvedEscape(escaped, asked string) (string, error) {
	if isProtectedPath(escaped) || isProtectedFile(physicalPath(escaped)) {
		return "", fmt.Errorf("path is protected (credentials/system files): %s", asked)
	}
	return escaped, nil
}

// SafeResolve is the exported form of resolve, for callers outside this
// package (e.g. the MCP server's resource reader) that would otherwise
// re-implement the join and skip the symlink/protected-path checks.
func SafeResolve(ctx context.Context, root, p string) (string, error) {
	return resolve(ctx, root, p)
}

// openResolved opens full, a path resolve approved, without the gap between
// that check and the open: a symlink swapped in afterwards (notes.txt →
// ~/.ssh/id_rsa, docs → /etc) would otherwise be followed by a plain
// os.Open. Inside the project the open goes through an os.Root on the
// project, which refuses any link leading out of it (see rootRel for the
// name it is given). Then the opened file is compared with what full names
// now: a link swapped to a protected file inside the project fails that, as
// does a hard link to a credential.
// An approved escape outside the project is opened by path and verified the
// same way. The open must not truncate: a write opens without O_TRUNC and
// truncates once the file is verified, so the wrong file is never cut.
func openResolved(root, full string, flag int, perm fs.FileMode) (*os.File, error) {
	r, rootAbs, err := openProjectRoot(root)
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return openIn(r, rootAbs, full, flag, perm, &linkCheck{})
}

// openProjectRoot opens the project as an os.Root, with its canonical path.
func openProjectRoot(root string) (*os.Root, string, error) {
	rootAbs, err := canonRoot(root)
	if err != nil {
		return nil, "", err
	}
	r, err := os.OpenRoot(rootAbs)
	return r, rootAbs, err
}

// openIn is openResolved with the project's os.Root already open, and the
// hard-link check of the call (a search opens many files).
func openIn(r *os.Root, rootAbs, full string, flag int, perm fs.FileMode, lc *linkCheck) (*os.File, error) {
	var f *os.File
	var err error
	// Non-blocking, then regular files and directories only: opening a FIFO
	// (mkfifo in the project) waited forever for a writer or reader, and no
	// cancellation reaches a blocked open.
	flag |= openNonblock
	if rel, ok := rootRel(rootAbs, full); ok {
		f, err = r.OpenFile(rel, flag, perm)
	} else {
		f, err = os.OpenFile(full, flag, perm) // an approved escape
	}
	if errors.Is(err, syscall.ENXIO) {
		// A non-blocking write open of a FIFO with no reader: refused as
		// any other file that is not regular, whatever the OS calls it
		// ("no such device or address", "device not configured").
		return nil, fmt.Errorf("%s is not a regular file", full)
	}
	if err != nil {
		return nil, err
	}
	if err := verifyOpened(f, rootAbs, full, flag&(os.O_WRONLY|os.O_RDWR) != 0, lc); err != nil {
		f.Close()
		return nil, err
	}
	return f, nil
}

// verifyOpened checks that f is the file full names now, and that this is
// not a protected file: the check resolve made still holds for what was
// opened. A file opened to be written must not be git metadata, and a file
// with other names is judged by checkHardLinks.
func verifyOpened(f *os.File, rootAbs, full string, write bool, lc *linkCheck) error {
	got, err := f.Stat()
	if err != nil {
		return err
	}
	if !got.Mode().IsRegular() && !got.IsDir() {
		return fmt.Errorf("%s is not a regular file", full)
	}
	real := physicalPath(full)
	now, err := os.Stat(real)
	if err != nil || !os.SameFile(got, now) {
		return fmt.Errorf("%s changed while it was being opened", full)
	}
	if isProtectedPath(full) || isProtectedFile(real) {
		return fmt.Errorf("path is protected (credentials/system files): %s", full)
	}
	if write {
		if err := checkGitWrite(full); err != nil {
			return err
		}
	}
	return lc.checkHardLinks(rootAbs, real, got, write)
}

// checkGitWrite refuses a write or delete of git metadata: any path with a
// component named .git, the directory or a worktree's gitdir file, as named
// or where it physically lands. Beyond .git/config and hooks (protected
// against reads too), .git/info/attributes runs filters, objects/info/
// alternates pulls objects from elsewhere, and a planted .git file points
// git at another repository. Reads stay allowed (.git/HEAD).
func checkGitWrite(full string) error {
	if isGitMetadata(full) || isGitMetadata(physicalPath(full)) {
		return fmt.Errorf("path is git metadata, which the file tools do not write: %s", full)
	}
	return nil
}

// mkdirResolved creates dir, a directory resolve approved, and its parents —
// through an os.Root inside the project, so a link swapped in cannot make
// it create directories elsewhere.
func mkdirResolved(root, dir string) error {
	r, rootAbs, err := openProjectRoot(root)
	if err != nil {
		return err
	}
	defer r.Close()
	rel, ok := rootRel(rootAbs, dir)
	if !ok {
		return os.MkdirAll(dir, 0o755) // an approved escape
	}
	return r.MkdirAll(rel, 0o755)
}

// rootRel is the name an os.Root on rootAbs opens full by, and false when
// full, as resolve returned it, is outside the project (an approved escape).
// It is where full physically lands when that is inside the project, so a
// project's own absolute symlinks, which os.Root refuses, keep working; the
// lexical name otherwise, which os.Root then refuses to follow out.
func rootRel(rootAbs, full string) (string, bool) {
	if !within(rootAbs, full) {
		return "", false
	}
	if real := physicalPath(full); within(rootAbs, real) {
		full = real
	}
	rel, err := filepath.Rel(rootAbs, full)
	return rel, err == nil
}

// SafeOpenFile resolves p under root like SafeResolve and opens it like
// os.OpenFile, without the gap between the check and the open (see
// openResolved). flag must not hold O_TRUNC: truncate the returned file.
func SafeOpenFile(ctx context.Context, root, p string, flag int, perm fs.FileMode) (*os.File, error) {
	full, err := resolve(ctx, root, p)
	if err != nil {
		return nil, err
	}
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
		if err := checkGitWrite(full); err != nil {
			return nil, err
		}
	}
	if flag&os.O_CREATE != 0 {
		if err := mkdirResolved(root, filepath.Dir(full)); err != nil {
			return nil, err
		}
	}
	return openResolved(root, full, flag, perm)
}

// SafeWriteFile is os.WriteFile through SafeOpenFile: it creates the parent
// directories, keeps an existing file's mode, and truncates only once the
// opened file passed the checks. It returns where the file was written.
func SafeWriteFile(ctx context.Context, root, p string, data []byte, perm fs.FileMode) (string, error) {
	full, err := resolve(ctx, root, p)
	if err != nil {
		return "", err
	}
	if err := checkGitWrite(full); err != nil {
		return "", err
	}
	if err := mkdirResolved(root, filepath.Dir(full)); err != nil {
		return "", err
	}
	f, err := openResolved(root, full, os.O_WRONLY|os.O_CREATE, perm)
	if err != nil {
		return "", err
	}
	if err := rewrite(f, data); err != nil {
		f.Close()
		return "", err
	}
	return full, f.Close()
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
				return approvedEscape(escaped, current)
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

// checkPathRunes refuses a path holding a control character (newline, tab,
// escape…) or an invisible or bidi one. The approval prompt shows a path,
// the tool writes another: `docs/<long name>\n\n  will create docs/x/../../Makefile`
// read as a harmless file once the prompt cut it, and wrote the Makefile.
// No real file name needs one.
func checkPathRunes(p string) error {
	if i := strings.IndexFunc(p, pathHiddenRune); i >= 0 {
		r, _ := utf8.DecodeRuneInString(p[i:])
		return fmt.Errorf("the path contains a control or invisible character (U+%04X); name the file without one", r)
	}
	return nil
}

// pathHiddenRune is hiddenRune plus what a command may hold but a file name
// should not: newline, tab and the Unicode line and paragraph separators.
func pathHiddenRune(r rune) bool {
	return r == '\n' || r == '\t' || r == 0x2028 || r == 0x2029 || hiddenRune(r)
}

// approvalTarget is the file a call on path p touches, for its approval
// prompt: project-relative, after the join cleaned the name
// (docs/x/../../Makefile is Makefile) and symlinks were followed (absolute
// when that leads out of the project). via is the cleaned name the model
// gave when it differs: a symlink notes.txt → Makefile shows both. ok is
// false when p does not join under the project.
func approvalTarget(root, p string) (target, via string, ok bool) {
	full, err := SafeJoin(root, p)
	if err != nil {
		return "", "", false
	}
	rootAbs, err := canonRoot(root)
	if err != nil {
		return "", "", false
	}
	named, _ := filepath.Rel(rootAbs, full)
	target = physicalPath(full)
	if within(rootAbs, target) {
		target, _ = filepath.Rel(rootAbs, target)
	}
	if target != named {
		via = named
	}
	return target, via, true
}

// targetLabel spells approvalTarget for a preview: "notes.txt", or
// "notes.txt → Makefile (symlink)"; p as given when it does not resolve.
func targetLabel(root, p string) string {
	target, via, ok := approvalTarget(root, p)
	switch {
	case !ok:
		return p
	case via != "":
		return via + " → " + target + " (symlink)"
	}
	return target
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
