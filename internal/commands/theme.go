package commands

import (
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/huh/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/discorgento/ouija/internal/theme"
)

// ouijaTheme implements huh.Theme with a bold neon-on-dark look.
type ouijaTheme struct{}

func (ouijaTheme) Theme(_ bool) *huh.Styles {
	t := huh.ThemeBase(true)

	var (
		purple  = theme.Lavender
		hotPink = theme.Pink
		dim     = theme.Mauve
		gray    = theme.Muted
		white   = theme.Blush
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

// printDone prints the card that ends a wizard: a ✓ title, label/value rows,
// warnings (⚠) and the next step. To stdout, in color where it shows.
func printDone(title string, rows [][2]string, warnings []string, next string) {
	writeDone(colorprofile.NewWriter(os.Stdout, os.Environ()), title, rows, warnings, next)
}

func writeDone(w io.Writer, title string, rows [][2]string, warnings []string, next string) {
	keyW := 0
	for _, r := range rows {
		keyW = max(keyW, len(r[0]))
	}
	var b strings.Builder
	b.WriteString(docOK.Render("✓ ") + lipgloss.NewStyle().Bold(true).Foreground(theme.Text).Render(title))
	for _, r := range rows {
		fmt.Fprintf(&b, "\n%s  %s", docLabel.Render(fmt.Sprintf("%-*s", keyW, r[0])), docText.Render(r[1]))
	}
	for _, warn := range warnings {
		b.WriteString("\n" + docWarn.UnsetBold().Render("⚠ "+warn))
	}
	if next != "" {
		b.WriteString("\n\n" + docDim.Render("next → ") + lipgloss.NewStyle().Bold(true).Foreground(theme.Pink).Render(next))
	}
	card := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(theme.Lavender).Padding(0, 1).Render(b.String())
	fmt.Fprintln(w, "\n"+card)
}
