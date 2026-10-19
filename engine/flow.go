package engine

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// Parallel runs fns concurrently with a concurrency limit, preserving order.
// A failing fn does not cancel the others — totems keep the partial results
// (e.g. the task totem's passes) — and the first error is returned. Results
// from successful fns are placed at their original index; indices of failed
// fns hold their zero value. Once ctx is done, fns not yet started are
// skipped and report ctx.Err().
func Parallel[T any](ctx context.Context, limit int, fns []func() (T, error)) ([]T, error) {
	if limit <= 0 {
		limit = 8
	}
	results := make([]T, len(fns))
	var g errgroup.Group
	g.SetLimit(limit)

	for i, fn := range fns {
		g.Go(func() error {
			if err := ctx.Err(); err != nil {
				return err
			}
			r, err := fn()
			if err == nil {
				results[i] = r
			}
			return err
		})
	}
	err := g.Wait()
	return results, err
}
