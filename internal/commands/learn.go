package commands

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/discorgento/ouija/internal/knowledge"
)

// Learn manages project knowledge (show or save).
func Learn(args []string) error {
	fs := flag.NewFlagSet("learn", flag.ExitOnError)
	fs.Parse(args)
	rest := fs.Args()

	if len(rest) == 0 {
		known := knowledge.Load(".")
		if known == "" {
			fmt.Println("No knowledge yet")
			return nil
		}
		fmt.Println(known)
		return nil
	}

	fact := strings.Join(rest, " ")
	if err := knowledge.Save(".", fact); err != nil {
		return fmt.Errorf("cannot save knowledge: %w", err)
	}
	fmt.Fprintf(os.Stderr, "✓ Learned: %s\n", fact)
	fmt.Fprintf(os.Stderr, "  → %s\n", filepath.Join(".", knowledge.FileName))
	return nil
}
