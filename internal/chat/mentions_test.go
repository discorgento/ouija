package chat

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/discorgento/ouija/engine"
)

func newMentionModel(t *testing.T) *model {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	for f, body := range map[string]string{"engine/budget.go": "package engine // budget\n", "README.md": "# hi\n", "docs/budget.md": "notes\n"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.Dir(f)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, f), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := newModel(&switchEngine{cfg: &engine.Config{ProjectRoot: root}}, nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	return m
}

func TestTypedMention(t *testing.T) {
	for input, want := range map[string]string{"look at @eng": "eng", "@": "", "@a.go": "a.go"} {
		if got, ok := typedMention(input); !ok || got != want {
			t.Errorf("typedMention(%q) = %q, %v", input, got, ok)
		}
	}
	for _, input := range []string{"no mention", "@done ", "mail@x.com", ""} {
		if _, ok := typedMention(input); ok && input != "mail@x.com" {
			t.Errorf("typedMention(%q) found a mention", input)
		}
	}
}

// Typing @ opens the file picker; Tab completes the path into the message.
func TestMentionPickerCompletes(t *testing.T) {
	m := newMentionModel(t)
	for _, r := range "see @budget.g" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if !m.picker.Visible || m.picker.Label != filePickerLabel {
		t.Fatalf("picker visible=%v label=%q", m.picker.Visible, m.picker.Label)
	}
	if got := m.picker.SelectedValue(); got != "engine/budget.go" { // matched by its name
		t.Fatalf("first match = %q", got)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if got := m.input.Value(); got != "see @engine/budget.go " {
		t.Fatalf("input = %q", got)
	}
}

// A message's @mentions are attached to the prompt; other @words are text.
func TestMentionsAreAttached(t *testing.T) {
	m := newMentionModel(t)
	text := "explain @engine/budget.go and @nothing, mail me@x.com"
	if got := m.mentionedFiles(text); strings.Join(got, ",") != "engine/budget.go" {
		t.Fatalf("mentioned = %v", got)
	}
	prompt := m.withAttachments(text)
	if !strings.HasPrefix(prompt, text) || !strings.Contains(prompt, "<file path=\"engine/budget.go\">\npackage engine // budget\n") {
		t.Fatalf("prompt:\n%s", prompt)
	}
	if m.withAttachments("no mentions") != "no mentions" {
		t.Fatal("a message without mentions changed")
	}
}
