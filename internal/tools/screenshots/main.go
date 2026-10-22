// Command screenshots turns the chat scenes written by
// `go test ./internal/chat -run Screenshots -screens=<dir>` (ANSI, one .ans
// file per scene) into the README's visuals: each still is drawn as HTML in
// a window frame like assets/install.gif's, and headless Chrome captures it;
// each animation's frames (a directory of them) become a GIF.
//
// Usage:
//
//	go run ./internal/tools/screenshots -in <dir> -out assets/screens -gif-out assets
//
// `make screens` runs both steps. Every PNG has the same size: the terminal
// is cols×rows cells, the window -width CSS pixels at -scale device pixels
// each.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func main() {
	in := flag.String("in", "", "directory with the scenes' .ans files")
	out := flag.String("out", "assets/screens", "directory for the PNGs")
	gifOut := flag.String("gif-out", "assets", "directory for the GIFs")
	gifs := flag.String("gif", "", "the animations to render (comma separated; every one by default)")
	gifScale := flag.Int("gif-scale", 1, "device pixels per CSS pixel in the GIFs")
	cols := flag.Int("cols", 100, "terminal width in cells (the scenes' width)")
	rows := flag.Int("rows", 30, "terminal height in cells (the scenes' height)")
	width := flag.Int("width", 800, "window width in CSS pixels")
	scale := flag.Int("scale", 2, "device pixels per CSS pixel")
	title := flag.String("title", "ouija chat", "the title bar's label")
	flag.Parse()

	scenes, err := filepath.Glob(filepath.Join(*in, "*.ans"))
	if err != nil || len(scenes) == 0 {
		fail(fmt.Errorf("no .ans scenes in %q", *in))
	}
	chrome := findChrome()
	if chrome == "" {
		fail(errors.New("no Chrome or Chromium found"))
	}
	l := newLayout(*cols, *rows, *width)
	if err := os.MkdirAll(*out, 0o755); err != nil {
		fail(err)
	}
	for _, scene := range scenes {
		name := strings.TrimSuffix(filepath.Base(scene), ".ans")
		src, err := os.ReadFile(scene)
		if err != nil {
			fail(err)
		}
		page := l.page(*title, parseANSI(string(src), *cols, *rows))
		png, err := filepath.Abs(filepath.Join(*out, name+".png"))
		if err != nil {
			fail(err)
		}
		if err := capture(chrome, page, png, *width, l.height, *scale); err != nil {
			fail(fmt.Errorf("%s: %w", name, err))
		}
		fmt.Printf("ok → %s (%dx%d)\n", filepath.Join(*out, name+".png"), *width**scale, l.height**scale)
	}

	// The animations: a directory of frames each.
	animations, err := filepath.Glob(filepath.Join(*in, "*"))
	if err != nil {
		fail(err)
	}
	if err := os.MkdirAll(*gifOut, 0o755); err != nil {
		fail(err)
	}
	for _, dir := range animations {
		info, err := os.Stat(dir)
		if err != nil || !info.IsDir() {
			continue
		}
		name := filepath.Base(dir)
		if !wanted(*gifs, name) {
			continue
		}
		frames, err := readFrames(dir)
		if err != nil {
			fail(fmt.Errorf("%s: %w", name, err))
		}
		gifPath, err := filepath.Abs(filepath.Join(*gifOut, name+".gif"))
		if err != nil {
			fail(err)
		}
		if err := writeGIF(chrome, l, *title, frames, gifPath, *gifScale); err != nil {
			fail(fmt.Errorf("%s: %w", name, err))
		}
		fmt.Printf("ok → %s (%d frames)\n", filepath.Join(*gifOut, name+".gif"), len(frames))
	}
}

// wanted tells whether the animation is one the -gif list names (every one
// when the list is empty).
func wanted(list, name string) bool {
	if list == "" {
		return true
	}
	for _, n := range strings.Split(list, ",") {
		if strings.TrimSpace(n) == name {
			return true
		}
	}
	return false
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "error: %v\n", err)
	os.Exit(1)
}

