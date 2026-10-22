package engine

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

const (
	// maxGlobResults caps a glob listing: 200k paths are no use to a model
	// and cost hundreds of MB to build.
	maxGlobResults = 1000
	// maxListEntries caps a list_dir listing.
	maxListEntries = 2000
	// maxGrepLine is how much of one matching line grep prints, around the
	// match: a 2 MB minified line used to fill the whole result.
	maxGrepLine = 300
	// maxReadLine is how much of one line read_file keeps; maxGrepScan how
	// much of one line grep searches. The rest is read and dropped: a
	// single-line 300 MB file cost 600 MB, a 4 GB one ran out of memory.
	maxReadLine = maxToolBytes / 2
	maxGrepScan = 1 << 20
	// maxGlobStars caps the "**" in a glob pattern (after merging runs).
	maxGlobStars = 8
)

// readLine reads one line of r, its newline included, keeping at most limit
// bytes of it in buf (reused between calls) and dropping the rest; dropped
// counts what was dropped.
func readLine(r *bufio.Reader, buf []byte, limit int) (line []byte, dropped int64, err error) {
	line = buf[:0]
	for {
		chunk, err := r.ReadSlice('\n')
		keep := min(len(chunk), limit-len(line))
		line = append(line, chunk[:keep]...)
		dropped += int64(len(chunk) - keep)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return line, dropped, err
		}
	}
}

// skipDirs are the directories a search walk does not enter — below the
// directory it was asked to search, so a search *inside* node_modules works.
var skipDirs = map[string]bool{".git": true, "node_modules": true}

// walkFiles calls fn for every regular file under base (paths relative to
// rootAbs), skipping skipDirs below base, protected locations and symlinks
// (never followed out of the sandbox), and stopping when ctx ends — a walk of
// a large monorepo used to run to the end after Esc. fn returns filepath.SkipAll
// to stop early.
func walkFiles(ctx context.Context, rootAbs, base string, fn func(path, rel string, d fs.DirEntry) error) error {
	return filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		if err != nil {
			return nil // unreadable entry: skip it, keep walking
		}
		if d.IsDir() {
			if p != base && (skipDirs[d.Name()] || isProtectedPath(p)) {
				return fs.SkipDir
			}
			return nil
		}
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() || isProtectedFile(p) {
			return nil
		}
		rel, _ := filepath.Rel(rootAbs, p)
		return fn(p, rel, d)
	})
}

// isBinary reports whether data (a file's first bytes) looks binary.
func isBinary(data []byte) bool { return bytes.IndexByte(data, 0) >= 0 }

// readFileTool reads a text file, a window of it when it is long: offset and
// limit are lines. Only what is shown is read — the whole file used to be
// read into memory (twice) before 40 KB of it were kept, and nothing past
// those 40 KB was reachable.
func readFileTool(ctx context.Context, root string, in map[string]any) ToolResult {
	p, _ := in["path"].(string)
	full, err := resolve(ctx, root, p)
	if err != nil {
		return errInvalidInput(err.Error())
	}
	f, err := openResolved(root, full, os.O_RDONLY, 0)
	if err != nil {
		return errNotFound(err.Error())
	}
	defer f.Close()
	if fi, err := f.Stat(); err == nil && fi.IsDir() {
		return errInvalidInput(p + " is a directory — use list_dir")
	}
	offset, limit := intArg(in, "offset", 1), intArg(in, "limit", 0)
	if offset < 1 {
		offset = 1
	}

	r := bufio.NewReaderSize(f, 64*1024)
	if head, _ := r.Peek(8192); isBinary(head) {
		return errInvalidInput(p + " is a binary file")
	}
	var out strings.Builder
	line, shown := 0, 0
	var buf []byte
	for {
		b, dropped, err := readLine(r, buf, maxReadLine)
		buf = b
		text := string(b)
		if dropped > 0 {
			// Cut on a rune, marked, the newline kept.
			text = strings.ToValidUTF8(strings.TrimSuffix(text, "\n"), "") + fmt.Sprintf("…[line cut: %d more bytes]", dropped)
			if err == nil {
				text += "\n"
			}
		}
		if text != "" {
			line++
			if line >= offset {
				if out.Len()+len(text) > maxToolBytes || (limit > 0 && shown == limit) {
					fmt.Fprintf(&out, "\n…[more follows: read on with offset %d]", line)
					return ToolResult{Data: out.String()}
				}
				out.WriteString(text)
				shown++
			}
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return errInternal(err.Error())
		}
	}
	if shown == 0 && line > 0 {
		return errInvalidInput(fmt.Sprintf("offset %d is past the end (%d lines)", offset, line))
	}
	return ToolResult{Data: out.String()}
}

