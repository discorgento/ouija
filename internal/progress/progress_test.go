package progress

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/enginetest"
)

// The board lists each run with its state, and a failure with its reason.
func TestBoardRendersRuns(t *testing.T) {
	var buf bytes.Buffer
	b := New(&buf, "task")
	b.start = time.Now()
	b.onRun(engine.RunEvent{Label: "scout"})
	b.onRun(engine.RunEvent{Label: "scout", Done: true, Duration: 8100 * time.Millisecond})
	b.onRun(engine.RunEvent{Label: "p1:risks"})
	b.onRun(engine.RunEvent{Label: "p2:risks"})
	b.onRun(engine.RunEvent{Label: "p2:risks", Done: true, Duration: time.Second, Err: errors.New("budget exceeded")})

	out := ansi.Strip(strings.Join(b.render(80), "\n"))
	for _, want := range []string{"ouija task", "✓ scout", "8.1s", "p1:risks", "✗ p2:risks", "budget exceeded"} {
		if !strings.Contains(out, want) {
			t.Errorf("board lacks %q:\n%s", want, out)
		}
	}
	for i, l := range b.render(30) {
		if w := ansi.StringWidth(l); w > 30 {
			t.Errorf("line %d is %d wide at width 30", i+1, w)
		}
	}
}

// Past maxRows the oldest finished runs fold into one line; running ones stay.
func TestBoardFoldsOldRuns(t *testing.T) {
	b := New(&bytes.Buffer{}, "task")
	b.start = time.Now()
	for i := range maxRows + 5 {
		label := "p" + string(rune('a'+i))
		b.onRun(engine.RunEvent{Label: label})
		b.onRun(engine.RunEvent{Label: label, Done: true, Duration: time.Second})
	}
	b.onRun(engine.RunEvent{Label: "reconcile"})
	out := ansi.Strip(strings.Join(b.render(80), "\n"))
	if !strings.Contains(out, "… 6 more done") || !strings.Contains(out, "reconcile") {
		t.Fatalf("board:\n%s", out)
	}
}

// Start hooks the engine's runs and logs; Stop leaves the final frame, the
// cursor shown, and the engine as it was.
func TestBoardStartStop(t *testing.T) {
	var buf bytes.Buffer
	eng := enginetest.NewEngine(&enginetest.Provider{Respond: func(engine.ChatRequest) enginetest.Reply {
		return enginetest.Reply{Text: "ok"}
	}}, t.TempDir())
	b := New(&buf, "task")
	// A buffer is no terminal, so its writer would strip every escape: act as
	// one that takes them all.
	b.out = &colorprofile.Writer{Forward: &buf, Profile: colorprofile.TrueColor}
	b.Start(eng)
	if _, err := eng.Run(t.Context(), engine.RunRequest{Label: "scout", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	eng.Logger().Warn("pass failed")
	b.Stop()
	out := ansi.Strip(buf.String())
	if !strings.Contains(out, "✓ scout") || !strings.Contains(out, "pass failed") {
		t.Fatalf("output:\n%s", out)
	}
	if !strings.HasSuffix(buf.String(), "\x1b[?25h\n") {
		t.Fatal("the cursor is not shown again at the end")
	}
	// The callback is gone: a later run does not reach the stopped board.
	if _, err := eng.Run(t.Context(), engine.RunRequest{Label: "after", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ansi.Strip(buf.String()), "after") {
		t.Fatal("a run after Stop reached the board")
	}
}

func TestEnabledOnlyOnATerminal(t *testing.T) {
	if Enabled(&bytes.Buffer{}) {
		t.Fatal("a buffer is not a terminal")
	}
}
