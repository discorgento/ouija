package models

import (
	"cmp"
	"context"
	"encoding/json"
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
	if cached, ok := readLiveCache(provider); ok {
		return cached
	}
	if lister, ok := p.(engine.ModelLister); ok {
		ctx, cancel := context.WithTimeout(ctx, liveTimeout)
		defer cancel()
		if infos, err := lister.ListModels(ctx); err == nil && len(infos) > 0 {
			live := make([]Model, len(infos))
			for i, info := range infos {
				known, _ := s.ModelInfo(provider, info.ID)
				live[i] = Model{
					ID:            info.ID,
					Label:         cmp.Or(info.Label, known.Label),
					ContextWindow: max(info.ContextWindow, known.ContextWindow),
				}
			}
			writeLiveCache(provider, live)
			return live
		}
	}
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

func liveCachePath(provider string) (string, bool) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}
	return filepath.Join(dir, "ouija", "models", filepath.Base(provider)+".json"), true
}

func readLiveCache(provider string) ([]Model, bool) {
	path, ok := liveCachePath(provider)
	if !ok {
		return nil, false
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	var c liveCache
	if json.Unmarshal(b, &c) != nil || len(c.Models) == 0 || time.Since(c.FetchedAt) > liveTTL {
		return nil, false
	}
	return c.Models, true
}

func writeLiveCache(provider string, list []Model) {
	path, ok := liveCachePath(provider)
	if !ok {
		return
	}
	b, err := json.Marshal(liveCache{FetchedAt: time.Now(), Models: list})
	if err != nil || os.MkdirAll(filepath.Dir(path), 0o700) != nil {
		return
	}
	_ = atomicfile.Write(path, b, 0o600, false) // a cache: best effort
}
