// Package seance holds the plumbing the multi-pass totems share: running the
// independent passes of a séance and naming its output files.
package seance

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/discorgento/ouija/engine"
)

// Passes runs the independent passes concurrently and keeps the ones that
// produced something (ok). It fails only when none did — a bad key or a spent
// budget fails them all; a few failures out of many are logged and dropped,
// since the reconcile step works with whatever passes agree. what names the
// passes in messages ("analysis passes", "fact passes").
func Passes[T any](ctx context.Context, eng *engine.Engine, what string, jobs []func() (T, error), ok func(T) bool) ([]T, error) {
	results, err := engine.Parallel(ctx, 8, jobs)
	var kept []T
	for _, r := range results {
		if ok(r) {
			kept = append(kept, r)
		}
	}
	switch {
	case len(kept) == 0 && err != nil:
		return nil, fmt.Errorf("all %d %s failed: %w", len(jobs), what, err)
	case len(kept) == 0:
		return nil, fmt.Errorf("all %d %s failed (check provider/key/budget)", len(jobs), what)
	case err != nil:
		eng.Logger().Warn(what+" failed", "failed", len(jobs)-len(kept), "total", len(jobs), "err", err)
	}
	return kept, nil
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns s into a lowercase, dash-separated file-name stem.
func Slugify(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}
