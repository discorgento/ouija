// Package theme is ouija's one palette: the chat TUI, the setup wizard and the
// CLI banner all take their colors from here, so they cannot drift apart.
package theme

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// Brand colors (ANSI 256, so they hold in terminals without truecolor); a
// theme other than ouija replaces them (themes.go).
var (
	Pink     color.Color = lipgloss.Color("177") // user, highlights (an orchid, more purple than pink)
	Blush    color.Color = lipgloss.Color("189") // soft highlight
	Lavender color.Color = lipgloss.Color("141") // accents, focused fields
	Mauve    color.Color = lipgloss.Color("140") // secondary text on accent
	Violet   color.Color = lipgloss.Color("99")  // headers, values
	Purple   color.Color = lipgloss.Color("93")  // ouija (AI)
)

// Neutrals.
var (
	// Text is the terminal's own text color: body text reads on any
	// terminal theme, light or dark.
	Text   color.Color = lipgloss.NoColor{}
	Bright color.Color = lipgloss.Color("255") // text on a colored background
	Silver color.Color = lipgloss.Color("250") // secondary text
	Gray   color.Color = lipgloss.Color("243") // trace, inactive
	Muted  color.Color = lipgloss.Color("60")  // hints, separators
	Shade  color.Color = lipgloss.Color("236") // unselected button background

	// The UI paints no background of its own (the terminal's shows): these
	// are for text on a colored pill and the like.
	Surface       = lipgloss.Color("#12101e")
	SurfaceRaised = lipgloss.Color("#1e1930")

	DiffAddBg color.Color = lipgloss.Color("#13301f") // background of an added line
	DiffDelBg color.Color = lipgloss.Color("#3a1424") // background of a removed line
)

// Status colors.
var (
	Green     color.Color = lipgloss.Color("118") // success, diff additions, read-only tools
	GreenDeep color.Color = lipgloss.Color("71")  // "yes" button
	Amber     color.Color = lipgloss.Color("220") // warnings, writes, diff hunks
	Orange    color.Color = lipgloss.Color("208") // edit, outside the sandbox
	Red       color.Color = lipgloss.Color("196") // errors, deletions, shell
	RedDeep   color.Color = lipgloss.Color("131") // "no" button
)

// Rainbow is the logo's per-letter cycle (ANSI 256).
var Rainbow = []color.Color{Pink, Lavender, Violet, Purple, Mauve, Blush}

// gradientStops are the truecolor stops of the brand gradient. It ends where
// it starts, so a gradient that flows (Gradient with a growing shift) wraps
// around without a seam.
var gradientStops = []color.Color{
	lipgloss.Color("#c39bff"), // lilac — the logo's gradient (assets/logo.svg)
	lipgloss.Color("#a78bfa"),
	lipgloss.Color("#8b5cf6"), // violet
	lipgloss.Color("#6d28d9"), // deep purple
	lipgloss.Color("#a78bfa"),
	lipgloss.Color("#c39bff"),
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
