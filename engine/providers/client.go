package providers

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/discorgento/ouija/engine"
)

// Client wraps HTTP transport + circuit breaker for all providers.
// Each provider creates its own Client instance with its own circuit breaker.
type Client struct {
	HTTP    *http.Client
	Circuit *CircuitBreaker // nil = no circuit breaking
}

// NewClient creates a Client and a fresh circuit breaker. timeout bounds the
// wait for the response to start (its headers), not the whole response: a
// stream may last as long as the provider keeps sending, and the engine's
// idle timeout (streamTimeout) stops one that goes silent. A total
// http.Client timeout would cut every long answer at that mark.
func NewClient(timeout time.Duration) *Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.ResponseHeaderTimeout = timeout
	return &Client{
		HTTP:    &http.Client{Transport: tr, CheckRedirect: sameHostRedirect},
		Circuit: NewCircuitBreaker(),
	}
}

// sameHostRedirect follows a redirect only to the same scheme and host. Go
// drops Authorization and Cookie on a redirect to another host, but not the
// other headers: the API key in x-api-key (and any extra header a provider
// sends) would reach whatever host the redirect names. A refused redirect
// comes back as the 3xx response itself, which callers report as an HTTP
// error (it is not retried).
func sameHostRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	if first := via[0].URL; req.URL.Host != first.Host || req.URL.Scheme != first.Scheme {
		return http.ErrUseLastResponse
	}
	return nil
}

// Response size limits. A provider's reply is tokens, far below these; they
// only stop a broken or hostile endpoint from filling the memory.
const (
	maxBodyBytes    = 32 << 20 // a whole JSON or error response
	maxSSELineBytes = 8 << 20  // one SSE line
	maxSSEEvent     = 32 << 20 // one event's data, across its lines
)

// readBody reads at most maxBodyBytes of r, failing past it instead of
// returning a truncated body that would parse as something else.
func readBody(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxBodyBytes+1))
	if err != nil {
		return data, err
	}
	if len(data) > maxBodyBytes {
		return nil, fmt.Errorf("response larger than %d MiB", maxBodyBytes>>20)
	}
	return data, nil
}

// maxReplyBytes caps the text and tool input one streamed response adds up
// to. Each event is capped above, but a stream of small events never ends on
// its own: a hostile endpoint would grow the reply until memory runs out.
const maxReplyBytes = 64 << 20

// replySize counts what a stream has added to a response.
type replySize int

// add counts n more bytes of text or tool input, failing past maxReplyBytes.
func (r *replySize) add(n int) error {
	*r += replySize(n)
	if *r > maxReplyBytes {
		return engine.ErrProvider(fmt.Sprintf("response larger than %d MiB of text and tool input: the provider is broken or hostile", maxReplyBytes>>20))
	}
	return nil
}

// errSSETooLarge ends a stream whose line or event passed the limits.
var errSSETooLarge = fmt.Errorf("SSE: an event passed the size limit (%d MiB a line, %d MiB an event)",
	maxSSELineBytes>>20, maxSSEEvent>>20)

// emptyTurn stands in for an assistant turn that produced no text and no tool
// calls: both APIs reject such a message, which would fail every later call of
// the conversation.
const emptyTurn = "(no output)"

// errStopped ends a stream whose consumer stopped iterating; it is never
// yielded (a range-over-func body that returned false must not be called
// again).
var errStopped = errors.New("stream consumer stopped")

// statusError turns a non-2xx response into an error. 401 and 403 are AUTH
// errors (a missing or refused key, or a plan that does not cover this use),
// so callers can tell them from transient failures.
func statusError(prefix string, code int, body string) error {
	msg := fmt.Sprintf("%sHTTP %d: %s", prefix, code, errorDetail(body))
	if code == http.StatusUnauthorized || code == http.StatusForbidden {
		return engine.ErrAuth(msg)
	}
	// Retryable statuses (429, 5xx) were already retried by the client.
	return engine.ErrProvider(msg)
}

// errorDetail is the readable reason in an error response body: the
// `error.message` the providers send (with the upstream one OpenRouter wraps
// in metadata.raw, when present), else the body cut to a sane length — not a
// page of JSON with request metadata.
func errorDetail(body string) string {
	var e struct {
		Error   json.RawMessage `json:"error"`
		Message string          `json:"message"`
	}
	if json.Unmarshal([]byte(body), &e) == nil {
		var obj struct {
			Message  string `json:"message"`
			Metadata struct {
				Raw string `json:"raw"`
			} `json:"metadata"`
		}
		var str string
		switch {
		case json.Unmarshal(e.Error, &obj) == nil && obj.Message != "":
			if upstream := errorDetail(obj.Metadata.Raw); obj.Metadata.Raw != "" && upstream != obj.Metadata.Raw {
				return obj.Message + ": " + upstream
			}
			return obj.Message
		case json.Unmarshal(e.Error, &str) == nil && str != "":
			return str
		case e.Message != "":
			return e.Message
		}
	}
	const maxBody = 500
	if len(body) > maxBody {
		return strings.ToValidUTF8(body[:maxBody], "") + "…"
	}
	return body
}

