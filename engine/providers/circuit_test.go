package providers

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestCircuitBreakerClosedByDefault(t *testing.T) {
	cb := NewCircuitBreaker()
	if cb.State() != CircuitClosed {
		t.Fatalf("expected closed, got %v", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("closed circuit should allow requests")
	}
}

func TestCircuitBreakerOpensAfterFailures(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.maxFailures = 2

	// Two failures → open.
	cb.Failure()
	if cb.State() != CircuitClosed {
		t.Fatalf("expected closed after 1 failure, got %v", cb.State())
	}
	cb.Failure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected open after 2 failures, got %v", cb.State())
	}

	if cb.Allow() {
		t.Fatal("open circuit should deny requests")
	}
}

func TestCircuitBreakerHalfOpenAfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.maxFailures = 1
	cb.openTimeout = time.Millisecond

	now := time.Now()
	cb.FailureAt(now)
	if cb.State() != CircuitOpen {
		t.Fatalf("expected open, got %v", cb.State())
	}

	// Still open before timeout.
	if cb.AllowAt(now) {
		t.Fatal("open circuit should deny before timeout")
	}

	// After timeout, should half-open.
	if !cb.AllowAt(now.Add(2 * time.Millisecond)) {
		t.Fatal("circuit should half-open after timeout")
	}
	if cb.State() != CircuitHalfOpen {
		t.Fatalf("expected half-open, got %v", cb.State())
	}
}

func TestCircuitBreakerHalfOpenSuccessCloses(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.maxFailures = 1
	cb.openTimeout = time.Millisecond

	now := time.Now()
	cb.FailureAt(now)
	cb.AllowAt(now.Add(2 * time.Millisecond)) // transitions to half-open

	cb.Success()
	if cb.State() != CircuitClosed {
		t.Fatalf("expected closed after half-open success, got %v", cb.State())
	}
}

func TestCircuitBreakerHalfOpenFailureReopens(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.maxFailures = 1
	cb.openTimeout = time.Millisecond

	now := time.Now()
	cb.FailureAt(now)
	cb.AllowAt(now.Add(2 * time.Millisecond)) // transitions to half-open

	cb.Failure()
	if cb.State() != CircuitOpen {
		t.Fatalf("expected open after half-open failure, got %v", cb.State())
	}
}

func TestCircuitBreakerSuccessResetsFailures(t *testing.T) {
	cb := NewCircuitBreaker()
	cb.maxFailures = 3

	cb.Failure()
	cb.Failure()
	cb.Success()
	cb.Failure()

	if cb.State() != CircuitClosed {
		t.Fatalf("expected closed, got %v (failures=%d)", cb.State(), cb.failures)
	}
}

func TestCircuitBreakerStateString(t *testing.T) {
	tests := []struct {
		s    CircuitState
		want string
	}{
		{CircuitClosed, "closed"},
		{CircuitOpen, "open"},
		{CircuitHalfOpen, "half-open"},
		{CircuitState(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.s.String(); got != tt.want {
			t.Errorf("CircuitState(%d).String() = %q, want %q", tt.s, got, tt.want)
		}
	}
}

// Half-open lets one probe through at a time; an abandoned (cancelled) probe
// frees the slot without counting a failure.
func TestCircuitBreakerHalfOpenSingleProbe(t *testing.T) {
	cb := NewCircuitBreaker()
	t0 := time.Now()
	for range cb.maxFailures {
		cb.FailureAt(t0)
	}
	later := t0.Add(cb.openTimeout)
	if !cb.AllowAt(later) {
		t.Fatal("no probe after the open timeout")
	}
	if cb.AllowAt(later) {
		t.Fatal("a second request passed while the probe is in flight")
	}
	cb.Abandon()
	if cb.State() != CircuitHalfOpen || !cb.AllowAt(later) {
		t.Fatal("an abandoned probe must free the slot without reopening")
	}
}

// A cancelled request is not a provider failure: it used to open the circuit.
func TestRoundTripCancelIsNotAFailure(t *testing.T) {
	cb := NewCircuitBreaker()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, r.Context().Err()
	})}
	for range cb.maxFailures + 1 {
		_, _ = retryableRoundTrip(ctx, client, "POST", "http://x", nil, nil, cb)
	}
	if cb.State() != CircuitClosed {
		t.Fatalf("circuit %v after cancelled requests, want closed", cb.State())
	}
}

// A Retry-After beyond the cap is answered at once, not slept on and retried.
func TestRoundTripRetryAfterBeyondCap(t *testing.T) {
	calls := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusTooManyRequests, Body: http.NoBody,
			Header: http.Header{"Retry-After": []string{"3600"}}, Request: r}, nil
	})}
	resp, err := retryableRoundTrip(context.Background(), client, "POST", "http://x", nil, nil, nil)
	if err != nil || resp.StatusCode != http.StatusTooManyRequests || calls != 1 {
		t.Fatalf("calls=%d err=%v; want one call returning the 429", calls, err)
	}
}
