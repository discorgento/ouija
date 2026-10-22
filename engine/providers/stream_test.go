package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
)

func sseServer(c *Client, body string) {
	c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)),
			Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Request: r}, nil
	})
}

// openAIToolCallsSSE streams n tool calls (indexes 0..n-1) and finishes twice.
func openAIToolCallsSSE(n int) string {
	var b strings.Builder
	for i := range n {
		fmt.Fprintf(&b, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":%d,\"id\":\"c%d\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{}\"}}]}}]}\n\n", i, i)
	}
	b.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
	b.WriteString("data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n")
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

// Tool calls come out in the order the model made them, once each.
func TestOpenAIToolCallsInOrderOnce(t *testing.T) {
	for range 20 { // a map would scramble 5 entries well within 20 tries
		o := NewOpenAI("k")
		sseServer(o.client, openAIToolCallsSSE(5))
		resp, err := engine.CollectStream(o.Chat(context.Background(), engine.ChatRequest{Model: "m"}), nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(resp.ToolCalls) != 5 {
			t.Fatalf("got %d tool calls; want 5 (emitted once)", len(resp.ToolCalls))
		}
		for i, tc := range resp.ToolCalls {
			if tc.ID != fmt.Sprintf("c%d", i) {
				t.Fatalf("tool calls out of order: %v", resp.ToolCalls)
			}
		}
	}
}

// A consumer that stops iterating must not be called again (range-over-func
// panics otherwise).
func TestStreamsStopWhenTheConsumerBreaks(t *testing.T) {
	o := NewOpenAI("k")
	sseServer(o.client, openAIToolCallsSSE(3))
	a := NewAnthropic("k")
	sseServer(a.client, "event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n"+
		"event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"a\"}}\n\n"+
		"event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"b\"}}\n\n"+
		"event: message_stop\ndata: {}\n\n")
	for _, p := range []engine.Provider{o, a} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("%s: yielded after the consumer stopped: %v", p.Name(), r)
				}
			}()
			for range p.Chat(context.Background(), engine.ChatRequest{Model: "m"}) {
				break
			}
		}()
	}
}

// response_format carries the closed schema: OpenAI (and Azure via
// OpenRouter) reject a nested object without additionalProperties: false —
// the doc totem's reconcile failed with a 400 on every OpenAI model.
func TestOpenAIResponseFormatClosesObjects(t *testing.T) {
	o := NewOpenAI("k")
	var body map[string]any
	o.client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &body)
		return &http.Response{StatusCode: http.StatusBadRequest, Body: io.NopCloser(strings.NewReader(`{}`)), Header: http.Header{}, Request: r}, nil
	})
	section := map[string]any{"type": "object", "properties": map[string]any{"heading": map[string]any{"type": "string"}}}
	schema := map[string]any{"type": "object", "properties": map[string]any{
		"sections": map[string]any{"type": "array", "items": section},
	}}
	for range o.Chat(context.Background(), engine.ChatRequest{Model: "m", Schema: schema}) {
	}
	js, _ := body["response_format"].(map[string]any)["json_schema"].(map[string]any)
	got, _ := js["schema"].(map[string]any)
	items := got["properties"].(map[string]any)["sections"].(map[string]any)["items"].(map[string]any)
	if got["additionalProperties"] != false || items["additionalProperties"] != false {
		t.Fatalf("objects not closed: %v", got)
	}
}

