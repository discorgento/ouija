// Package seance holds the plumbing the multi-pass totems share: running the
// independent passes of a séance and naming its output files.
package seance

import (
	"context"
	"errors"
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
	// A pass that has not started does not start once the budget is mostly
	// spent: the reconcile/compose step after the passes needs room, and
	// without it the run spent its budget and then failed with nothing saved.
	guarded := make([]func() (T, error), len(jobs))
	for i, job := range jobs {
		guarded[i] = func() (T, error) {
			if reason := budgetReserved(eng); reason != "" {
				var zero T
				return zero, errors.New(reason)
			}
			return job()
		}
	}
	results, err := engine.Parallel(ctx, 8, guarded)
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

// reserveShare is the part of the budget kept for the step after the passes.
const reserveShare = 0.2

// budgetReserved says why a new pass must not start ("" when it may): the
// tokens or steps spent already reach the share kept for the final step.
func budgetReserved(eng *engine.Engine) string {
	b := eng.Config().Budget
	tokens, steps, _ := eng.Spent()
	switch {
	case b.MaxTokens > 0 && float64(tokens) >= float64(b.MaxTokens)*(1-reserveShare):
		return fmt.Sprintf("pass skipped: %d of %d tokens spent, the rest is kept for the final step", tokens, b.MaxTokens)
	case b.MaxSteps > 0 && float64(steps) >= float64(b.MaxSteps)*(1-reserveShare):
		return fmt.Sprintf("pass skipped: %d of %d steps spent, the rest is kept for the final step", steps, b.MaxSteps)
	}
	return ""
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// Slugify turns s into a lowercase, dash-separated file-name stem.
func Slugify(s string) string {
	return strings.Trim(nonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), "-"), "-")
}
