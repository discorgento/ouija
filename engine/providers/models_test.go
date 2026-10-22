package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
)

// fakeHTTP answers each GET with the body routes[path+query], recording URLs.
func fakeHTTP(t *testing.T, c *Client, routes map[string]string) *[]string {
	t.Helper()
	var seen []string
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		key := r.URL.Path
		if r.URL.RawQuery != "" {
			key += "?" + r.URL.RawQuery
		}
		seen = append(seen, key)
		body, ok := routes[key]
		code := http.StatusOK
		if !ok {
			code, body = http.StatusNotFound, `{"error":"no route `+key+`"}`
		}
		return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})
	return &seen
}

// Anthropic lists every page of /v1/models, with display names and context.
func TestAnthropicListModelsPaginates(t *testing.T) {
	a := NewAnthropic("k")
	seen := fakeHTTP(t, a.client, map[string]string{
		"/v1/models?limit=1000":                          `{"data":[{"id":"claude-opus-5-5","display_name":"Claude Opus 5.5","max_input_tokens":1000000}],"has_more":true,"last_id":"claude-opus-5-5"}`,
		"/v1/models?limit=1000&after_id=claude-opus-5-5": `{"data":[{"id":"claude-haiku-5-5","display_name":"Claude Haiku 5.5"}],"has_more":false}`,
	})
	got, err := a.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels: %v (requests %v)", err, *seen)
	}
	if len(got) != 2 || got[0].ID != "claude-opus-5-5" || got[0].Label != "Claude Opus 5.5" ||
		got[0].ContextWindow != 1_000_000 || got[1].ID != "claude-haiku-5-5" {
		t.Fatalf("models = %+v", got)
	}
	if _, err := NewAnthropic("").ListModels(context.Background()); err == nil {
		t.Fatal("listing without a key must fail")
	}
}

// OpenAI-compatible providers list GET {base}/models; OpenRouter's name and
// context length come through.
func TestOpenAICompatListModels(t *testing.T) {
	o := NewOpenRouter("k")
	fakeHTTP(t, o.client, map[string]string{
		"/api/v1/models": `{"data":[{"id":"qwen/qwen3-coder:free","name":"Qwen3 Coder (free)","context_length":262144},{"id":""}]}`,
	})
	got, err := o.ListModels(context.Background())
	if err != nil || len(got) != 1 || got[0].Label != "Qwen3 Coder (free)" || got[0].ContextWindow != 262144 {
		t.Fatalf("models = %+v, %v", got, err)
	}

	refused := NewOpenAI("bad")
	fakeHTTP(t, refused.client, nil) // every path 404s
	if _, err := refused.ListModels(context.Background()); err == nil {
		t.Fatal("an error status must fail the listing")
	}
	var lister engine.ModelLister = refused
	_ = lister
}

// A models endpoint that always has more ends the listing: with a cursor that
// does not move, at once; with one that does, after maxModelPages pages.
func TestAnthropicListModelsStops(t *testing.T) {
	for name, next := range map[string]func(page int) string{
		"stuck cursor":   func(int) string { return "same" },
		"endless cursor": func(page int) string { return fmt.Sprintf("m%d", page) },
	} {
		a := NewAnthropic("k")
		pages := 0
		a.client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			pages++
			if pages > maxModelPages+5 {
				t.Fatalf("%s: still listing after %d pages", name, pages)
			}
			body := fmt.Sprintf(`{"data":[{"id":"m"}],"has_more":true,"last_id":%q}`, next(pages))
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
		})
		if _, err := a.ListModels(context.Background()); err == nil {
			t.Errorf("%s: ListModels returned no error", name)
		}
	}
}
