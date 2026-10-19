package providers

import (
	"bytes"
	"context"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"
)

const maxRetries = 3

// maxRetryAfter caps a server-suggested wait. A Retry-After of an hour would
// otherwise park the run, silently, until its context times out; past the cap
// it is better to fail and let the user decide.
const maxRetryAfter = 60 * time.Second

// retryAfter parses a Retry-After header value (seconds or HTTP-date).
// Returns 0 if the header is absent or unparseable.
func retryAfter(resp *http.Response) time.Duration {
	if resp == nil {
		return 0
	}
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}
	// Delay-seconds, else an HTTP-date (any of the three formats RFC 9110
	// allows). Sscanf would have read "1.5" as 1 and "30abc" as 30.
	if sec, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
		return max(time.Duration(sec)*time.Second, 0)
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0)
	}
	return 0
}

// backoff returns a jittered exponential backoff for the given attempt.
// Base delay is 1s; each attempt doubles (1s, 2s, 4s).
// If retryAfter > 0, it takes precedence (honours server-suggested wait),
// capped at maxRetryAfter (the caller does not retry past the cap at all).
func backoff(attempt int, retryAfterDur time.Duration) time.Duration {
	if retryAfterDur > 0 {
		return min(retryAfterDur, maxRetryAfter)
	}
	// Exponential: 1s, 2s, 4s, capped at attempt 3 (max 4s).
	base := time.Duration(1<<uint(min(attempt-1, 3))) * time.Second
	// Full jitter: random duration between 0 and base.
	return time.Duration(rand.Int64N(int64(base)))
}

// isRetryableStatus returns true for status codes that should be retried.
func isRetryableStatus(code int) bool {
	return code == http.StatusTooManyRequests || code >= 500
}

// retryableRoundTrip performs up to maxRetries attempts with backoff,
// retrying on transport errors and retryable status codes.
// The optional circuit breaker (cb) is checked before each logical request:
//   - OPEN → fails immediately without HTTP call
//   - CLOSED/HALF-OPEN → proceeds, then reports success/failure
//   - nil → no circuit breaking applied
//
// Returns the HTTP response (body NOT read — caller must close resp.Body).
func retryableRoundTrip(ctx context.Context, client *http.Client, method, url string, headers map[string]string, body []byte, cb *CircuitBreaker) (*http.Response, error) {
	if cb != nil && !cb.Allow() {
		return nil, ErrCircuitOpen
	}

	for attempt := 1; ; attempt++ { // every iteration continues or returns
		req, err := http.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}

		resp, err := client.Do(req)
		if err != nil {
			// Transport error (connection refused, DNS, TLS, etc.).
			if attempt < maxRetries && sleep(ctx, backoff(attempt, 0)) {
				continue
			}
			// A cancelled call (Esc, the idle watchdog) says nothing about
			// the provider: counting it opened the circuit on a healthy one.
			if cb != nil && ctx.Err() != nil {
				cb.Abandon()
			} else if cb != nil {
				cb.Failure()
			}
			return nil, err
		}

		// A server asking for a longer wait than we would sleep is not
		// retried: sleeping the cap and failing anyway only adds minutes.
		if ra := retryAfter(resp); isRetryableStatus(resp.StatusCode) && attempt < maxRetries && ra <= maxRetryAfter {
			if sleep(ctx, backoff(attempt, ra)) {
				resp.Body.Close()
				continue
			}
			if ctx.Err() != nil {
				resp.Body.Close()
				if cb != nil {
					cb.Abandon()
				}
				return nil, ctx.Err()
			}
		}

		// Non-retryable response (success or client error like 400).
		if cb != nil {
			if isRetryableStatus(resp.StatusCode) {
				cb.Failure()
			} else {
				cb.Success()
			}
		}
		return resp, nil
	}
}

// sleep waits for the given duration, honoring context cancellation.
// Returns false if ctx ended before the duration elapsed.
// Uses time.NewTimer + defer Stop to avoid timer leaks on context cancellation.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}
