//go:build unix

package engine

import (
	"context"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// A FIFO in the project neither hangs a read or a write nor is opened: the
// open does not wait for the other end, and only regular files and
// directories pass.
func TestOpenResolvedRefusesAFIFO(t *testing.T) {
	root := t.TempDir()
	if err := syscall.Mkfifo(filepath.Join(root, "p"), 0o600); err != nil {
		t.Skip("mkfifo:", err)
	}
	// One at a time: a reader and a writer opened together would unblock
	// each other.
	for name, tool := range map[string]func() string{
		"read": func() string {
			return readFileTool(context.Background(), root, map[string]any{"path": "p"}).String()
		},
		"write": func() string {
			return writeFileTool(context.Background(), root, map[string]any{"path": "p", "content": "x"}).String()
		},
	} {
		done := make(chan string, 1)
		go func() { done <- tool() }()
		select {
		case out := <-done:
			if !strings.Contains(out, "not a regular file") {
				t.Errorf("%s: a FIFO was opened: %s", name, out)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("%s: opening a FIFO hung", name)
		}
	}
}
