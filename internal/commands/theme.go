package commands

import (
	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
)

// ouijaTheme implements huh.Theme with a bold neon-on-dark look.
type ouijaTheme struct{}

func (ouijaTheme) Theme(_ bool) *huh.Styles {
	t := huh.ThemeBase(true)

	var (
		purple  = lipgloss.Color("141") // same as chat bannerGrad
		hotPink = lipgloss.Color("213") // same as chat bannerColors[0]
		dim     = lipgloss.Color("140") // same as chat bannerColors[4]
		gray    = lipgloss.Color("60")
		white   = lipgloss.Color("225") // same as chat boldStyle
	)

	// ─── Focused ─────────────────────────────────────────────────────
	t.Focused.Base = lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(purple).
		Padding(0, 1)

	t.Focused.Title = lipgloss.NewStyle().
		Foreground(purple).
		Bold(true).
		Padding(0, 1)

	t.Focused.Description = lipgloss.NewStyle().
		Foreground(dim).
		Italic(true).
		Padding(0, 1)

	// Select
	t.Focused.SelectSelector = lipgloss.NewStyle().
		Foreground(hotPink).
		SetString("◆ ")

	t.Focused.SelectedPrefix = lipgloss.NewStyle().
		Foreground(white).
		SetString("● ")

	t.Focused.UnselectedPrefix = lipgloss.NewStyle().
		Foreground(gray).
		SetString("○ ")

	t.Focused.FocusedButton = lipgloss.NewStyle().
		Foreground(purple).
		Bold(true).
		Padding(0, 1)

	t.Focused.BlurredButton = lipgloss.NewStyle().
		Foreground(dim).
		Padding(0, 1)

	// TextInput
	t.Focused.TextInput.Cursor = lipgloss.NewStyle().
		Foreground(hotPink)

	t.Focused.TextInput.Placeholder = lipgloss.NewStyle().
		Foreground(gray).
		Italic(true)

	// ─── Blurred ────────────────────────────────────────────────────
	t.Blurred.Base = lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(gray).
		Padding(0, 1)

	t.Blurred.Title = lipgloss.NewStyle().
		Foreground(gray).
		Padding(0, 1)

	t.Blurred.Description = lipgloss.NewStyle().
		Foreground(gray).
		Italic(true).
		Padding(0, 1)

	t.Blurred.SelectSelector = lipgloss.NewStyle().
		Foreground(gray).
		SetString("  ")

	t.Blurred.SelectedPrefix = lipgloss.NewStyle().
		Foreground(gray).
		SetString("● ")

	t.Blurred.UnselectedPrefix = lipgloss.NewStyle().
		Foreground(gray).
		SetString("○ ")

	t.Blurred.FocusedButton = lipgloss.NewStyle().
		Foreground(purple).
		Bold(true).
		Padding(0, 1)

	t.Blurred.BlurredButton = lipgloss.NewStyle().
		Foreground(gray).
		Padding(0, 1)

	t.Blurred.TextInput.Cursor = lipgloss.NewStyle().
		Foreground(hotPink)

	t.Blurred.TextInput.Placeholder = lipgloss.NewStyle().
		Foreground(gray).
		Italic(true)

	return t
}
