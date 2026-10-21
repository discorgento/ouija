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

// seanceFrame is one frame of the séance intro: the logo shown up to column
// reveal with a diagonal gradient sheen (shift moves it), the planchette (◉)
// under the last column shown, and the letters it has passed spelled out.
// reveal < 0 is the finished logo with the subtitle.
func seanceFrame(shift, reveal int, subtitle string) []string {
	w := 0
	for _, l := range OuijaArt {
		w = max(w, len(l))
	}
	colors := theme.Gradient(w+2*len(OuijaArt), shift)
	var out []string
	for row, l := range OuijaArt {
		var b strings.Builder
		for col, r := range l + strings.Repeat(" ", w-len(l)) {
			if r == ' ' || (reveal >= 0 && col > reveal) {
				b.WriteRune(' ')
				continue
			}
			b.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colors[col+2*row]).Render(string(r)))
		}
		out = append(out, "  "+b.String())
	}
	if reveal >= 0 {
		col := min(reveal, w-1)
		pl := lipgloss.NewStyle().Bold(true).Foreground(theme.Pink).Render("◉")
		out = append(out, "  "+strings.Repeat(" ", col)+pl)
		spelled := "OUIJA"[:min(5, col*5/w+1)]
		out = append(out, "  "+theme.GradientText(strings.Join(strings.Split(spelled, ""), " · "), shift))
	} else {
		out = append(out, "")
		out = append(out, "  "+lipgloss.NewStyle().Foreground(theme.Muted).Render(subtitle))
	}
	return out
}

// Seance draws the intro of the CLI wizards: the planchette moves under the
// logo and spells its name, then the gradient sweeps across it (about 1.8s).
// It redraws in place and leaves the finished logo on screen.
func Seance(subtitle string) {
	w := 0
	for _, l := range OuijaArt {
		w = max(w, len(l))
	}
	height := len(OuijaArt) + 2
	draw := func(lines []string, first bool) {
		if !first {
			fmt.Printf("\033[%dA", height)
		}
		for _, l := range lines {
			fmt.Print("\033[2K" + l + "\n")
		}
	}
	fmt.Println()
	frame := 0
	for reveal := 0; reveal < w; reveal += 2 {
		draw(seanceFrame(frame, reveal, subtitle), frame == 0)
		frame++
		time.Sleep(55 * time.Millisecond)
	}
	for i := 0; i < 10; i++ {
		draw(seanceFrame(frame, -1, subtitle), false)
		frame++
		time.Sleep(60 * time.Millisecond)
	}
	fmt.Println()
}