// The real API opens a tool_use block with "input":{} and streams the
// arguments as input_json_delta fragments; they used to be appended to the
// {} ("{}{...}"), so every tool ran with no arguments.
func TestAnthropicToolInputFromDeltas(t *testing.T) {
	a := NewAnthropic("k")
	sseServer(a.client, "event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t1\",\"name\":\"read_file\",\"input\":{}}}\n\n"+
		"event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\"}}\n\n"+
		"event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"a.go\\\"}\"}}\n\n"+
		"event: content_block_stop\ndata: {\"index\":0}\n\n"+
		"event: content_block_start\ndata: {\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t2\",\"name\":\"list_dir\",\"input\":{\"path\":\".\"}}}\n\n"+
		"event: content_block_stop\ndata: {\"index\":1}\n\n"+
		"event: message_stop\ndata: {}\n\n")
	resp, err := engine.CollectStream(a.Chat(context.Background(), engine.ChatRequest{Model: "m"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 2 || resp.ToolCalls[0].Input["path"] != "a.go" || resp.ToolCalls[1].Input["path"] != "." {
		t.Fatalf("tool calls = %+v; want path a.go from the deltas and . from the start block", resp.ToolCalls)
	}
}

// Tool-call arguments accumulate per index even when a backend repeats the id
// on every chunk, and `data:` without a space is still an event.
func TestOpenAIRepeatedIDAndCompactSSE(t *testing.T) {
	o := NewOpenAI("k")
	sseServer(o.client, "data:{\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n"+
		"data:{\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"c1\",\"function\":{\"arguments\":\"\\\"a.go\\\"}\"}}]}}]}\n\n"+
		"data:{\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n"+
		"data:[DONE]\n\n")
	resp, err := engine.CollectStream(o.Chat(context.Background(), engine.ChatRequest{Model: "m"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Input["path"] != "a.go" || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool calls = %+v; want read_file{path: a.go}", resp.ToolCalls)
	}
}

// OpenRouter's list keeps only text models that take tools, sorted.
func TestListOpenRouterFiltersModels(t *testing.T) {
	o := NewOpenRouter("k")
	o.client.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"data":[
			{"id":"z/chat","supported_parameters":["tools","temperature"],"architecture":{"output_modalities":["text"]}},
			{"id":"a/chat","supported_parameters":["tools"]},
			{"id":"m/no-tools","supported_parameters":["temperature"]},
			{"id":"i/image-only","supported_parameters":["tools"],"architecture":{"output_modalities":["image"]}}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: http.Header{}, Request: r}, nil
	})
	got, err := o.ListModels(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "a/chat" || got[1].ID != "z/chat" {
		t.Fatalf("models = %+v; want a/chat, z/chat", got)
	}
}

// repeatReader streams s n times without holding n copies.
func repeatReader(s string, n int) io.Reader {
	rs := make([]io.Reader, n)
	for i := range rs {
		rs[i] = strings.NewReader(s)
	}
	return io.MultiReader(rs...)
}

// A stream of small events that never ends is cut at maxReplyBytes of text or
// tool input: each event is within the per-event limit, but the reply would
// otherwise grow until memory runs out.
func TestStreamTotalSizeCap(t *testing.T) {
	chunk := strings.Repeat("x", 1<<20)
	n := maxReplyBytes>>20 + 2
	for name, tc := range map[string]struct {
		anthropic bool
		event     string
	}{
		"openai text":       {false, `data: {"choices":[{"index":0,"delta":{"content":"` + chunk + `"}}]}` + "\n\n"},
		"openai tool input": {false, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"c","function":{"name":"x","arguments":"` + chunk + `"}}]}}]}` + "\n\n"},
		"anthropic text":    {true, "event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"" + chunk + "\"}}\n\n"},
		"anthropic tool input": {true, "event: content_block_start\ndata: {\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"t\",\"name\":\"x\",\"input\":{}}}\n\n" +
			"event: content_block_delta\ndata: {\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"" + chunk + "\"}}\n\n"},
	} {
		var p engine.Provider
		var c *Client
		if tc.anthropic {
			a := NewAnthropic("k")
			p, c = a, a.client
		} else {
			o := NewOpenAI("k")
			p, c = o, o.client
		}
		c.HTTP.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(repeatReader(tc.event, n)),
				Header: http.Header{"Content-Type": []string{"text/event-stream"}}, Request: r}, nil
		})
		var err error
		for _, e := range p.Chat(context.Background(), engine.ChatRequest{Model: "m"}) {
			if e != nil {
				err = e
			}
		}
		if err == nil || !strings.Contains(err.Error(), "MiB of text and tool input") {
			t.Errorf("%s: err = %v, want the response size cap", name, err)
		}
	}
}
