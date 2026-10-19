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
	date := time.Now().Add(30 * time.Second).UTC().Format(time.RFC1123)
	if got := retryAfter(resp(date)); got <= 0 || got > 31*time.Second {
		t.Fatalf("HTTP-date form = %v, want about 30s", got)
	}
	if got := retryAfter(resp("soon")); got != 0 {
		t.Fatalf("garbage = %v, want 0", got)
	}
	if got := retryAfter(nil); got != 0 {
		t.Fatalf("nil response = %v, want 0", got)
	}
}

// A refused key or plan (401/403) is an AUTH error; other statuses are not.
func TestStatusErrorAuth(t *testing.T) {
	err := statusError("SSE: ", http.StatusForbidden, `{"error":{"type":"FreeTierError"}}`)
	if code, ok := engine.Code(err); !ok || code != engine.ErrCodeAuth {
		t.Fatalf("403 → %v; want AUTH", err)
	}
	if _, ok := engine.Code(statusError("SSE: ", http.StatusBadRequest, "bad")); ok {
		t.Fatal("400 must not be classified as AUTH")
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
