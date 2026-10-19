// Package art holds shared banner/logo assets used by the CLI and TUI.
package art

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/discorgento/ouija/internal/theme"
)

// OuijaArt is the figlet rendering of "OUIJA", shared between the CLI wizard
// and the chat TUI so the logo is always the same everywhere.
var OuijaArt = []string{
	`   ____  __  ______    _____ `,
	`  / __ \/ / / /  _/   / /   |`,
	` / / / / / / // /__  / / /| |`,
	`/ /_/ / /_/ // // /_/ / ___ |`,
	`\____/\____/___/\____/_/  |_|`,
}

// renderArt builds the colored logo lines for a given time-based shift.
func renderArt(shift int, subtitle string) []string {
	var out []string
	for _, line := range OuijaArt {
		var b strings.Builder
		ci := shift
		for _, r := range line {
			if r != ' ' {
				c := theme.Rainbow[ci%len(theme.Rainbow)]
				s := lipgloss.NewStyle().Bold(true).Foreground(c)
				b.WriteString(s.Render(string(r)))
				ci++
			} else {
				b.WriteRune(r)
			}
		}
		out = append(out, b.String())
	}
	if subtitle != "" {
		s := lipgloss.NewStyle().Foreground(theme.Muted)
		out = append(out, "", s.Render("  "+subtitle))
	}
	return out
}

// Print renders the ouija logo once with a time-based rainbow gradient.
func Print(subtitle string) {
	shift := int(time.Now().UnixMilli()/200) % len(theme.Rainbow)
	fmt.Println()
	for _, l := range renderArt(shift, subtitle) {
		fmt.Println(l)
	}
	fmt.Println()
}

// Animate renders the logo with a short rainbow cycling effect (8 frames ×
// 100ms). It leaves the last frame visible. Uses per-line clear + cursor-up,
// which works in most modern terminals (konsole, iterm2, vscode, gnome).
func Animate(subtitle string) {
	art := renderArt(0, subtitle)
	total := 1 + len(art) + 1 // blank + art lines + blank

	for frame := 0; frame < 8; frame++ {
		shift := frame % len(theme.Rainbow)
		lines := renderArt(shift, subtitle)

		if frame == 0 {
			fmt.Println()
			for _, l := range lines {
				fmt.Println(l)
			}
			fmt.Println()
		} else {
			// Clear each line from bottom to top, then redraw.
			for i := 0; i < total; i++ {
				fmt.Print("\033[A\033[2K\r")
			}
			fmt.Println()
			for _, l := range lines {
				fmt.Println(l)
			}
			fmt.Println()
		}
		time.Sleep(100 * time.Millisecond)
	}
}
