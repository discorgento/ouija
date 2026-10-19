// Package progress draws a totem's run live on the terminal: one line per
// model run (scout, each pass, reconcile) with its state and time, under a
// header with the elapsed time, tokens and cost. It redraws in place, so it is
// only used on a terminal (Enabled).
package progress

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/term"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/termsafe"
	"github.com/discorgento/ouija/internal/theme"
)

// frameEvery is the redraw interval: smooth enough for the spinner.
const frameEvery = 100 * time.Millisecond

// maxRows is how many runs the board lists; older finished ones fold into a
// "… N more done" line.
const maxRows = 14

var spinnerFrames = []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}

var (
	styleOK      = lipgloss.NewStyle().Foreground(theme.Green)
	styleFail    = lipgloss.NewStyle().Foreground(theme.Red)
	styleRun     = lipgloss.NewStyle().Foreground(theme.Pink)
	styleLabel   = lipgloss.NewStyle().Foreground(theme.Text)
	styleDim     = lipgloss.NewStyle().Foreground(theme.Muted)
	styleMeta    = lipgloss.NewStyle().Foreground(theme.Lavender)
	styleWaiting = lipgloss.NewStyle().Foreground(theme.Gray)
)

// Enabled reports whether w is a terminal the board can redraw on.
func Enabled(w io.Writer) bool {
	f, ok := w.(*os.File)
	return ok && term.IsTerminal(f.Fd()) && os.Getenv("TERM") != "dumb"
}

// run is one model run on the board.
type run struct {
	label string
	start time.Time
	dur   time.Duration
	done  bool
	err   string
}

// Board is the live view of one totem run. Start it with the engine, Stop it
// when the totem returns; logs written meanwhile go above the board.
type Board struct {
	out   io.Writer // colorprofile writer: downsampled or stripped to fit the terminal
	fd    uintptr   // for the terminal width; 0 when unknown
	title string

	mu    sync.Mutex
	eng   *engine.Engine
	runs  []*run
	drawn int // lines the last frame took
	start time.Time
	frame int

	stop chan struct{}
	done chan struct{}
}

// New returns a board drawing on w (a terminal: see Enabled) for the totem
// named title.
func New(w io.Writer, title string) *Board {
	b := &Board{out: colorprofile.NewWriter(w, os.Environ()), title: title}
	if f, ok := w.(*os.File); ok {
		b.fd = f.Fd()
	}
	return b
}

// Start hooks the board to eng — its runs, its logs — and starts drawing.
func (b *Board) Start(eng *engine.Engine) {
	b.mu.Lock()
	b.eng, b.start = eng, time.Now()
	b.stop, b.done = make(chan struct{}), make(chan struct{})
	b.mu.Unlock()

	eng.SetRunCallback(b.onRun)
	// Logs go above the board, not through it: a line written mid-frame would
	// break the redraw.
	eng.SetLogger(slog.New(slog.NewTextHandler(logWriter{b}, &slog.HandlerOptions{
		Level: engine.LogLevel(),
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})))

	fmt.Fprint(b.out, "\x1b[?25l") // hide the cursor while drawing
	go func() {
		defer close(b.done)
		t := time.NewTicker(frameEvery)
		defer t.Stop()
		for {
			select {
			case <-b.stop:
				return
			case <-t.C:
				b.mu.Lock()
				b.frame++
				b.redraw()
				b.mu.Unlock()
			}
		}
	}()
}

// Stop draws the final frame, leaves it on screen and gives the engine its
// default logger back.
func (b *Board) Stop() {
	close(b.stop)
	<-b.done
	b.mu.Lock()
	defer b.mu.Unlock()
	b.redraw()
	fmt.Fprint(b.out, "\x1b[?25h\n") // the cursor back, below the board
	b.drawn = 0
	b.eng.SetRunCallback(nil)
	b.eng.SetLogger(nil)
}

// onRun records a run starting or ending.
func (b *Board) onRun(ev engine.RunEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !ev.Done {
		b.runs = append(b.runs, &run{label: ev.Label, start: time.Now()})
		return
	}
	// The latest unfinished run with this label (a label can repeat).
	for i := len(b.runs) - 1; i >= 0; i-- {
		if r := b.runs[i]; r.label == ev.Label && !r.done {
			r.done, r.dur = true, ev.Duration
			if ev.Err != nil {
				r.err = ev.Err.Error()
			}
			break
		}
	}
}

