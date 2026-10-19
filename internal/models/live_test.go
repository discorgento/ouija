package models

import (
	"context"
	"errors"
	"iter"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/discorgento/ouija/engine"
)

// listerProvider is a provider whose model list is canned (or fails).
type listerProvider struct {
	models []engine.ModelInfo
	err    error
	calls  int
}

func (l *listerProvider) Name() string                      { return "fake" }
func (l *listerProvider) Capabilities() engine.Capabilities { return engine.Capabilities{} }
func (l *listerProvider) Chat(context.Context, engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
	return func(func(engine.StreamEvent, error) bool) {}
}
func (l *listerProvider) ListModels(context.Context) ([]engine.ModelInfo, error) {
	l.calls++
	return l.models, l.err
}

func liveTestStore() *Store {
	return &Store{data: &ModelsFile{Providers: map[string]ProviderDef{
		"anthropic": {Models: map[string]ModelDef{"claude-old": {Label: "Old", ContextWindow: 200_000}}},
	}}}
}

// The provider's own list wins, the catalog fills in what it leaves out, and
// the answer is reused from the cache for a day.
func TestListUsesTheProvidersOwnList(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	s := liveTestStore()
	p := &listerProvider{models: []engine.ModelInfo{{ID: "claude-new", Label: "New"}, {ID: "claude-old"}}}

	got := s.List(context.Background(), "anthropic", p)
	if len(got) != 2 || got[0].ID != "claude-new" || got[1].Label != "Old" || got[1].ContextWindow != 200_000 {
		t.Fatalf("list = %+v", got)
	}
	again := s.List(context.Background(), "anthropic", &listerProvider{err: errors.New("offline")})
	if len(again) != 2 || again[0].ID != "claude-new" {
		t.Fatalf("the cached list was not reused: %+v", again)
	}
}

// Without a usable list — no lister, an error, an expired cache with the API
// down — the built-in suggestions are the fallback.
func TestListFallsBackToTheCatalog(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cache)
	s := liveTestStore()

	if got := s.List(context.Background(), "anthropic", nil); len(got) != 1 || got[0].ID != "claude-old" {
		t.Fatalf("no provider: %+v", got)
	}
	if got := s.List(context.Background(), "anthropic", &listerProvider{err: errors.New("401")}); len(got) != 1 || got[0].ID != "claude-old" {
		t.Fatalf("failing API: %+v", got)
	}

	// Freshness comes from fetchedAt inside the cache file.
	writeLiveCache("anthropic", []Model{{ID: "claude-stale"}})
	writeStaleCache(t, filepath.Join(cache, "ouija", "models", "anthropic.json"))
	fresh := &listerProvider{models: []engine.ModelInfo{{ID: "claude-fresh"}}}
	if got := s.List(context.Background(), "anthropic", fresh); len(got) != 1 || got[0].ID != "claude-fresh" || fresh.calls != 1 {
		t.Fatalf("an expired cache must be refetched: %+v (calls %d)", got, fresh.calls)
	}
}

func writeStaleCache(t *testing.T, path string) {
	t.Helper()
	stale := `{"fetchedAt":"` + time.Now().Add(-2*liveTTL).Format(time.RFC3339) + `","models":[{"id":"claude-stale"}]}`
	if err := os.WriteFile(path, []byte(stale), 0o600); err != nil {
		t.Fatal(err)
	}
}
