package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// captureAnthropicBody sends req through the Anthropic provider and returns
// the JSON body it would post (the fake transport answers 400, so no network).
func captureAnthropicBody(t *testing.T, req engine.ChatRequest) map[string]any {
	t.Helper()
	a := NewAnthropic("test-key")
	var body map[string]any
	a.client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(b, &body); err != nil {
			t.Fatalf("request body is not JSON: %v", err)
		}
		return &http.Response{
			StatusCode: http.StatusBadRequest,
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"stop"}}`)),
			Header:     http.Header{},
			Request:    r,
		}, nil
	})
	for range a.Chat(context.Background(), req) {
	}
	if body == nil {
		t.Fatal("no request was sent")
	}
	return body
}

// A Schema becomes native structured output (output_config.format), never a
// forced tool_choice, which current models reject with a 400. The schema is
// adapted: every object closed, unsupported constraints dropped, property
// names untouched.
func TestAnthropicSchemaUsesOutputConfig(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"minimum": map[string]any{"type": "integer", "minimum": 1}, // a property NAMED minimum
			"items": map[string]any{
				"type":     "array",
				"minItems": 1,
				"items":    map[string]any{"type": "object", "properties": map[string]any{"x": map[string]any{"type": "string", "maxLength": 9}}},
			},
		},
		"required": []string{"minimum"},
	}
	body := captureAnthropicBody(t, engine.ChatRequest{
		Model: "m", MaxTokens: 10, Schema: schema,
		Messages: []engine.Message{{Role: engine.RoleUser, Content: "hi"}},
	})
	if _, forced := body["tool_choice"]; forced {
		t.Fatalf("tool_choice = %v; a schema must not force a tool", body["tool_choice"])
	}
	oc, _ := body["output_config"].(map[string]any)
	format, _ := oc["format"].(map[string]any)
	if format["type"] != "json_schema" {
		t.Fatalf("output_config = %v", body["output_config"])
	}
	got, _ := format["schema"].(map[string]any)
	if got["additionalProperties"] != false {
		t.Errorf("root object not closed: %v", got)
	}
	props, _ := got["properties"].(map[string]any)
	minProp, _ := props["minimum"].(map[string]any)
	if minProp == nil || minProp["minimum"] != nil {
		t.Errorf("property named minimum lost, or its numeric bound kept: %v", props["minimum"])
	}
	items, _ := props["items"].(map[string]any)
	if items["minItems"] != nil {
		t.Errorf("array size constraint kept: %v", items)
	}
	inner, _ := items["items"].(map[string]any)
	if inner["additionalProperties"] != false {
		t.Errorf("nested object not closed: %v", inner)
	}
	x, _ := inner["properties"].(map[string]any)["x"].(map[string]any)
	if x["maxLength"] != nil {
		t.Errorf("string length constraint kept: %v", x)
	}
	if schema["additionalProperties"] != nil {
		t.Error("outputSchema modified the caller's schema")
	}
}

// NoToolCalls keeps the tools declared (the history references them) and
// sends tool_choice none; small max_tokens get thinking headroom.
func TestAnthropicNoToolCallsAndMaxTokens(t *testing.T) {
	body := captureAnthropicBody(t, engine.ChatRequest{
		Model: "m", MaxTokens: 4096, NoToolCalls: true,
		Messages: []engine.Message{{Role: engine.RoleUser, Content: "hi"}},
		Tools:    []engine.ToolDef{{Name: "read_file", Schema: map[string]any{"type": "object"}}},
	})
	if tools, _ := body["tools"].([]any); len(tools) != 1 {
		t.Fatalf("tools = %v; want them declared", body["tools"])
	}
	if choice, _ := body["tool_choice"].(map[string]any); choice["type"] != "none" {
		t.Fatalf("tool_choice = %v; want none", body["tool_choice"])
	}
	if body["max_tokens"] != float64(minMaxTokens) {
		t.Fatalf("max_tokens = %v; want the %d floor", body["max_tokens"], minMaxTokens)
	}
}

// A refusal (HTTP 200, stop_reason refusal) is an error, not an empty answer.
func TestAnthropicRefusalIsAnError(t *testing.T) {
	a := NewAnthropic("test-key")
	sse := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n" +
		"event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"refusal\",\"stop_details\":{\"type\":\"refusal\",\"category\":\"cyber\",\"explanation\":\"declined\"}},\"usage\":{\"output_tokens\":1}}\n\n" +
		"event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
	a.client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(sse)),
			Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
			Request:    r,
		}, nil
	})
	_, err := engine.CollectStream(a.Chat(context.Background(), engine.ChatRequest{
		Model: "m", MaxTokens: 10, Messages: []engine.Message{{Role: engine.RoleUser, Content: "hi"}},
	}), nil)
	if err == nil || !strings.Contains(err.Error(), "declined") || !strings.Contains(err.Error(), "cyber") {
		t.Fatalf("err = %v; want a refusal error naming the category", err)
	}
	if code, ok := engine.Code(err); !ok || code != engine.ErrCodeProvider {
		t.Fatalf("refusal error code = %v, %v; want PROVIDER", code, ok)
	}
}

// Without a Schema, nothing is forced.
func TestAnthropicNoSchemaNoToolChoice(t *testing.T) {
	body := captureAnthropicBody(t, engine.ChatRequest{
		Model: "m", MaxTokens: 10,
		Messages: []engine.Message{{Role: engine.RoleUser, Content: "hi"}},
		Tools:    []engine.ToolDef{{Name: "read_file", Schema: map[string]any{"type": "object"}}},
	})
	if _, ok := body["tool_choice"]; ok {
		t.Fatalf("unexpected tool_choice without a schema: %v", body["tool_choice"])
	}
}
