package providers

import (
	"net/http"
	"testing"
	"time"

	"github.com/discorgento/ouija/engine"
)

func TestBackoffHonoursRetryAfterUpToCap(t *testing.T) {
	if got := backoff(1, 5*time.Second); got != 5*time.Second {
		t.Fatalf("backoff with Retry-After 5s = %v, want 5s", got)
	}
	if got := backoff(1, time.Hour); got != maxRetryAfter {
		t.Fatalf("backoff with Retry-After 1h = %v, want the %v cap", got, maxRetryAfter)
	}
	for attempt := 1; attempt <= 5; attempt++ {
		if got := backoff(attempt, 0); got < 0 || got > 8*time.Second {
			t.Fatalf("backoff(%d, 0) = %v, want jitter within [0, 8s]", attempt, got)
		}
	}
}

func TestRetryAfterParsing(t *testing.T) {
	resp := func(v string) *http.Response {
		return &http.Response{Header: http.Header{"Retry-After": []string{v}}}
	}
	if got := retryAfter(resp("7")); got != 7*time.Second {
		t.Fatalf("seconds form = %v, want 7s", got)
	}
	date := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat) // "GMT", as RFC 9110 requires
	if got := retryAfter(resp(date)); got <= 0 || got > 31*time.Second {
		t.Fatalf("HTTP-date form = %v, want about 30s", got)
	}
	if got := retryAfter(resp("soon")); got != 0 {
		t.Fatalf("garbage = %v, want 0", got)
	}
	if got := retryAfter(nil); got != 0 {
		t.Fatalf("nil response = %v, want 0", got)
	}
	for _, v := range []string{"1.5", "30abc", "-5"} {
		if got := retryAfter(resp(v)); got != 0 {
			t.Errorf("Retry-After %q = %v, want 0 (not delay-seconds)", v, got)
		}
	}
	// RFC 850, one of the other HTTP-date formats servers still send.
	rfc850 := time.Now().Add(20 * time.Second).UTC().Format("Monday, 02-Jan-06 15:04:05 GMT")
	if got := retryAfter(resp(rfc850)); got <= 0 || got > 21*time.Second {
		t.Errorf("RFC 850 date = %v, want ~20s", got)
	}
}

// A refused key or plan (401/403) is an AUTH error; other statuses are not.
func TestStatusErrorAuth(t *testing.T) {
	err := statusError("SSE: ", http.StatusForbidden, `{"error":{"type":"FreeTierError"}}`)
	if code, ok := engine.Code(err); !ok || code != engine.ErrCodeAuth {
		t.Fatalf("403 → %v; want AUTH", err)
	}
	if code, _ := engine.Code(statusError("SSE: ", http.StatusBadRequest, "bad")); code != engine.ErrCodeProvider {
		t.Fatalf("400 → %v; want PROVIDER (not AUTH, not retryable)", code)
	}
}

// The client bounds the wait for headers, never the whole stream.
func TestNewClientHasNoTotalTimeout(t *testing.T) {
	c := NewClient(time.Minute)
	if c.HTTP.Timeout != 0 {
		t.Fatalf("http.Client.Timeout = %v; a total timeout cuts long streams", c.HTTP.Timeout)
	}
	tr, ok := c.HTTP.Transport.(*http.Transport)
	if !ok || tr.ResponseHeaderTimeout != time.Minute {
		t.Fatalf("transport = %#v; want ResponseHeaderTimeout = 1m", c.HTTP.Transport)
	}
}
