package termsafe

import "testing"

func TestClean(t *testing.T) {
	for in, want := range map[string]string{
		"plain\n\ttext é 🔮":                 "plain\n\ttext é 🔮",
		"\x1b]52;c;Y3VybCBldmlsfHNo\x07git": "]52;c;Y3VybCBldmlsfHNogit",
		"git\x1b[1A\x1b[2K\r  tools":        "git[1A[2K  tools",
		"a\x7fb\u009bc":                     "abc",
		// Bidi controls reorder what follows: an approval prompt would show
		// one command and run another.
		"ls \u202e# hs.txt\u202c":                            "ls # hs.txt",
		"a\u2066b\u2067c\u2068d\u2069e\u200ef\u200fg\u061ch": "abcdefgh",
		// Zero-width characters hide inside a word.
		"r\u200bm\u200c \u2060-rf\ufeff": "rm -rf",
		// The joiner next to ASCII only hides; between emoji it builds one.
		"r\u200dm":        "rm",
		"\u200dgit\u200d": "git",
		"👩\u200d💻 👨\u200d👩\u200d👧": "👩\u200d💻 👨\u200d👩\u200d👧",
		"👩\u200d\u202e💻":           "👩💻",
		// Tag characters spell ASCII a model reads and a terminal hides.
		"ok\U000e0072\U000e006d\U000e0020\U000e002d\U000e0072\U000e0066\U000e007f": "ok",
		// Soft hyphen, separators, Mongolian vowel separator, invisible
		// operators, Hangul fillers.
		"a\u00adb\u2028c\u2029d\u180ee\u2061f\u2064g": "abcdefg",
		"x\u115fy\u1160z\u3164w\uffa0v":               "xyzwv",
		// Variation selectors: VS15/VS16 stay once after a non-ASCII rune.
		"a\ufe00b\U000e0100c\U000e01efd":       "abcd",
		"❤\ufe0f ☺\ufe0e 👁\ufe0f\u200d🗨\ufe0f": "❤\ufe0f ☺\ufe0e 👁\ufe0f\u200d🗨\ufe0f",
		"a\ufe0f ❤\ufe0f\ufe0e\ufe0f":          "a ❤\ufe0f",
		// Joiners in a run carry bits too.
		"é\u200d\u200dé": "é\u200dé",
		// Visible format characters stay.
		"\u0600١": "\u0600١",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// Reveal shows what Clean drops instead of dropping it, and Hidden says
// whether there is anything to show.
func TestReveal(t *testing.T) {
	for in, want := range map[string]string{
		"plain\n\ttext 👩\u200d💻 ❤\ufe0f": "plain\n\ttext 👩\u200d💻 ❤\ufe0f",
		"be nice\U000e0068\U000e0069":    `be nice\u{E0068}\u{E0069}`,
		"\x1b]52;c;x\x07":                `\u{001B}]52;c;x\u{0007}`,
		"a\u200bb\u202ec\ufe00d\u3164e":  `a\u{200B}b\u{202E}c\u{FE00}d\u{3164}e`,
		"r\u200dm":                       `r\u{200D}m`,
	} {
		if got := Reveal(in); got != want {
			t.Errorf("Reveal(%q) = %q, want %q", in, got, want)
		}
		if got := Hidden(in); got != (in != want) {
			t.Errorf("Hidden(%q) = %v", in, got)
		}
	}
}