// ─── ANSI → cells ──────────────────────────────────────────────────────

// style is a cell's look, as SGR sets it. Colors are CSS colors, "" for the
// terminal's default.
type style struct {
	fg, bg                                      string
	bold, faint, italic, underline, rev, strike bool
}

// cell is one terminal cell: its text ("" for the right half of a wide
// character) and how wide it draws.
type cell struct {
	text  string
	width int
	st    style
}

// parseANSI lays the screen out on a cols×rows grid. SGR sets the style;
// hyperlinks (OSC 8) and every other sequence are dropped.
func parseANSI(s string, cols, rows int) [][]cell {
	grid := make([][]cell, rows)
	for i := range grid {
		grid[i] = make([]cell, cols)
		for j := range grid[i] {
			grid[i][j] = cell{text: " ", width: 1}
		}
	}
	var st style
	r, c := 0, 0
	for i := 0; i < len(s); {
		switch b := s[i]; {
		case b == '\x1b' && i+1 < len(s) && s[i+1] == '[':
			j := i + 2
			for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
				j++
			}
			if j < len(s) && s[j] == 'm' {
				st = applySGR(st, s[i+2:j])
			}
			i = j + 1
		case b == '\x1b' && i+1 < len(s) && s[i+1] == ']':
			// OSC: up to BEL or ESC \.
			j := i + 2
			for j < len(s) && s[j] != '\a' && !(s[j] == '\x1b' && j+1 < len(s) && s[j+1] == '\\') {
				j++
			}
			if j < len(s) && s[j] == '\x1b' {
				j++
			}
			i = j + 1
		case b == '\x1b':
			i += 2
		case b == '\n':
			r, c = r+1, 0
			i++
		case b == '\r':
			c = 0
			i++
		default:
			ch, size := nextRune(s[i:])
			i += size
			w := ansi.StringWidth(ch)
			if w == 0 {
				// A combining mark joins the cell before it.
				if c > 0 && r < rows && c-1 < cols {
					grid[r][c-1].text += ch
				}
				continue
			}
			if r < rows && c+w <= cols {
				grid[r][c] = cell{text: ch, width: w, st: st}
				for k := 1; k < w; k++ {
					grid[r][c+k] = cell{st: st}
				}
			}
			c += w
		}
	}
	return grid
}

// nextRune returns the first character of s (invalid UTF-8 as one byte).
func nextRune(s string) (string, int) {
	for n := 1; n <= 4 && n <= len(s); n++ {
		if r := []rune(s[:n]); len(r) == 1 && r[0] != '�' {
			return s[:n], n
		}
	}
	return s[:1], 1
}

// applySGR applies one SGR sequence's parameters ("1;38;2;r;g;b", or with
// colons) to st.
func applySGR(st style, params string) style {
	if params == "" {
		return style{}
	}
	ps := strings.FieldsFunc(params, func(r rune) bool { return r == ';' || r == ':' })
	num := func(i int) int {
		if i >= len(ps) {
			return 0
		}
		n, _ := strconv.Atoi(ps[i])
		return n
	}
	// color reads an extended color (5;n or 2;r;g;b) at ps[i] and returns
	// it with the index after it.
	color := func(i int) (string, int) {
		switch num(i) {
		case 5:
			return xterm256(num(i + 1)), i + 2
		case 2:
			// 38:2::r:g:b carries an empty color-space id: FieldsFunc dropped it.
			return fmt.Sprintf("#%02x%02x%02x", num(i+1), num(i+2), num(i+3)), i + 4
		}
		return "", i + 1
	}
	for i := 0; i < len(ps); i++ {
		switch n := num(i); {
		case n == 0:
			st = style{}
		case n == 1:
			st.bold = true
		case n == 2:
			st.faint = true
		case n == 3:
			st.italic = true
		case n == 4:
			st.underline = true
		case n == 7:
			st.rev = true
		case n == 9:
			st.strike = true
		case n == 22:
			st.bold, st.faint = false, false
		case n == 23:
			st.italic = false
		case n == 24:
			st.underline = false
		case n == 27:
			st.rev = false
		case n == 29:
			st.strike = false
		case n >= 30 && n <= 37:
			st.fg = xterm256(n - 30)
		case n >= 90 && n <= 97:
			st.fg = xterm256(n - 90 + 8)
		case n == 39:
			st.fg = ""
		case n >= 40 && n <= 47:
			st.bg = xterm256(n - 40)
		case n >= 100 && n <= 107:
			st.bg = xterm256(n - 100 + 8)
		case n == 49:
			st.bg = ""
		case n == 38:
			st.fg, i = color(i + 1)
			i--
		case n == 48:
			st.bg, i = color(i + 1)
			i--
		case n == 58:
			_, i = color(i + 1) // underline color: not drawn
			i--
		}
	}
	return st
}

