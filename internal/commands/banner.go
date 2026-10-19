package commands

import (
	"github.com/discorgento/ouija/internal/art"
	"github.com/discorgento/ouija/internal/theme"
)

// printBanner shows the ouija logo with a short rainbow animation before the
// setup wizard — same lively feel as the chat TUI.
func printBanner() {
	if theme.NoMotion {
		art.Print("project-aware AI assistant")
		return
	}
	art.Animate("project-aware AI assistant")
}
