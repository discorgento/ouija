package art

import (
	"regexp"
	"strings"
	"testing"
)

var ansi = regexp.MustCompile("\x1b\\[[0-9;]*m")

// stripANSI removes SGR color codes so the test compares the visible glyphs,
// whether or not the test environment supports color.
func stripANSI(s string) string { return ansi.ReplaceAllString(s, "") }

// The logo is a shared, stable asset (identical in the wizard and the TUI).
// Pin the line count and the first line so it cannot disappear silently and
// make the comparisons below vacuous.
func TestOuijaArt_IsNonTrivial(t *testing.T) {
	if len(OuijaArt) != 5 {
		t.Fatalf("OuijaArt has %d lines, want the 5-line figlet logo", len(OuijaArt))
	}
	const firstLine = `   ____  __  ______    _____ `
	if OuijaArt[0] != firstLine {
		t.Errorf("first line = %q, want %q", OuijaArt[0], firstLine)
	}
	for i, line := range OuijaArt {
		if strings.TrimSpace(line) == "" {
			t.Errorf("line %d is blank; the logo is broken", i)
		}
	}
}

// The CLI wizard and the chat TUI render the same logo; a style or padding
// change here would visibly break the logo in one of them. Spacing must be
// preserved exactly.
func TestRenderArt_KeepsLogoIntact(t *testing.T) {
	lines := renderArt(0, "")
	if len(lines) != len(OuijaArt) {
		t.Fatalf("renderArt() returned %d lines, want %d", len(lines), len(OuijaArt))
	}
	for i, line := range lines {
		if got := stripANSI(line); got != OuijaArt[i] {
			t.Errorf("line %d = %q, want %q", i, got, OuijaArt[i])
		}
	}
}

func TestRenderArt_AppendsSubtitleAfterBlankLine(t *testing.T) {
	const sub = "multi-totem AI harness"

	lines := renderArt(3, sub)
	if len(lines) != len(OuijaArt)+2 {
		t.Fatalf("renderArt() with a subtitle returned %d lines, want %d", len(lines), len(OuijaArt)+2)
	}
	if lines[len(OuijaArt)] != "" {
		t.Errorf("line before the subtitle = %q, want a blank separator", lines[len(OuijaArt)])
	}
	if last := stripANSI(lines[len(lines)-1]); !strings.Contains(last, sub) {
		t.Errorf("last line = %q, want it to contain the subtitle", last)
	}
}

func TestRenderArt_AllShiftsAreStable(t *testing.T) {
	for shift := 0; shift < 12; shift++ {
		if lines := renderArt(shift, ""); len(lines) != len(OuijaArt) {
			t.Fatalf("renderArt(%d) returned %d lines, want %d", shift, len(lines), len(OuijaArt))
		}
	}
}
