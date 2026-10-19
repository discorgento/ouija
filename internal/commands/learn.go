package commands

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/knowledge"
	"github.com/discorgento/ouija/internal/termsafe"
)

// Learn manages project knowledge (show or save).
func Learn(args []string) error {
	// ContinueOnError: a bad flag is returned like any other error instead of
	// exiting the process from inside a library function.
	fs := flag.NewFlagSet("learn", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return app.ErrUsage // flag already printed why
	}
	rest := fs.Args()
	root := projectRoot()

	if len(rest) == 0 {
		known := knowledge.Load(root)
		if known == "" {
			fmt.Println("No knowledge yet")
			return nil
		}
		fmt.Println(termsafe.Clean(known)) // a cloned repo's file
		return nil
	}

	fact := strings.Join(rest, " ")
	if err := knowledge.Save(root, fact); err != nil {
		return fmt.Errorf("cannot save knowledge: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ Learned: %s\n", fact)
	fmt.Fprintf(os.Stderr, "  → %s\n", filepath.Join(root, knowledge.FileName))
	return nil
}

// projectRoot is the root of the project the current directory belongs to —
// where the chat and the task totem read the knowledge from — or "." when no
// project config is found (a subdirectory used to get its own docs/).
func projectRoot() string {
	if cfg, _, err := app.DiscoverConfig(""); err == nil && cfg.ProjectRoot != "" {
		return cfg.ProjectRoot
	}
	return "."
}
