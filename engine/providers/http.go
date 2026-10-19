package providers

import (
	"bytes"
	"context"
	"fmt"
	"math/rand/v2"
	"net/http"
	"time"
)

const maxRetries = 3

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
	// Try seconds first.
	var sec int
	if _, err := fmt.Sscanf(v, "%d", &sec); err == nil {
		return time.Duration(sec) * time.Second
	}
	// Try HTTP-date.
	if t, err := time.Parse(time.RFC1123, v); err == nil {
		return time.Until(t)
	}
	return 0
}

// backoff returns a jittered exponential backoff for the given attempt.
// Base delay is 1s; each attempt doubles (1s, 2s, 4s).
// If retryAfter > 0, it takes precedence (honours server-suggested wait).
func backoff(attempt int, retryAfterDur time.Duration) time.Duration {
	if retryAfterDur > 0 {
		return retryAfterDur
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

	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
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
			lastErr = err
			if attempt < maxRetries && sleep(ctx, backoff(attempt, 0)) {
				continue
			}
			if cb != nil {
				cb.Failure()
			}
			return nil, err
		}

		if isRetryableStatus(resp.StatusCode) && attempt < maxRetries {
			d := backoff(attempt, retryAfter(resp))
			if sleep(ctx, d) {
				resp.Body.Close()
				continue
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
	if cb != nil {
		cb.Failure()
	}
	return nil, lastErr
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
