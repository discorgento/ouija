package engine

import (
	"context"
	"fmt"
	"iter"
	"sync/atomic"
	"time"
)

// activityKey carries the stream watchdog's reset through the context.
type activityKey struct{}

// NotifyActivity tells the engine the provider is still sending: it resets the
// idle timeout (Config.StreamTimeout) of the call ctx belongs to. Providers
// call it for every chunk they read — keep-alives and hidden reasoning
// deltas included, which yield no StreamEvent. A no-op outside a call.
func NotifyActivity(ctx context.Context) {
	if fn, ok := ctx.Value(activityKey{}).(func()); ok {
		fn()
	}
}

// idleWatchdog bounds a streaming call by silence, not by length: the call is
// cancelled only after timeout passes with no activity. It returns the call's
// context, a function reporting whether the watchdog fired, and a stop
// function to release the timer.
func idleWatchdog(ctx context.Context, timeout time.Duration) (context.Context, func() bool, func()) {
	ctx, cancel := context.WithCancel(ctx)
	if timeout <= 0 {
		return ctx, func() bool { return false }, cancel
	}
	var fired atomic.Bool
	timer := time.AfterFunc(timeout, func() {
		fired.Store(true)
		cancel()
	})
	ctx = context.WithValue(ctx, activityKey{}, func() { timer.Reset(timeout) })
	return ctx, fired.Load, func() {
		timer.Stop()
		cancel()
	}
}

// withActivity counts every event of seq as activity, for providers that do
// not call NotifyActivity themselves.
func withActivity(ctx context.Context, seq iter.Seq2[StreamEvent, error]) iter.Seq2[StreamEvent, error] {
	return func(yield func(StreamEvent, error) bool) {
		for ev, err := range seq {
			NotifyActivity(ctx)
			if !yield(ev, err) {
				return
			}
		}
	}
}

// errStreamIdle is the error of a call the watchdog stopped.
func errStreamIdle(timeout time.Duration) error {
	return ErrNetwork(fmt.Sprintf("the provider sent nothing for %s — raise streamTimeout if the model "+
		"is slow to start answering", timeout))
}
