package knowledge

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/discorgento/ouija/engine"
)

const (
	// FileName is the knowledge file inside the scope directory.
	FileName = "docs/knowledge.md"
)

// openFile opens the knowledge file under dir, through the sandbox: a docs/
// or knowledge.md symlink planted in a repo — before the check or swapped in
// after it — must not make Load read (into every prompt) or Save append to a
// file outside the project, or a protected one.
func openFile(dir string, flag int) (*os.File, error) {
	return engine.SafeOpenFile(context.Background(), dir, FileName, flag, 0o644)
}

// Load reads the knowledge file from the current directory.
// Returns empty string if the file doesn't exist or can't be read.
func Load(dir string) string {
	f, err := openFile(dir, os.O_RDONLY)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	return string(b)
}

// Save appends a fact to the knowledge file in the given directory.
// Creates the file with a header if it doesn't exist yet.
func Save(dir, fact string) error {
	fact = strings.TrimSpace(fact)
	if fact == "" {
		return fmt.Errorf("empty fact")
	}
	if dir == "" {
		return fmt.Errorf("empty directory")
	}

	// SafeOpenFile creates docs/ too.
	f, err := openFile(dir, os.O_APPEND|os.O_CREATE|os.O_WRONLY)
	if err != nil {
		return fmt.Errorf("knowledge file: %w", err)
	}
	defer f.Close()

	var header string
	if fi, err := f.Stat(); err == nil && fi.Size() == 0 {
		header = fmt.Sprintf("# Knowledge — %s\n\n", filepath.Base(dir))
	}

	if header != "" {
		if _, err := f.WriteString(header); err != nil {
			return fmt.Errorf("write header: %w", err)
		}
	}
	line := fmt.Sprintf("- [%s] %s\n", time.Now().Format("2006-01-02"), fact)
	if _, err := f.WriteString(line); err != nil {
		return fmt.Errorf("write fact: %w", err)
	}

	return nil
}
