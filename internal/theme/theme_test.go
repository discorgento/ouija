package theme

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestGradient(t *testing.T) {
	if got := Gradient(0, 3); got != nil {
		t.Fatalf("Gradient(0) = %v, want nil", got)
	}
	g := Gradient(10, 0)
	if len(g) != 10 {
		t.Fatalf("len = %d, want 10", len(g))
	}
	// Shifting by one moves every color one place left: the gradient flows.
	shifted := Gradient(10, 1)
	for i := 0; i < 9; i++ {
		if g[i+1] != shifted[i] {
			t.Fatalf("Gradient(10, 1)[%d] != Gradient(10, 0)[%d]", i, i+1)
		}
	}
	// A full cycle (2n) comes back to the start.
	if back := Gradient(10, 20); back[0] != g[0] {
		t.Fatal("Gradient(10, 20) does not wrap back to Gradient(10, 0)")
	}
}

func TestGradientTextKeepsTheText(t *testing.T) {
	s := "/ __ \\ ouija"
	out := GradientText(s, 4)
	if got := ansi.Strip(out); got != s {
		t.Fatalf("stripped = %q, want %q", got, s)
	}
	if strings.Count(out, "\x1b[") < len(strings.ReplaceAll(s, " ", "")) {
		t.Fatal("not every non-space rune is colored")
	}
}