// logWriter writes log lines above the board.
type logWriter struct{ b *Board }

func (w logWriter) Write(p []byte) (int, error) {
	w.b.mu.Lock()
	defer w.b.mu.Unlock()
	w.b.clear()
	fmt.Fprint(w.b.out, styleDim.Render(strings.TrimRight(termsafe.Clean(string(p)), "\n"))+"\n")
	w.b.drawn = 0
	w.b.redraw()
	return len(p), nil
}

// clear erases the last frame and leaves the cursor where it began.
func (b *Board) clear() {
	if b.drawn > 0 {
		fmt.Fprintf(b.out, "\x1b[%dA\r\x1b[J", b.drawn)
	}
}

// width is the terminal's width, one column short so a line never wraps (a
// wrapped line would throw the redraw off).
func (b *Board) width() int {
	if b.fd != 0 {
		if w, _, err := term.GetSize(b.fd); err == nil && w > 20 {
			return w - 1
		}
	}
	return 79
}

// redraw replaces the last frame with the current one. b.mu must be held.
func (b *Board) redraw() {
	lines := b.render(b.width())
	b.clear()
	fmt.Fprint(b.out, strings.Join(lines, "\n")+"\n")
	b.drawn = len(lines)
}

// render builds the frame's lines at width.
func (b *Board) render(width int) []string {
	elapsed := time.Since(b.start)
	head := "  " + theme.GradientText("ouija "+b.title, b.frame) + styleDim.Render("  ·  ") + styleMeta.Render(fmtDuration(elapsed))
	if b.eng != nil {
		tokens, _, usd := b.eng.Spent()
		if tokens > 0 {
			head += styleDim.Render("  ·  ") + styleMeta.Render(fmtTokens(tokens)+" tok")
		}
		if usd > 0 {
			head += styleDim.Render("  ·  ") + styleMeta.Render(fmt.Sprintf("$%.2f", usd))
		}
	}
	lines := []string{ansi.Truncate(head, width, "…")}

	if len(b.runs) == 0 {
		return append(lines, "  "+styleRun.Render(spinnerFrames[b.frame%len(spinnerFrames)])+" "+styleWaiting.Render("starting…"))
	}
	labelW := 0
	for _, r := range b.runs {
		labelW = max(labelW, ansi.StringWidth(r.label))
	}
	labelW = min(labelW, 28)

	runs := b.runs
	if hidden := len(runs) - maxRows; hidden > 0 {
		// Fold the oldest finished runs; running ones always show.
		var kept []*run
		folded := 0
		for _, r := range runs {
			if r.done && folded < hidden {
				folded++
				continue
			}
			kept = append(kept, r)
		}
		if folded > 0 {
			lines = append(lines, "  "+styleDim.Render(fmt.Sprintf("… %d more done", folded)))
		}
		runs = kept
	}
	for _, r := range runs {
		var mark, dur string
		switch {
		case !r.done:
			mark = styleRun.Render(spinnerFrames[b.frame%len(spinnerFrames)])
			dur = styleWaiting.Render(fmtDuration(time.Since(r.start)))
		case r.err != "":
			mark, dur = styleFail.Render("✗"), styleDim.Render(fmtDuration(r.dur))
		default:
			mark, dur = styleOK.Render("✓"), styleDim.Render(fmtDuration(r.dur))
		}
		label := ansi.Truncate(r.label, labelW, "…")
		row := fmt.Sprintf("  %s %s%s  %s", mark, styleLabel.Render(label), strings.Repeat(" ", labelW-ansi.StringWidth(label)), dur)
		if r.err != "" {
			reason, _, _ := strings.Cut(termsafe.Clean(r.err), "\n")
			row += "  " + styleFail.Render(reason)
		}
		lines = append(lines, ansi.Truncate(row, width, "…"))
	}
	return lines
}

// fmtDuration renders an elapsed time: 850ms, 8.1s, 1m12s.
func fmtDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// fmtTokens renders a token count compactly: 950, 12.3k, 1.2M.
func fmtTokens(n int) string {
	var s string
	switch {
	case n >= 1_000_000:
		s = fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		s = fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
	return strings.Replace(s, ".0", "", 1)
}
