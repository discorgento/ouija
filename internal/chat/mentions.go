package chat

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/discorgento/ouija/engine"
)

// ─── @file mentions ───────────────────────────────────────────────────

// filePickerLabel is the picker's label while it completes an @mention.
const filePickerLabel = "Files"

const (
	// maxProjectFiles bounds the picker's file list (a huge monorepo).
	maxProjectFiles = 20000
	// maxPickerFiles is how many matches the picker lists.
	maxPickerFiles = 200
	// maxAttachBytes bounds what the files attached to one message add.
	maxAttachBytes = 120 * 1024
)

var mentionRe = regexp.MustCompile(`(^|\s)@([^\s@]+)`)

// typedMention returns the @mention being typed at the end of input, without
// the @ ("" with ok when just the @ is typed); ok is false when the last word
// is not a mention.
func typedMention(input string) (string, bool) {
	if input == "" || strings.HasSuffix(input, " ") {
		return "", false
	}
	word := input[strings.LastIndexAny(input, " \n\t")+1:]
	if !strings.HasPrefix(word, "@") {
		return "", false
	}
	return word[1:], true
}

// projectFiles lists the project's files, once per session.
func (m *model) projectFiles() []string {
	if m.files == nil {
		files, _ := engine.ProjectFiles(context.Background(), m.eng.Config().ProjectRoot, maxProjectFiles)
		m.files = append([]string{}, files...) // not nil: listed, even if empty
	}
	return m.files
}

// matchFiles ranks the files for an @mention being typed: a file whose name
// starts with it first, then one whose path does, then one that contains it
// (case-insensitive); shorter paths first within each.
func matchFiles(files []string, typed string) []string {
	q := strings.ToLower(typed)
	type hit struct {
		path string
		rank int
	}
	var hits []hit
	for _, f := range files {
		lf := strings.ToLower(f)
		base := lf[strings.LastIndexByte(lf, '/')+1:]
		switch {
		case strings.HasPrefix(base, q):
			hits = append(hits, hit{f, 0})
		case strings.HasPrefix(lf, q):
			hits = append(hits, hit{f, 1})
		case strings.Contains(lf, q):
			hits = append(hits, hit{f, 2})
		}
	}
	slices.SortStableFunc(hits, func(a, b hit) int {
		if a.rank != b.rank {
			return a.rank - b.rank
		}
		return len(a.path) - len(b.path)
	})
	out := make([]string, 0, min(len(hits), maxPickerFiles))
	for _, h := range hits[:min(len(hits), maxPickerFiles)] {
		out = append(out, h.path)
	}
	return out
}

// showFilePicker opens the picker on the files matching the @mention typed.
// It reports whether any matched.
func (m *model) showFilePicker(typed string) bool {
	matches := matchFiles(m.projectFiles(), typed)
	if len(matches) == 0 {
		m.picker.Hide()
		return false
	}
	m.picker.Show(matches, matches, filePickerLabel)
	return true
}

// completeMention replaces the @mention being typed with the chosen file.
func (m *model) completeMention(path string) {
	input := m.input.Value()
	start := strings.LastIndexAny(input, " \n\t") + 1
	m.input.SetValue(input[:start] + "@" + path + " ")
	m.input.CursorEnd()
}

// mentionedFiles returns the project files a message @mentions, in order,
// each once (a mention that is not a file is plain text).
func (m *model) mentionedFiles(text string) []string {
	var out []string
	for _, g := range mentionRe.FindAllStringSubmatch(text, -1) {
		p := strings.TrimRight(g[2], ".,;:!?)")
		if !slices.Contains(out, p) && slices.Contains(m.projectFiles(), p) {
			out = append(out, p)
		}
	}
	return out
}

// withAttachments is the prompt sent for a message: the message, then the
// files it @mentions, read through the sandbox and bounded in size, so the
// model has them without searching.
func (m *model) withAttachments(text string) string {
	files := m.mentionedFiles(text)
	if len(files) == 0 {
		return text
	}
	var b strings.Builder
	b.WriteString(text)
	b.WriteString("\n\n---\nFiles the developer attached to this message (@mentions):\n")
	used := 0
	for _, f := range files {
		content, err := engine.ReadProjectFile(context.Background(), m.eng.Config().ProjectRoot, f)
		if err != nil {
			fmt.Fprintf(&b, "\n<file path=%q>\n(not attached: %s)\n</file>\n", f, err)
			continue
		}
		if used+len(content) > maxAttachBytes {
			fmt.Fprintf(&b, "\n<file path=%q>\n(not attached: the attachments are over %d KB — read it with read_file)\n</file>\n", f, maxAttachBytes/1024)
			continue
		}
		used += len(content)
		fmt.Fprintf(&b, "\n<file path=%q>\n%s\n</file>\n", f, content)
	}
	return b.String()
}
