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