// intArg reads an integer argument (JSON numbers decode as float64).
func intArg(in map[string]any, key string, def int) int {
	if v, ok := in[key].(float64); ok {
		return int(v)
	}
	return def
}

func grepTool(ctx context.Context, root string, in map[string]any) ToolResult {
	pattern, _ := in["pattern"].(string)
	sub, _ := in["path"].(string)
	if sub == "" {
		sub = "."
	}
	// Go regexps are RE2: matching is linear in the input, with no
	// catastrophic backtracking, so a model-supplied pattern is safe to run.
	re, err := regexp.Compile(pattern)
	if err != nil {
		return errInvalidInput(err.Error())
	}
	base, err := resolve(ctx, root, sub)
	if err != nil {
		return errInvalidInput(err.Error())
	}
	// Each file is opened through the project's os.Root and checked once
	// open: one swapped for a symlink after the walk saw it is not followed.
	r, rootAbs, err := openProjectRoot(root)
	if err != nil {
		return errInternal(err.Error())
	}
	defer r.Close()
	var out strings.Builder
	matches := 0
	lc := &linkCheck{}
	search := func(p, rel string) error {
		f, err := openIn(r, rootAbs, p, os.O_RDONLY, 0, lc)
		if err != nil {
			return nil
		}
		defer f.Close()
		r := bufio.NewReaderSize(f, 64*1024)
		if head, _ := r.Peek(8192); isBinary(head) {
			return nil
		}
		// Line by line, so a large file is searched (it used to be skipped
		// silently over 2 MB, and the answer was "no matches"); of a line,
		// its first maxGrepScan bytes.
		var buf []byte
		for n := 1; ; n++ {
			b, _, err := readLine(r, buf, maxGrepScan)
			buf = b
			if loc := re.FindIndex(b); loc != nil {
				text := string(b)
				fmt.Fprintf(&out, "%s:%d: %s\n", rel, n, clipAround(strings.TrimRight(text, "\r\n"), loc))
				if matches++; matches >= maxGrepMatches {
					return filepath.SkipAll
				}
			}
			if err != nil {
				return nil
			}
		}
	}
	var walkErr error
	if fi, err := os.Stat(base); err == nil && fi.Mode().IsRegular() {
		rel, _ := filepath.Rel(rootAbs, base)
		walkErr = search(base, rel) // a single file the model named
	} else {
		walkErr = walkFiles(ctx, rootAbs, base, func(p, rel string, d fs.DirEntry) error {
			if skipPath(p) {
				return nil
			}
			return search(p, rel)
		})
	}
	if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
		return errInternal("grep: " + walkErr.Error())
	}
	if matches == 0 {
		return ToolResult{Data: "no matches"}
	}
	if matches >= maxGrepMatches {
		fmt.Fprintf(&out, "\n[match limit %d reached — narrow the pattern or the path]\n", maxGrepMatches)
	}
	return ToolResult{Data: trim(out.String())}
}

// clipAround shortens a long line to maxGrepLine bytes around the match.
func clipAround(line string, loc []int) string {
	line = strings.TrimSpace(line)
	if len(line) <= maxGrepLine {
		return line
	}
	start := max(0, loc[0]-maxGrepLine/3)
	end := min(len(line), start+maxGrepLine)
	clip := strings.ToValidUTF8(line[start:end], "")
	if start > 0 {
		clip = "…" + clip
	}
	if end < len(line) {
		clip += "…"
	}
	return clip
}

