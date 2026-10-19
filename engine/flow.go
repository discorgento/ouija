package engine

import (
	"context"

	"golang.org/x/sync/errgroup"
)

// Parallel runs fns concurrently with a concurrency limit, preserving order.
// It uses errgroup for structured concurrency: the first failing fn cancels
// the rest, and its error is returned. Results from successful fns are placed
// at their original index; indices of failed fns hold their zero value.
func Parallel[T any](ctx context.Context, limit int, fns []func() (T, error)) ([]T, error) {
	if limit <= 0 {
		limit = 8
	}
	results := make([]T, len(fns))
	g, ctx := errgroup.WithContext(ctx)
	g.SetLimit(limit)

	for i, fn := range fns {
		i, fn := i, fn
		g.Go(func() error {
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
