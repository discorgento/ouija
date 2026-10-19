package providers

import (
	"fmt"
	"sync"
	"time"
)

// ── States ─────────────────────────────────────────────────────────────

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	CircuitClosed   CircuitState = iota // normal — requests pass
	CircuitOpen                         // failing fast — requests denied
	CircuitHalfOpen                     // testing — lets one probe request through
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// ── CircuitBreaker ─────────────────────────────────────────────────────

// CircuitBreaker prevents cascading failures by failing fast when a provider
// is returning errors. After N consecutive failures, the circuit opens and
// all subsequent requests fail immediately (without making an HTTP call).
// After a timeout, it transitions to half-open and allows one probe request.
// If that succeeds, the circuit closes; if it fails, the circuit re-opens.
//
// Each provider should have its own CircuitBreaker instance.
type CircuitBreaker struct {
	mu          sync.Mutex
	state       CircuitState
	failures    int
	lastFailure time.Time

	// Configuration.
	maxFailures int           // consecutive failures before opening (default: 3)
	openTimeout time.Duration // time before transitioning to half-open (default: 30s)
}

// NewCircuitBreaker creates a circuit breaker with default settings.
func NewCircuitBreaker() *CircuitBreaker {
	return &CircuitBreaker{
		state:       CircuitClosed,
		maxFailures: 3,
		openTimeout: 30 * time.Second,
	}
}

// Allow returns true if the request should proceed.
// When the circuit is half-open, exactly one probe request is allowed through.
func (cb *CircuitBreaker) Allow() bool {
	return cb.AllowAt(time.Now())
}

// AllowAt is like Allow but with an explicit time — used by tests.
func (cb *CircuitBreaker) AllowAt(now time.Time) bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true
	case CircuitHalfOpen:
		return true
	case CircuitOpen:
		if now.Sub(cb.lastFailure) >= cb.openTimeout {
			cb.state = CircuitHalfOpen
			return true
		}
		return false
	default:
		return false
	}
}

// Success reports a successful request. Resets the failure count.
// If half-open, transitions to closed.
func (cb *CircuitBreaker) Success() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	if cb.state == CircuitHalfOpen {
		cb.state = CircuitClosed
	}
}

// Failure reports a failed request. If the failure threshold is reached,
// transitions to open (or re-opens if half-open).
func (cb *CircuitBreaker) Failure() {
	cb.FailureAt(time.Now())
}

// FailureAt is like Failure but with an explicit time — used by tests.
func (cb *CircuitBreaker) FailureAt(now time.Time) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures++
	cb.lastFailure = now
	switch cb.state {
	case CircuitClosed:
		if cb.failures >= cb.maxFailures {
			cb.state = CircuitOpen
		}
	case CircuitHalfOpen:
		cb.state = CircuitOpen
	}
}

// State returns the current circuit state (for diagnostics).
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// ErrCircuitOpen is returned when the circuit is open.
var ErrCircuitOpen = fmt.Errorf("circuit breaker: open (failing fast)")
