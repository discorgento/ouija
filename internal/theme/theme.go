// Package theme is ouija's one palette: the chat TUI, the setup wizard and the
// CLI banner all take their colors from here, so they cannot drift apart.
package theme

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Brand colors (ANSI 256, so they hold in terminals without truecolor).
var (
	Pink     = lipgloss.Color("213") // user, highlights
	Blush    = lipgloss.Color("225") // soft highlight
	Lavender = lipgloss.Color("141") // accents, focused fields
	Mauve    = lipgloss.Color("140") // secondary text on accent
	Violet   = lipgloss.Color("99")  // headers, values
	Purple   = lipgloss.Color("93")  // ouija (AI)
)

// Neutrals.
var (
	// Text is the terminal's own text color: body text reads on any
	// terminal theme, light or dark.
	Text   color.Color = lipgloss.NoColor{}
	Bright             = lipgloss.Color("255") // text on a colored background
	Silver             = lipgloss.Color("250") // secondary text
	Gray               = lipgloss.Color("243") // trace, inactive
	Muted              = lipgloss.Color("60")  // hints, separators
	Shade              = lipgloss.Color("236") // unselected button background

	// The UI paints no background of its own (the terminal's shows): these
	// are for text on a colored pill and the like.
	Surface       = lipgloss.Color("#12101e")
	SurfaceRaised = lipgloss.Color("#1e1930")

	DiffAddBg = lipgloss.Color("#13301f") // background of an added line
	DiffDelBg = lipgloss.Color("#3a1424") // background of a removed line
)

// Status colors.
var (
	Green     = lipgloss.Color("118") // success, diff additions, read-only tools
	GreenDeep = lipgloss.Color("71")  // "yes" button
	Amber     = lipgloss.Color("220") // warnings, writes, diff hunks
	Orange    = lipgloss.Color("208") // edit, outside the sandbox
	Red       = lipgloss.Color("196") // errors, deletions, shell
	RedDeep   = lipgloss.Color("131") // "no" button
)

// Rainbow is the logo's per-letter cycle (ANSI 256).
var Rainbow = []color.Color{Pink, Lavender, Violet, Purple, Mauve, Blush}

// gradientStops are the truecolor stops of the brand gradient. It ends where
// it starts, so a gradient that flows (Gradient with a growing shift) wraps
// around without a seam.
var gradientStops = []color.Color{
	lipgloss.Color("#ff5fd7"), // hot pink
	lipgloss.Color("#af87ff"), // lavender
	lipgloss.Color("#875fff"), // violet
	lipgloss.Color("#8700ff"), // deep purple
	lipgloss.Color("#af87ff"),
	lipgloss.Color("#ff5fd7"),
}

// Gradient returns n colors along the brand gradient, which spans 2n steps
// (n shows half of it), rotated by shift steps: redrawing with a growing
// shift makes the colors flow.
func Gradient(n, shift int) []color.Color {
	if n <= 0 {
		return nil
	}
	cycle := lipgloss.Blend1D(2*n, gradientStops...)
	out := make([]color.Color, n)
	for i := range out {
		out[i] = cycle[(i+shift)%len(cycle)]
	}
	return out
}

// GradientText paints s left to right along the brand gradient, bold.
// Spaces keep no color, so a gradient over a logo only colors its strokes.
func GradientText(s string, shift int) string {
	runes := []rune(s)
	colors := Gradient(len(runes), shift)
	var b strings.Builder
	for i, r := range runes {
		if r == ' ' {
			b.WriteRune(r)
			continue
		}
		b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colors[i]).Render(string(r)))
	}
	return b.String()
}