// RoundTrip performs the request with retries and circuit breaking.
// Returns the HTTP response (body NOT read — caller must close resp.Body).
func (c *Client) RoundTrip(ctx context.Context, method, url string, headers map[string]string, body []byte) (*http.Response, error) {
	return retryableRoundTrip(ctx, c.HTTP, method, url, headers, body, c.Circuit)
}

// PostJSON POSTs a body and returns (responseBytes, statusCode, error).
func (c *Client) PostJSON(ctx context.Context, url string, headers map[string]string, body []byte) ([]byte, int, error) {
	resp, err := c.RoundTrip(ctx, http.MethodPost, url, headers, body)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := readBody(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}
	return data, resp.StatusCode, nil
}

// GetJSON GETs url and returns (responseBytes, statusCode, error).
func (c *Client) GetJSON(ctx context.Context, url string, headers map[string]string) ([]byte, int, error) {
	resp, err := c.RoundTrip(ctx, http.MethodGet, url, headers, nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	data, err := readBody(resp.Body)
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("read response body: %w", err)
	}
	return data, resp.StatusCode, nil
}

// PostSSE POSTs a body and streams SSE events to onEvent, using the circuit
// breaker for the initial connection. Once streaming begins, no retry.
func (c *Client) PostSSE(ctx context.Context, url string, headers map[string]string, body []byte, onEvent func(eventType string, data []byte) error) error {
	resp, err := c.RoundTrip(ctx, http.MethodPost, url, headers, body)
	if err != nil {
		// RoundTrip already retried (and the circuit breaker may be open).
		return engine.ErrNetwork("SSE: connect", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		raw, readErr := readBody(resp.Body)
		if readErr != nil {
			return engine.ErrProvider(fmt.Sprintf("SSE: HTTP %d (body read error)", resp.StatusCode), readErr)
		}
		return statusError("SSE: ", resp.StatusCode, string(raw))
	}

	br := bufio.NewReader(resp.Body)
	cancel := context.AfterFunc(ctx, func() { resp.Body.Close() })
	defer cancel()
	var eventType string
	var dataBuf bytes.Buffer

	for {
		line, err := readLine(br)
		if len(line) > 0 {
			engine.NotifyActivity(ctx) // the stream is alive, even if this is a keep-alive
		}
		if errors.Is(err, errSSETooLarge) {
			return engine.ErrProvider(err.Error())
		}
		if err != nil {
			if err == io.EOF {
				if dataBuf.Len() > 0 {
					return onEvent(eventType, bytes.TrimRight(dataBuf.Bytes(), "\n\r"))
				}
				return nil
			}
			// The reply had started and broke off: worth repeating the call.
			return engine.ErrStream("SSE: stream broke off", err)
		}
		line = strings.TrimRight(line, "\n\r")

		switch {
		case line == "":
			if dataBuf.Len() > 0 {
				if err := onEvent(eventType, dataBuf.Bytes()); err != nil {
					return err
				}
			}
			eventType = ""
			dataBuf.Reset()

		// The space after the colon is optional in SSE (`data:{…}`); servers
		// that omit it had every event silently dropped.
		case strings.HasPrefix(line, "event:"):
			eventType = strings.TrimPrefix(strings.TrimPrefix(line, "event:"), " ")

		case strings.HasPrefix(line, "data:"):
			data := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			if dataBuf.Len()+len(data) >= maxSSEEvent {
				return engine.ErrProvider(errSSETooLarge.Error())
			}
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(data)

		default:
			// Ignore SSE comments (":") and unrecognized lines (e.g. "id:", "retry:").
		}
	}
}

// readLine is br.ReadString('\n') bounded by maxSSELineBytes: a server that
// never sends a newline would otherwise grow the line without limit.
func readLine(br *bufio.Reader) (string, error) {
	var b strings.Builder
	for {
		chunk, err := br.ReadSlice('\n')
		if b.Len()+len(chunk) > maxSSELineBytes {
			return b.String(), errSSETooLarge
		}
		b.Write(chunk)
		if err == bufio.ErrBufferFull {
			continue
		}
		return b.String(), err
	}
}