// globTool lists the files matching a pattern relative to the root, with **
// for any number of directories (filepath.Glob read ** as *, so **/*.go
// missed every file below the first level).
func globTool(ctx context.Context, root string, in map[string]any) ToolResult {
	pattern, _ := in["pattern"].(string)
	pattern = filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" || path.IsAbs(pattern) || filepath.IsAbs(pattern) || slices.Contains(strings.Split(pattern, "/"), "..") {
		return errInvalidInput("glob: the pattern must be relative to the project root, without ..")
	}
	if _, err := path.Match(strings.ReplaceAll(pattern, "**", "*"), ""); err != nil {
		return errInvalidInput("glob: " + err.Error())
	}
	// Runs of ** are one **, and a pattern holds few: each one multiplies
	// the ways a path can match (60 of them took minutes per file).
	segs := slices.Compact(strings.Split(pattern, "/"))
	stars := 0
	for _, s := range segs {
		if s == "**" {
			stars++
		}
	}
	if stars > maxGlobStars {
		return errInvalidInput(fmt.Sprintf("glob: at most %d ** in a pattern", maxGlobStars))
	}
	rootAbs, _ := canonRoot(root)
	// Walk only below the pattern's literal prefix (src/**/x.go → src).
	prefix := 0
	for prefix < len(segs)-1 && !strings.ContainsAny(segs[prefix], "*?[") {
		prefix++
	}
	base := filepath.Join(rootAbs, filepath.FromSlash(strings.Join(segs[:prefix], "/")))
	var found []string
	walkErr := walkFiles(ctx, rootAbs, base, func(p, rel string, d fs.DirEntry) error {
		if matchGlob(segs, strings.Split(filepath.ToSlash(rel), "/")) {
			found = append(found, rel)
			if len(found) >= maxGlobResults {
				return filepath.SkipAll
			}
		}
		return nil
	})
	if errors.Is(walkErr, context.Canceled) || errors.Is(walkErr, context.DeadlineExceeded) {
		return errInternal("glob: " + walkErr.Error())
	}
	if len(found) == 0 {
		return ToolResult{Data: "no matches"}
	}
	slices.Sort(found)
	out := strings.Join(found, "\n") + "\n"
	if len(found) >= maxGlobResults {
		out += fmt.Sprintf("\n[limit %d reached — narrow the pattern]\n", maxGlobResults)
	}
	return ToolResult{Data: trim(out)}
}

// matchGlob matches path segments against pattern segments; "**" matches
// zero or more whole segments, the rest follow path.Match. Each (pattern,
// segment) position is solved once: without the memo, n "**" tried every
// split of the path, exponential in n.
func matchGlob(pat, segs []string) bool {
	memo := make([]int8, (len(pat)+1)*(len(segs)+1)) // 0 unknown, 1 yes, 2 no
	var match func(i, j int) bool
	match = func(i, j int) bool {
		k := i*(len(segs)+1) + j
		if memo[k] != 0 {
			return memo[k] == 1
		}
		var ok bool
		switch {
		case i == len(pat):
			ok = j == len(segs)
		case pat[i] == "**":
			ok = match(i+1, j) || (j < len(segs) && match(i, j+1))
		case j < len(segs):
			m, _ := path.Match(pat[i], segs[j])
			ok = m && match(i+1, j+1)
		}
		memo[k] = 2
		if ok {
			memo[k] = 1
		}
		return ok
	}
	return match(0, 0)
}

func listDirTool(ctx context.Context, root string, in map[string]any) ToolResult {
	sub, _ := in["path"].(string)
	if sub == "" {
		sub = "."
	}
	full, err := resolve(ctx, root, sub)
	if err != nil {
		return errInvalidInput(err.Error())
	}
	d, err := openResolved(root, full, os.O_RDONLY, 0)
	if err != nil {
		return errNotFound(err.Error())
	}
	defer d.Close()
	entries, err := d.ReadDir(-1)
	if err != nil {
		return errNotFound(err.Error())
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	var out strings.Builder
	for i, e := range entries {
		if i == maxListEntries {
			fmt.Fprintf(&out, "\n[%d more entries not shown — use glob to narrow]\n", len(entries)-i)
			break
		}
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		out.WriteString(name + "\n")
	}
	return ToolResult{Data: trim(out.String())}
}

// ProjectFiles lists the project's files (relative, slash-separated) the way
// the search tools see them — .git, node_modules, protected paths and
// symlinks left out — up to limit, for a UI's file picker.
func ProjectFiles(ctx context.Context, root string, limit int) ([]string, error) {
	rootAbs, err := canonRoot(root)
	if err != nil {
		return nil, err
	}
	var out []string
	err = walkFiles(ctx, rootAbs, rootAbs, func(_, rel string, _ fs.DirEntry) error {
		out = append(out, filepath.ToSlash(rel))
		if len(out) >= limit {
			return filepath.SkipAll
		}
		return nil
	})
	return out, err
}

// ReadProjectFile reads a text file of the project through the sandbox, as
// read_file does (cut at the same size), for a file the developer attached to
// a message.
func ReadProjectFile(ctx context.Context, root, rel string) (string, error) {
	r := readFileTool(ctx, root, map[string]any{"path": rel})
	if r.Error != nil {
		return "", errors.New(r.Error.Message)
	}
	return r.Data, nil
}
