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

// Anthropic has no response_format: a Schema must become a forced
// submit_result call whose input_schema is the requested schema.
func TestAnthropicSchemaForcesSubmitTool(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}}
	body := captureAnthropicBody(t, engine.ChatRequest{
		Model: "m", MaxTokens: 10, Schema: schema,
		Messages: []engine.Message{{Role: engine.RoleUser, Content: "hi"}},
	})

	choice, _ := body["tool_choice"].(map[string]any)
	if choice["type"] != "tool" || choice["name"] != engine.SubmitTool {
		t.Fatalf("tool_choice = %v, want forced %s", body["tool_choice"], engine.SubmitTool)
	}
	tools, _ := body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want only %s", tools, engine.SubmitTool)
	}
	tool, _ := tools[0].(map[string]any)
	if tool["name"] != engine.SubmitTool || tool["input_schema"] == nil {
		t.Fatalf("submit tool malformed: %v", tool)
	}
	if props, _ := tool["input_schema"].(map[string]any)["properties"].(map[string]any); props["ok"] == nil {
		t.Fatalf("input_schema is not the requested schema: %v", tool["input_schema"])
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