// ansi16 is the terminal's 16-color palette, tuned to the window's purple.
var ansi16 = [16]string{
	"#1e1930", "#f0628a", "#8be28b", "#f5c26b", "#8aa8ff", "#c792ea", "#7fd8d8", "#cfc8e0",
	"#5c5470", "#ff7a9c", "#a6f0a0", "#ffd88a", "#a6bcff", "#dcaaff", "#9ae8e8", "#f4f0fa",
}

// xterm256 returns the CSS color of a 256-color index.
func xterm256(n int) string {
	switch {
	case n < 16:
		return ansi16[max(n, 0)]
	case n < 232:
		n -= 16
		level := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		return fmt.Sprintf("#%02x%02x%02x", level(n/36), level(n/6%6), level(n%6))
	case n < 256:
		v := 8 + (n-232)*10
		return fmt.Sprintf("#%02x%02x%02x", v, v, v)
	}
	return ""
}

// ─── cells → HTML ──────────────────────────────────────────────────────

// The window, measured on assets/install.gif: a purple 1px border, a 40px
// title bar with three dots, the terminal on a near-black violet.
const (
	pageBg   = "#0d1117" // around the rounded corners (GitHub's dark page)
	winBg    = "#0f081a"
	winFg    = "#e6e1f0"
	barBg    = "#160d29"
	border   = "#3d2c68"
	titleFg  = "#6b5f8f"
	barH     = 40
	padding  = 16
	radius   = 8
	fontsCSS = `"JetBrains Mono", "JetBrainsMono Nerd Font Mono", "JetBrainsMono NFM", "Adwaita Mono", "DejaVu Sans Mono", "DejaVu Sans", Menlo, Consolas, monospace`
)

// layout is the geometry every scene shares, in CSS pixels.
type layout struct {
	cols, rows, width int
	cell, line        float64 // a cell's width, a row's height
	height            int
}

func newLayout(cols, rows, width int) layout {
	l := layout{cols: cols, rows: rows, width: width}
	l.cell = float64(width-2-2*padding) / float64(cols)
	// A bit under the font's ascent plus descent (1.32em; a cell is 0.6em):
	// box drawing and blocks then join from row to row.
	l.line = l.cell / 0.6 * 1.25
	l.height = 1 + barH + 1 + padding + int(float64(rows)*l.line+0.999) + padding + 1
	return l
}

