package theme

import (
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/internal/ouijaconfig"
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

// A theme replaces the colors; an unknown name keeps them.
func TestApplyTheme(t *testing.T) {
	saved := []any{Pink, Green, Current, Rainbow, append([]color.Color(nil), gradientStops...)}
	t.Cleanup(func() {
		Pink, Green, Current, Rainbow = saved[0].(color.Color), saved[1].(color.Color), saved[2].(string), saved[3].([]color.Color)
		gradientStops = saved[4].([]color.Color)
	})
	apply("nope")
	if Current != "ouija" {
		t.Fatalf("an unknown theme changed Current to %q", Current)
	}
	apply("Dracula")
	if Current != "dracula" || Hex(Pink) != "#ff79c6" || Hex(Green) != "#50fa7b" || Rainbow[0] != Pink {
		t.Fatalf("dracula: current=%q pink=%s green=%s", Current, Hex(Pink), Hex(Green))
	}
	if names := Names(); names[0] != "ouija" || len(names) != 4 {
		t.Fatalf("names = %v", names)
	}
}

// Save writes the theme to the global config; an unknown one is refused.
func TestSaveTheme(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("APPDATA", t.TempDir())
	if err := Save("nope"); err == nil {
		t.Fatal("saved an unknown theme")
	}
	if err := Save("mono"); err != nil {
		t.Fatal(err)
	}
	cfg, err := ouijaconfig.Load()
	if err != nil || cfg == nil || cfg.Theme != "mono" {
		t.Fatalf("config theme = %+v, %v", cfg, err)
	}
}
