package providers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// A redirect to another host must not carry the API key there: Go strips
// Authorization across hosts, not x-api-key. The 3xx comes back instead.
func TestRedirectToAnotherHostIsRefused(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("x-api-key")
	}))
	defer other.Close()
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/away":
			http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
		case "/here":
			http.Redirect(w, r, "/ok", http.StatusTemporaryRedirect)
		default:
			w.Write([]byte(`{}`))
		}
	}))
	defer api.Close()

	c := NewClient(5 * time.Second)
	_, code, err := c.GetJSON(context.Background(), api.URL+"/away", map[string]string{"x-api-key": "sk-secret"})
	if err != nil || code != http.StatusTemporaryRedirect || leaked != "" {
		t.Fatalf("cross-host redirect: code=%d err=%v leaked=%q", code, err, leaked)
	}
	if data, code, err := c.GetJSON(context.Background(), api.URL+"/here", nil); err != nil || code != 200 || string(data) != "{}" {
		t.Fatalf("same-host redirect not followed: code=%d err=%v", code, err)
	}
}

// A response past the limits is an error, not unbounded memory.
func TestResponseSizeLimits(t *testing.T) {
	big := strings.Repeat("x", maxBodyBytes+1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/body":
			w.Write([]byte(big))
		case "/line": // one line that never ends
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: " + strings.Repeat("x", maxSSELineBytes+1)))
		case "/event": // many lines, one event
			w.Header().Set("Content-Type", "text/event-stream")
			line := "data: " + strings.Repeat("x", 1<<20) + "\n"
			for range maxSSEEvent>>20 + 1 {
				w.Write([]byte(line))
			}
			w.Write([]byte("\n"))
		case "/small":
			w.Write([]byte("data: {\"a\":1}\n\n"))
		}
	}))
	defer srv.Close()
	c := NewClient(5 * time.Second)
	ctx := context.Background()

	if _, _, err := c.GetJSON(ctx, srv.URL+"/body", nil); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("oversized body: err=%v", err)
	}
	for _, path := range []string{"/line", "/event"} {
		err := c.PostSSE(ctx, srv.URL+path, nil, nil, func(string, []byte) error { return nil })
		if err == nil || !strings.Contains(err.Error(), "size limit") {
			t.Errorf("%s: err=%v, want the size limit", path, err)
		}
	}
	var got string
	if err := c.PostSSE(ctx, srv.URL+"/small", nil, nil, func(_ string, d []byte) error { got = string(d); return nil }); err != nil || got != `{"a":1}` {
		t.Errorf("small stream: got %q err=%v", got, err)
	}
}
