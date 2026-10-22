// Package termsafe makes text from outside ouija — a repository's files and
// configs, a model's replies — safe to print to a terminal.
package termsafe

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Clean drops the control characters a terminal acts on — ESC and the other
// C0 controls, DEL, the C1 range — keeping newline and tab. Text from a
// repository or a model could otherwise set the clipboard (OSC 52), erase or
// rewrite lines already on screen (hiding what `ouija trust` grants), or
// change the window title.
//
// It also drops the invisible characters that make text read differently
// from what it is: the Unicode format characters (bidi controls, which
// reorder what follows them on screen, zero-width characters, the soft
// hyphen, and the tag characters U+E0000–E007F, which a terminal shows as
// nothing but a model reads as ASCII), the line and paragraph separators, the
// Hangul fillers, and the variation selectors. The zero-width joiner is kept
// between two non-ASCII characters, where it builds emoji sequences and
// shapes Indic and Arabic text, and the emoji presentation selectors VS15 and
// VS16 right after a non-ASCII character; next to ASCII they only hide
// something.
func Clean(s string) string {
	return filter(s, func(*strings.Builder, rune) {})
}

// Reveal is Clean for text a person must review rather than just read, such
// as a prompt `ouija trust` is about to grant: instead of dropping a control
// or invisible character it shows it as a visible \u{XXXX} escape, so an
// instruction hidden in one cannot pass unseen.
func Reveal(s string) string {
	return filter(s, func(b *strings.Builder, r rune) { fmt.Fprintf(b, `\u{%04X}`, r) })
}

// Hidden reports whether s holds a character Clean drops and Reveal escapes.
func Hidden(s string) bool { return Clean(s) != s }

// filter copies s, handing each rune Clean drops to drop instead.
func filter(s string, drop func(*strings.Builder, rune)) string {
	if strings.IndexFunc(s, unsafe) < 0 {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	prev := rune(-1) // the last rune kept
	for i, r := range s {
		switch {
		case r == zwj:
			next, _ := utf8.DecodeRuneInString(s[i+utf8.RuneLen(r):])
			if prev < utf8.RuneSelf || next < utf8.RuneSelf || next == utf8.RuneError || unsafe(next) {
				drop(&b, r)
				continue
			}
		case r == vs15 || r == vs16:
			// One selector per character: a run of them carries bits.
			if prev < utf8.RuneSelf || prev == vs15 || prev == vs16 || prev == zwj {
				drop(&b, r)
				continue
			}
		case unsafe(r):
			drop(&b, r)
			continue
		}
		b.WriteRune(r)
		prev = r
	}
	return b.String()
}

const (
	zwj  = '‍'
	vs15 = '︎' // text presentation
	vs16 = '️' // emoji presentation
)

// unsafe reports whether r is a control or invisible character. The joiner
// and VS15/VS16 count: Clean keeps them only where they are needed.
func unsafe(r rune) bool {
	switch {
	case r == '\n' || r == '\t':
		return false
	case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
		return true
	case r == 0x2028, r == 0x2029, // line and paragraph separators
		r == 0x115f, r == 0x1160, r == 0x3164, r == 0xffa0, // Hangul fillers
		r >= 0xfe00 && r <= 0xfe0f, r >= 0xe0100 && r <= 0xe01ef: // variation selectors
		return true
	case r < 0xad:
		return false
	case unicode.Is(unicode.Cf, r):
		// Bidi controls, zero-width characters, the soft hyphen, the
		// invisible operators, tags. The prepended concatenation marks
		// (Arabic number signs and the like) are visible: keep them.
		return !unicode.Is(unicode.Prepended_Concatenation_Mark, r)
	}
	return false
}
