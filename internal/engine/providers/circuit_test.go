package providers

import (
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
