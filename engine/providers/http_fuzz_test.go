package providers

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

// FuzzSSEParse feeds random byte sequences through the SSE line-by-line
// parser used by postSSE to ensure it never panics on arbitrary input.
func FuzzSSEParse(f *testing.F) {
	// Seed corpus — realistic SSE events from OpenAI-compatible APIs.
	f.Add([]byte("data: hello\n\n"))
	f.Add([]byte("event: ping\ndata: {}\n\n"))
	f.Add([]byte("data: [DONE]\n\n"))
	f.Add([]byte(":\n\n"))
	f.Add([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n"))
	f.Add([]byte("id: 1\nevent: completion\ndata: {\"text\":\"ok\"}\n\n"))
	f.Add([]byte("data: chunk 1\ndata: chunk 2\n\n"))
	f.Add([]byte("event: error\ndata: {\"code\":429}\n\n"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Replay the same state machine as postSSE.
		br := bufio.NewReader(bytes.NewReader(data))
		var eventType string
		var dataBuf bytes.Buffer

		for {
			line, err := br.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					// Flush any remaining buffered event (same as postSSE).
					if dataBuf.Len() > 0 {
						_ = eventType
						_ = bytes.TrimRight(dataBuf.Bytes(), "\n\r")
					}
					return
				}
				// Non-EOF errors don't occur with bytes.Reader.
				return
			}
			line = strings.TrimRight(line, "\n\r")

			switch {
			case line == "":
				// Empty line = end of event.
				if dataBuf.Len() > 0 {
					_ = eventType
					_ = dataBuf.Bytes()
				}
				eventType = ""
				dataBuf.Reset()

			case strings.HasPrefix(line, "event: "):
				eventType = strings.TrimPrefix(line, "event: ")

			case strings.HasPrefix(line, "data: "):
				d := strings.TrimPrefix(line, "data: ")
				if dataBuf.Len() > 0 {
					dataBuf.WriteByte('\n')
				}
				dataBuf.WriteString(d)

			case strings.HasPrefix(line, ":"):
				// SSE comment — ignore.

			default:
				// Ignore unrecognised lines (e.g. "id:", "retry:").
			}
		}
	})
}

// FuzzToolCallArgs fuzzes the json.Unmarshal that parses tool call
// arguments in openai.go (StreamEvent.EventToolCall). Malformed JSON
// must degrade gracefully without panicking.
func FuzzToolCallArgs(f *testing.F) {
	// Seed corpus — realistic and edge-case tool call arguments.
	f.Add([]byte(`{"city":"San Francisco"}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"code":"let x = 1"}`))
	f.Add([]byte(`[1,2,3]`))
	f.Add([]byte(`invalid json`))
	f.Add([]byte(`{"nested":{"a":[1,2,3]}}`))

	f.Fuzz(func(t *testing.T, args []byte) {
		// This is the exact same pattern used in openai.go:175.
		var input map[string]any
		_ = json.Unmarshal(args, &input)
		if input == nil {
			input = map[string]any{}
		}
		// input is now always a valid non-nil map — no panic possible.
		_ = input
	})
}
