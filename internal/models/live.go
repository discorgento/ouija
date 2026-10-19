package models

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/atomicfile"
)

// Model is one model a provider offers.
type Model struct {
	ID            string `json:"id"`
	Label         string `json:"label,omitempty"`
	ContextWindow int    `json:"contextWindow,omitempty"`
}

const (
	// liveTTL is how long a list fetched from a provider's API is reused.
	liveTTL = 24 * time.Hour
	// liveTimeout bounds the fetch, so a picker never hangs on the network.
	liveTimeout = 8 * time.Second
)

// List returns the models of provider: the provider's own list, from its API
// through p when p is an engine.ModelLister, reused for a day from the user
// cache dir; the built-in suggestions when that list can't be had (no network,
// no key, a provider without a list endpoint). The built-in catalog fills in a
// label or context size the API leaves out.
func (s *Store) List(ctx context.Context, provider string, p engine.Provider) []Model {
	list, _ := s.Fetch(ctx, provider, p)
	return list
}

// errNoLister is Fetch's error for a provider (or nil p) with no list endpoint.
var errNoLister = errors.New("the provider has no model list to fetch")

// Fetch is List that also says why it fell back to the built-in catalog: the
// provider's error (a bad key, no network) or errNoLister. A caller can show
// it, and should not keep a fallback as if it were the provider's list.
func (s *Store) Fetch(ctx context.Context, provider string, p engine.Provider) ([]Model, error) {
	lister, ok := p.(engine.ModelLister)
	if !ok {
		return s.Catalog(provider), errNoLister
	}
	// The cache is keyed by what the list depends on (endpoint, key): a
	// list fetched through one gateway or key is never shown for another.
	key := lister.ListCacheKey()
	if key != "" {
		if cached, ok := readLiveCache(provider, key); ok {
			return cached, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, liveTimeout)
	defer cancel()
	infos, err := lister.ListModels(ctx)
	if err == nil && len(infos) == 0 {
		err = errors.New("the provider listed no models")
	}
	if err == nil {
		live := make([]Model, len(infos))
		for i, info := range infos {
			known, _ := s.ModelInfo(provider, info.ID)
			live[i] = Model{
				ID:            info.ID,
				Label:         cmp.Or(info.Label, known.Label),
				ContextWindow: max(info.ContextWindow, known.ContextWindow),
			}
		}
		if key != "" {
			writeLiveCache(provider, key, live)
		}
		return live, nil
	}
	return s.Catalog(provider), err
}

// Catalog is the built-in model suggestions for provider — no network.
func (s *Store) Catalog(provider string) []Model {
	var out []Model
	for _, id := range s.Models(provider) {
		info, _ := s.ModelInfo(provider, id)
		out = append(out, Model{ID: id, Label: info.Label, ContextWindow: info.ContextWindow})
	}
	return out
}

// IDs returns the model IDs of a list.
func IDs(list []Model) []string {
	out := make([]string, len(list))
	for i, m := range list {
		out[i] = m.ID
	}
	return out
}

type liveCache struct {
	FetchedAt time.Time `json:"fetchedAt"`
	Models    []Model   `json:"models"`
}

func liveCachePath(provider, key string) (string, bool) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, "ouija", "models", filepath.Base(provider)+"-"+filepath.Base(key)+".json"), true
}

func readLiveCache(provider, key string) ([]Model, bool) {
	path, ok := liveCachePath(provider, key)
	if !ok {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var c liveCache
	if json.Unmarshal(b, &c) != nil || len(c.Models) == 0 {
		return nil, false
	}
	// A timestamp in the future (a clock that was ahead) counts as stale:
	// time.Since alone kept such a cache fresh until that date.
	if age := time.Since(c.FetchedAt); age > liveTTL || age < -time.Minute {
		return nil, false
	}
	return c.Models, true
}

func writeLiveCache(provider, key string, list []Model) {
	path, ok := liveCachePath(provider, key)
	if !ok {
		return
	}
	b, err := json.Marshal(liveCache{FetchedAt: time.Now(), Models: list})
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = atomicfile.Write(path, b, 0o600, false) // a cache: best effort
}
