package seance

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/enginetest"
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

// Once most of the budget is spent, passes that have not started are
// skipped, keeping the rest for the final step.
func TestPassesKeepBudgetForTheFinalStep(t *testing.T) {
	cfg := engine.DefaultConfig()
	cfg.Budget = engine.Budget{MaxTokens: 1000, MaxSteps: 100}
	p := &enginetest.Provider{Respond: func(engine.ChatRequest) enginetest.Reply { return enginetest.Reply{Text: "x"} }}
	eng := engine.New(engine.EngineConfig{Provider: p, Config: cfg})
	eng.SetLogger(slog.New(slog.DiscardHandler))
	// Spend past the 80% mark: each call costs 15 tokens (enginetest usage).
	for range 54 {
		if _, err := eng.Run(context.Background(), engine.RunRequest{Prompt: "spend"}); err != nil {
			t.Fatal(err)
		}
	}
	ran := 0
	job := func() (string, error) { ran++; return "ok", nil }
	_, err := Passes(context.Background(), eng, "passes", []func() (string, error){job, job}, func(s string) bool { return s != "" })
	if err == nil || ran != 0 || !strings.Contains(err.Error(), "kept for the final step") {
		t.Fatalf("ran=%d err=%v; want both passes skipped to keep the budget", ran, err)
	}
}
