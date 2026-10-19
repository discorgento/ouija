package commands

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/discorgento/ouija/internal/knowledge"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func() error) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = w

	runErr := fn()

	if err := w.Close(); err != nil {
		t.Fatalf("close pipe: %v", err)
	}
	os.Stdout = old

	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	if runErr != nil {
		t.Fatalf("Learn() error = %v", runErr)
	}
	return string(b)
}

func TestLearn_SavesJoinedFactInCwd(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	if err := Learn([]string{"uses", "Bubble", "Tea"}); err != nil {
		t.Fatalf("Learn() error = %v", err)
	}

	got := knowledge.Load(".")
	if !strings.Contains(got, "uses Bubble Tea") {
		t.Fatalf("knowledge file = %q, want the joined fact", got)
	}
}

func TestLearn_NoArgsPrintsKnowledge(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	out := captureStdout(t, func() error { return Learn(nil) })
	if !strings.Contains(out, "No knowledge yet") {
		t.Fatalf("output = %q, want the empty-knowledge message", out)
	}

	if err := knowledge.Save(".", "a stored fact"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	out = captureStdout(t, func() error { return Learn(nil) })
	if !strings.Contains(out, "a stored fact") {
		t.Fatalf("output = %q, want the stored knowledge", out)
	}
}
