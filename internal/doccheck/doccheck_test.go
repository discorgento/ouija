// Package doccheck verifies the Markdown documentation as part of the normal
// test suite: every relative link resolves to a file inside the repository,
// every #fragment matches a heading of the target page, and every page under
// docs/ is reachable from docs/index.md.
//
// It is a test-only package (no non-test Go files) so `go test ./...` — and
// therefore `make check` and CI — fails as soon as a docs move breaks a link.
package doccheck

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"unicode"
)

// repoRoot walks up from this test file (internal/doccheck) to the module root.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test file")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root not found above %s: %v", file, err)
	}
	return root
}

// docFiles returns every Markdown file that participates in the link check:
// the root-level documents plus everything under docs/.
func docFiles(t *testing.T, root string) []string {
	t.Helper()
	files := []string{}
	for _, name := range []string{"README.md", "CONTRIBUTING.md", "SECURITY.md", "CHANGELOG.md"} {
		if _, err := os.Stat(filepath.Join(root, name)); err == nil {
			files = append(files, filepath.Join(root, name))
		}
	}
	err := filepath.Walk(filepath.Join(root, "docs"), func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".md") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk docs: %v", err)
	}
	return files
}

var (
	fenceRE      = regexp.MustCompile("(?s)```.*?```|~~~.*?~~~")
	commentRE    = regexp.MustCompile("(?s)<!--.*?-->")
	inlineCodeRE = regexp.MustCompile("`[^`\n]*`")
	linkRE       = regexp.MustCompile(`\[[^\]]*\]\(([^)\s]+)[^)]*\)`)
	headingRE    = regexp.MustCompile(`^#{1,6}\s+(.*?)\s*#*\s*$`)
	markdownRE   = regexp.MustCompile(`\.md$`)
)

// stripNoise removes content that never renders as a link or a heading: fenced
// code blocks and HTML comments. Inline code is intentionally kept here —
// headings like `### \`ouija setup\“ are code, and their text defines the
// anchor.
func stripNoise(body string) string {
	body = fenceRE.ReplaceAllString(body, "")
	return commentRE.ReplaceAllString(body, "")
}

// headingSlugs computes the GitHub-style anchor of every heading in body,
// including the `-1`/`-2` suffixes GitHub adds to duplicate headings.
func headingSlugs(body string) map[string]bool {
	slugs := map[string]bool{}
	seen := map[string]int{}
	for _, line := range strings.Split(body, "\n") {
		m := headingRE.FindStringSubmatch(strings.TrimLeft(line, " \t"))
		if m == nil {
			continue
		}
		slug := githubSlug(m[1])
		if slug == "" {
			continue
		}
		if n := seen[slug]; n > 0 {
			slug = slug + "-" + itoa(n)
		}
		seen[githubSlug(m[1])]++
		slugs[slug] = true
	}
	return slugs
}

// githubSlug mimics GitHub's heading-to-anchor conversion: lowercase, drop
// everything that is not a letter, digit, space, hyphen or underscore, then
// turn spaces into hyphens.
func githubSlug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-' || r == '_':
			b.WriteRune(r)
		case r == ' ':
			b.WriteByte('-')
		}
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// TestRelativeLinksResolve asserts that every relative Markdown link and
// #anchor in the documentation points at something that exists.
func TestRelativeLinksResolve(t *testing.T) {
	root := repoRoot(t)
	for _, file := range docFiles(t, root) {
		rel, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatalf("rel: %v", err)
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		body := stripNoise(string(raw))
		// Inline code never renders as a link, and the style-guide skeleton
		// contains placeholder links (`[Index](<relative index>)`) inside it.
		linkBody := inlineCodeRE.ReplaceAllString(body, "")
		slugs := headingSlugs(body)
		dir := filepath.Dir(file)

		for _, target := range linkRE.FindAllStringSubmatch(linkBody, -1) {
			checkTarget(t, root, rel, dir, target[1], slugs)
		}
	}
}

func checkTarget(t *testing.T, root, rel, dir, target string, slugs map[string]bool) {
	t.Helper()

	if strings.HasPrefix(target, "http://") ||
		strings.HasPrefix(target, "https://") ||
		strings.HasPrefix(target, "mailto:") {
		return
	}

	path, anchor, _ := strings.Cut(target, "#")

	// Same-page anchor: `#section`.
	if path == "" {
		if anchor != "" && !slugs[anchor] {
			t.Errorf("%s: anchor %q does not match any heading of this page", rel, target)
		}
		return
	}

	abs := filepath.Clean(filepath.Join(dir, filepath.FromSlash(path)))
	if !within(root, abs) {
		t.Errorf("%s: link %q escapes the repository root", rel, target)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		t.Errorf("%s: link %q → %s: file not found", rel, target, path)
		return
	}
	if anchor == "" || info.IsDir() {
		return
	}

	// Cross-page anchor: verify it against the target page's headings.
	raw, err := os.ReadFile(abs)
	if err != nil || !markdownRE.MatchString(abs) {
		return // anchors into non-Markdown files are not verifiable here
	}
	if !headingSlugs(stripNoise(string(raw)))[anchor] {
		tRel, _ := filepath.Rel(root, abs)
		t.Errorf("%s: link %q → anchor %q not found in %s", rel, target, anchor, tRel)
	}
}

func within(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// TestIndexCoversEveryPage asserts that docs/index.md links to every page in
// the tree, so no document becomes an orphan after a rename.
func TestIndexCoversEveryPage(t *testing.T) {
	root := repoRoot(t)
	index, err := os.ReadFile(filepath.Join(root, "docs", "index.md"))
	if err != nil {
		t.Fatalf("read docs/index.md: %v", err)
	}
	body := stripNoise(string(index))

	for _, file := range docFiles(t, root) {
		rel, err := filepath.Rel(filepath.Join(root, "docs"), file)
		if err != nil || strings.Contains(rel, "..") {
			continue // root-level documents, listed separately
		}
		want := filepath.ToSlash(rel)
		if want == "index.md" {
			continue
		}
		if !strings.Contains(body, "("+want+")") {
			t.Errorf("docs/index.md does not link to %s", want)
		}
	}
}
