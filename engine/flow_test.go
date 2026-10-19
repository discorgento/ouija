package engine

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
)

// A failing fn must not cancel the rest: totems keep partial results.
func TestParallelKeepsPartialResults(t *testing.T) {
	boom := errors.New("boom")
	fns := []func() (int, error){
		func() (int, error) { return 1, nil },
		func() (int, error) { return 0, boom },
		func() (int, error) { return 3, nil },
	}
	got, err := Parallel(context.Background(), 1, fns)
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
	if got[0] != 1 || got[1] != 0 || got[2] != 3 {
		t.Fatalf("results = %v, want [1 0 3]", got)
	}
}

// After ctx is cancelled, fns that have not started are skipped.
func TestParallelSkipsAfterCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var ran atomic.Int32
	fns := []func() (int, error){
		func() (int, error) { ran.Add(1); return 1, nil },
		func() (int, error) { ran.Add(1); return 2, nil },
	}
	_, err := Parallel(ctx, 1, fns)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if ran.Load() != 0 {
		t.Fatalf("%d fns ran after cancellation", ran.Load())
	}
}
