package knowledge

import (
	"context"
	"fmt"
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

// resolvePath returns the knowledge file under dir, through the sandbox: a
// docs/ or knowledge.md symlink planted in a repo must not make Load read
// (into every prompt) or Save append to a file outside the project, or a
// protected one.
func resolvePath(dir string) (string, error) {
	return engine.SafeResolve(context.Background(), dir, FileName)
}

// Load reads the knowledge file from the current directory.
// Returns empty string if the file doesn't exist or can't be read.
func Load(dir string) string {
	path, err := resolvePath(dir)
	if err != nil {
		return ""
	}
	b, err := os.ReadFile(path)
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

	path, err := resolvePath(dir)
	if err != nil {
		return fmt.Errorf("knowledge file: %w", err)
	}
	dirPath := filepath.Dir(path)

	if err := os.MkdirAll(dirPath, 0o755); err != nil {
		return fmt.Errorf("create docs dir: %w", err)
	}

	var header string
	if _, err := os.Stat(path); os.IsNotExist(err) {
		header = fmt.Sprintf("# Knowledge — %s\n\n", filepath.Base(dir))
	}

	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open knowledge file: %w", err)
	}
	defer f.Close()

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
