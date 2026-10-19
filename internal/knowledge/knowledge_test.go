package knowledge

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestLoad_MissingFileReturnsEmpty(t *testing.T) {
	if got := Load(t.TempDir()); got != "" {
		t.Fatalf("Load() = %q, want empty when there is no knowledge file", got)
	}
}

func TestSave_CreatesHeaderThenAppends(t *testing.T) {
	dir := t.TempDir()

	if err := Save(dir, "first fact"); err != nil {
		t.Fatalf("Save(first) error = %v", err)
	}
	if err := Save(dir, "   second fact   "); err != nil {
		t.Fatalf("Save(second) error = %v", err)
	}

	got := Load(dir)
	if got == "" {
		t.Fatal("Load() = empty after Save")
	}

	// The header is written once, naming the project directory.
	wantHeader := "# Knowledge — " + filepath.Base(dir)
	if n := strings.Count(got, wantHeader); n != 1 {
		t.Errorf("header %q appears %d times, want exactly 1:\n%s", wantHeader, n, got)
	}

	// Facts are appended as dated bullets.
	dated := regexp.MustCompile(`(?m)^- \[\d{4}-\d{2}-\d{2}\] first fact$`)
	if !dated.MatchString(got) {
		t.Errorf("missing dated bullet for the first fact:\n%s", got)
	}
	if !strings.Contains(got, "] second fact") {
		t.Errorf("second fact was not appended:\n%s", got)
	}
	if strings.Contains(got, "   second fact   ") {
		t.Error("fact was not trimmed before writing")
	}
}

func TestSave_CreatesDocsDir(t *testing.T) {
	dir := t.TempDir()

	if err := Save(dir, "a fact"); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName)); err != nil {
		t.Fatalf("knowledge file not created at %s: %v", FileName, err)
	}
}

func TestSave_RejectsEmptyInput(t *testing.T) {
	if err := Save(t.TempDir(), "   \n\t "); err == nil {
		t.Error("Save() error = nil for an empty fact, want an error")
	}
	if err := Save("", "a fact"); err == nil {
		t.Error("Save() error = nil for an empty directory, want an error")
	}
}

func TestFileName_IsUnderDocs(t *testing.T) {
	if want := filepath.Join("docs", "knowledge.md"); FileName != want {
		t.Fatalf("FileName = %q, want %q", FileName, want)
	}
}
