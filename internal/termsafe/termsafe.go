// Package termsafe makes text from outside ouija — a repository's files and
// configs, a model's replies — safe to print to a terminal.
package termsafe

import "strings"

// Clean drops the control characters a terminal acts on — ESC and the other
// C0 controls, DEL, the C1 range — keeping newline and tab. Text from a
// repository or a model could otherwise set the clipboard (OSC 52), erase or
// rewrite lines already on screen (hiding what `ouija trust` grants), or
// change the window title.
func Clean(s string) string {
	if strings.IndexFunc(s, unsafe) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		if unsafe(r) {
			return -1
		}
		return r
	}, s)
}

func unsafe(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	}
	return false
}