// page returns the scene as a self-contained HTML page of exactly
// l.width×l.height.
func (l layout) page(title string, grid [][]cell) string {
	var b strings.Builder
	fmt.Fprintf(&b, `<!DOCTYPE html><html><head><meta charset="utf-8"><style>
html, body { margin: 0; width: %dpx; height: %dpx; background: %s; overflow: hidden; }
.win { box-sizing: border-box; width: 100%%; height: 100%%; border: 1px solid %s; border-radius: %dpx; background: %s; overflow: hidden; }
.bar { position: relative; height: %dpx; background: %s; border-bottom: 1px solid %s; }
.bar i { position: absolute; top: 14px; width: 12px; height: 12px; border-radius: 50%%; }
.bar i:nth-child(1) { left: 18px; background: #c88a9f; }
.bar i:nth-child(2) { left: 39px; background: #e2cf8e; }
.bar i:nth-child(3) { left: 59px; background: #96d89d; }
.bar span { position: absolute; inset: 0; text-align: center; font: 13px/%dpx %s; color: %s; }
.term { padding: %dpx; color: %s; font-family: %s; font-size: %.3fpx; line-height: %.3fpx;
  font-variant-ligatures: none; -webkit-font-smoothing: antialiased; }
.r { height: %.3fpx; white-space: pre; }
.r span { display: inline-block; overflow: hidden; vertical-align: top; height: 100%%; }
</style></head><body><div class="win"><div class="bar"><i></i><i></i><i></i><span>%s</span></div><div class="term">
`, l.width, l.height, pageBg, border, radius, winBg, barH, barBg, border, barH, fontsCSS, titleFg,
		padding, winFg, fontsCSS, l.cell/0.6, l.line, l.line, html.EscapeString(title))
	for _, row := range grid {
		b.WriteString(`<div class="r">`)
		// A run of plain ASCII cells in one style is one span; any other
		// character gets its own, so a fallback font cannot shift the grid.
		for i := 0; i < len(row); {
			c := row[i]
			if c.text == "" {
				i++
				continue
			}
			n, text := c.width, c.text
			if isASCII(c.text) {
				j := i + 1
				for j < len(row) && row[j].st == c.st && row[j].text != "" && isASCII(row[j].text) {
					text += row[j].text
					j++
				}
				n = j - i
				i = j
			} else {
				i += c.width
			}
			fmt.Fprintf(&b, `<span style="width:%.3fpx;%s">%s</span>`, float64(n)*l.cell, css(c.st), html.EscapeString(text))
		}
		b.WriteString("</div>\n")
	}
	b.WriteString("</div></div></body></html>\n")
	return b.String()
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// css returns the inline CSS of a style.
func css(st style) string {
	fg, bg := st.fg, st.bg
	if st.rev {
		fg, bg = or(bg, winBg), or(fg, winFg)
	}
	var b strings.Builder
	if fg != "" {
		b.WriteString("color:" + fg + ";")
	}
	if bg != "" {
		b.WriteString("background:" + bg + ";")
	}
	if st.bold {
		b.WriteString("font-weight:700;")
	}
	if st.faint {
		b.WriteString("opacity:.6;")
	}
	if st.italic {
		b.WriteString("font-style:italic;")
	}
	switch {
	case st.underline && st.strike:
		b.WriteString("text-decoration:underline line-through;")
	case st.underline:
		b.WriteString("text-decoration:underline;")
	case st.strike:
		b.WriteString("text-decoration:line-through;")
	}
	return b.String()
}

func or(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// ─── HTML → PNG ────────────────────────────────────────────────────────

// capture renders page with headless Chrome into png, w×h CSS pixels at
// scale. Chrome runs offline, on a throwaway profile.
func capture(chrome, page, png string, w, h, scale int) error {
	dir, err := os.MkdirTemp("", "ouija-screens-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	htmlPath := filepath.Join(dir, "scene.html")
	if err := os.WriteFile(htmlPath, []byte(page), 0o644); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, chrome,
		"--headless", "--disable-gpu", "--hide-scrollbars",
		"--user-data-dir="+filepath.Join(dir, "profile"),
		"--force-device-scale-factor="+strconv.Itoa(scale),
		fmt.Sprintf("--window-size=%d,%d", w, h),
		"--host-resolver-rules=MAP * ~NOTFOUND", "--proxy-server=http://127.0.0.1:9",
		"--disable-background-networking", "--no-first-run",
		"--screenshot="+png, "file://"+filepath.ToSlash(htmlPath))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// findChrome looks on PATH, then where macOS installs the browsers (like
// totems/doc).
func findChrome() string {
	for _, c := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "chrome", "msedge"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	if runtime.GOOS == "darwin" {
		for _, p := range []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
		} {
			if _, err := os.Stat(p); err == nil {
				return p
			}
		}
	}
	return ""
}
