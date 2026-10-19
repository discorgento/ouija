package engine

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

// The undo journal: what a file held before ouija's tools changed it, so the
// developer can take a change back (the chat's /undo). It covers write_file,
// edit_file and delete_file on a file — not bash, whose effects are unknown,
// nor deleting a whole directory.

// maxJournalFile is the largest file the journal keeps a copy of.
const maxJournalFile = 4 << 20

// maxJournalEntries bounds the journal: the oldest changes drop off.
const maxJournalEntries = 100

// journaled are the tools whose changes can be undone.
var journaled = map[string]bool{"write_file": true, "edit_file": true, "delete_file": true}

// journalEntry is one file as it was before a change.
type journalEntry struct {
	rel     string      // as the tool was given it, for messages
	full    string      // resolved inside the sandbox
	existed bool        // false: the change created it
	content []byte      // what it held (existed)
	mode    fs.FileMode // its permissions (existed)
	undoErr string      // why it cannot be undone ("" when it can)
}

// Journal records files before they change and puts them back. Safe for
// concurrent use (parallel tool calls).
type Journal struct {
	mu      sync.Mutex
	entries []journalEntry
}

// snapshot records rel (relative to root, resolved through the sandbox)
// before a change. It returns the entry's index for discard.
func (j *Journal) snapshot(ctx context.Context, root, rel string) int {
	e := journalEntry{rel: rel}
	full, err := resolve(ctx, root, rel)
	if err != nil {
		return -1 // the tool will refuse it too
	}
	e.full = full
	switch fi, err := os.Lstat(full); {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		e.undoErr = err.Error()
	case fi.IsDir():
		e.undoErr = "it was a directory"
	case !fi.Mode().IsRegular():
		e.undoErr = "it was not a regular file"
	case fi.Size() > maxJournalFile:
		e.undoErr = fmt.Sprintf("it was over %d MB", maxJournalFile>>20)
	default:
		b, err := os.ReadFile(full)
		if err != nil {
			e.undoErr = err.Error()
		}
		e.existed, e.content, e.mode = true, b, fi.Mode().Perm()
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries = append(j.entries, e)
	if len(j.entries) > maxJournalEntries {
		j.entries = j.entries[len(j.entries)-maxJournalEntries:]
	}
	return len(j.entries) - 1
}

// discard drops the entry of a change that did not happen (the tool failed).
func (j *Journal) discard(i int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if i >= 0 && i < len(j.entries) {
		j.entries = append(j.entries[:i], j.entries[i+1:]...)
	}
}

// Len reports how many changes can be taken back.
func (j *Journal) Len() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return len(j.entries)
}

// Undo puts the file of the latest change back as it was — its content, or
// no file when the change created it — and says what it did.
func (j *Journal) Undo() (string, error) {
	j.mu.Lock()
	if len(j.entries) == 0 {
		j.mu.Unlock()
		return "", errors.New("nothing to undo")
	}
	e := j.entries[len(j.entries)-1]
	j.entries = j.entries[:len(j.entries)-1]
	j.mu.Unlock()

	switch {
	case e.undoErr != "":
		return "", fmt.Errorf("%s cannot be undone: %s", e.rel, e.undoErr)
	case !e.existed:
		if err := os.Remove(e.full); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		return "removed " + e.rel + " (it did not exist before)", nil
	}
	if err := os.MkdirAll(filepath.Dir(e.full), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(e.full, e.content, e.mode); err != nil {
		return "", err
	}
	return "restored " + e.rel, nil
}

// Undo takes the latest file change back (see Journal.Undo).
func (e *Engine) Undo() (string, error) { return e.journal.Undo() }

// Journal returns the engine's undo journal.
func (e *Engine) Journal() *Journal { return e.journal }

// SnapshotFile records a project file before a change the engine does not
// make itself (the chat writing an edit back from the editor), so it can be
// undone too.
func (e *Engine) SnapshotFile(rel string) {
	e.journal.snapshot(context.Background(), e.cfg.ProjectRoot, rel)
}
