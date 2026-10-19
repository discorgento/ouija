package engine

import (
	"context"
	"errors"
	"io"
	"iter"
	"sync/atomic"
	"testing"
	"time"
)

// The watchdog fires on silence, not on length.
func TestIdleWatchdog(t *testing.T) {
	ctx, idled, stop := idleWatchdog(context.Background(), 80*time.Millisecond)
	defer stop()
	for range 5 { // 5 × 40ms = 200ms in total, more than the timeout
		time.Sleep(40 * time.Millisecond)
		NotifyActivity(ctx)
	}
	if ctx.Err() != nil || idled() {
		t.Fatal("an active stream was cut by its total length")
	}
	time.Sleep(200 * time.Millisecond)
	if ctx.Err() == nil || !idled() {
		t.Fatal("a silent stream was not stopped")
	}
}

// dripProvider streams n text events, one every gap.
type dripProvider struct {
	n     int
	gap   time.Duration
	calls atomic.Int32
}

func (d *dripProvider) Name() string               { return "drip" }
func (d *dripProvider) Capabilities() Capabilities { return Capabilities{} }
func (d *dripProvider) Chat(ctx context.Context, _ ChatRequest) iter.Seq2[StreamEvent, error] {
	d.calls.Add(1)
	return func(yield func(StreamEvent, error) bool) {
		for range d.n {
			select {
			case <-time.After(d.gap):
			case <-ctx.Done():
				yield(StreamEvent{}, ctx.Err())
				return
			}
			if !yield(StreamEvent{Type: EventText, Text: "x"}, nil) {
				return
			}
		}
		yield(StreamEvent{Type: EventDone}, nil)
	}
}

// streamTimeout is the longest silence allowed, not a deadline: an answer
// that keeps streaming past it completes (the old deadline cut the task
// totem's reconcile at exactly 120s on a slow model).
func TestStreamTimeoutIsIdleNotDeadline(t *testing.T) {
	eng := newTestEngine(&dripProvider{n: 6, gap: 300 * time.Millisecond}, t.TempDir()) // 1.8s in all
	eng.Config().StreamTimeout = 1
	res, err := eng.Run(context.Background(), RunRequest{Prompt: "hi"})
	if err != nil {
		t.Fatalf("a stream that never went silent for 1s was cut: %v", err)
	}
	if res.Text != "xxxxxx" {
		t.Fatalf("text = %q", res.Text)
	}

	fastRetries(t)
	quiet := &dripProvider{n: 1, gap: 1500 * time.Millisecond}
	silent := newTestEngine(quiet, t.TempDir())
	silent.Config().StreamTimeout = 1
	_, err = silent.Run(context.Background(), RunRequest{Prompt: "hi"})
	if code, ok := Code(err); !ok || code != ErrCodeStream {
		t.Fatalf("silent provider: err = %v; want a STREAM idle-timeout error", err)
	}
	if quiet.calls.Load() != maxStreamAttempts {
		t.Fatalf("silent provider called %d times; want %d (retried)", quiet.calls.Load(), maxStreamAttempts)
	}
}

// fastRetries shortens the wait between stream retries for one test.
func fastRetries(t *testing.T) {
	t.Helper()
	old := streamRetryDelay
	streamRetryDelay = time.Millisecond
	t.Cleanup(func() { streamRetryDelay = old })
}

// flakyProvider breaks its first `fail` streams mid-way with err, after
// sending some text, then answers.
type flakyProvider struct {
	fail  int32
	err   error
	calls atomic.Int32
}

func (f *flakyProvider) Name() string               { return "flaky" }
func (f *flakyProvider) Capabilities() Capabilities { return Capabilities{} }
func (f *flakyProvider) Chat(context.Context, ChatRequest) iter.Seq2[StreamEvent, error] {
	n := f.calls.Add(1)
	return func(yield func(StreamEvent, error) bool) {
		if !yield(StreamEvent{Type: EventText, Text: "partial "}, nil) {
			return
		}
		if n <= f.fail {
			yield(StreamEvent{}, f.err)
			return
		}
		if yield(StreamEvent{Type: EventText, Text: "answer"}, nil) {
			yield(StreamEvent{Type: EventDone}, nil)
		}
	}
}

// A stream that breaks mid-way is retried: one dropped connection must not
// throw away a whole run (the live task lost its reconcile this way).
func TestDroppedStreamIsRetried(t *testing.T) {
	fastRetries(t)
	p := &flakyProvider{fail: 1, err: ErrStream("SSE: stream broke off", errors.New("http2: response body closed"))}
	res, err := newTestEngine(p, t.TempDir()).Run(context.Background(), RunRequest{Prompt: "hi"})
	if err != nil || res.Text != "partial answer" || p.calls.Load() != 2 {
		t.Fatalf("res=%v err=%v calls=%d; want the retry's answer", res, err, p.calls.Load())
	}
}

// What a retry cannot fix is not retried, and text already on screen is
// never shown twice.
func TestStreamRetryLimits(t *testing.T) {
	fastRetries(t)
	cases := []struct {
		name  string
		err   error
		token bool
	}{
		{"auth", ErrAuth("bad key"), false},
		{"refusal", ErrProvider("anthropic: the model declined the request (cyber)"), false},
		{"http status", ErrProvider("SSE: HTTP 400: bad request"), false},
		{"connect, already retried", ErrNetwork("SSE: connect", errors.New("connection refused")), false},
		{"untagged error", io.ErrUnexpectedEOF, false},
		{"text already shown", ErrStream("cut", io.ErrUnexpectedEOF), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &flakyProvider{fail: 9, err: tc.err}
			eng := newTestEngine(p, t.TempDir())
			if tc.token {
				eng.SetTokenCallback(func(string) {})
			}
			if _, err := eng.Run(context.Background(), RunRequest{Prompt: "hi"}); err == nil {
				t.Fatal("want the error")
			}
			if p.calls.Load() != 1 {
				t.Fatalf("called %d times; want no retry", p.calls.Load())
			}
		})
	}
}
