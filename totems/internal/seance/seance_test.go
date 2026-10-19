package seance

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/discorgento/ouija/engine"
)

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"My Project":     "my-project",
		"Hello World!":   "hello-world",
		"  spaces  ":     "spaces",
		"already-slug":   "already-slug",
		"UPPERCASE":      "uppercase",
		"special_chars!": "special-chars",
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPasses(t *testing.T) {
	eng := engine.New(engine.EngineConfig{Config: engine.DefaultConfig()})
	eng.SetLogger(slog.New(slog.DiscardHandler))
	job := func(s string, err error) func() (string, error) {
		return func() (string, error) { return s, err }
	}
	nonEmpty := func(s string) bool { return s != "" }
	boom := errors.New("boom")

	got, err := Passes(context.Background(), eng, "passes", []func() (string, error){job("a", nil), job("", boom), job("b", nil)}, nonEmpty)
	if err != nil || len(got) != 2 {
		t.Fatalf("some failed: got %v, %v; want the two that worked and no error", got, err)
	}
	if _, err := Passes(context.Background(), eng, "passes", []func() (string, error){job("", boom), job("", boom)}, nonEmpty); !errors.Is(err, boom) {
		t.Fatalf("all failed: err = %v; want it wrapping the pass error", err)
	}
	if _, err := Passes(context.Background(), eng, "passes", []func() (string, error){job("", nil)}, nonEmpty); err == nil {
		t.Fatal("all empty: want an error")
	}
}
