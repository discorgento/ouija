package commands

import "github.com/discorgento/ouija/internal/art"

// printBanner shows the ouija logo with a short rainbow animation before the
// setup wizard — same lively feel as the chat TUI.
func printBanner() {
	art.Animate("project-aware AI assistant")
}
