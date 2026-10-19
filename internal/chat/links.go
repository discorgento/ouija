package chat

import (
	"net/url"
	"path/filepath"

	"github.com/charmbracelet/x/ansi"
)

// linkRoot is the project root file links resolve against (set by newModel).
var linkRoot string

// fileLink makes text a terminal hyperlink (OSC 8) to the project file path:
// a click opens it in the terminal's handler (kitty, WezTerm, iTerm2, GNOME
// terminals); a terminal without links shows the text alone.
func fileLink(path, text string) string {
	if path == "" || text == "" {
		return text
	}
	abs := path
	if !filepath.IsAbs(abs) {
		if linkRoot == "" {
			return text
		}
		abs = filepath.Join(linkRoot, path)
	}
	u := url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}
	return ansi.SetHyperlink(u.String()) + text + ansi.ResetHyperlink()
}

// pathTools are the tools whose target is a project path (linkable).
var pathTools = map[string]bool{"read_file": true, "write_file": true, "edit_file": true, "delete_file": true, "list_dir": true}
