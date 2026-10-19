package providers

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client wraps HTTP transport + circuit breaker for all providers.
// Each provider creates its own Client instance with its own circuit breaker.
type Client struct {
	HTTP    *http.Client
	Circuit *CircuitBreaker // nil = no circuit breaking
}

// NewClient creates a Client with the given timeout and a fresh circuit breaker.
func NewClient(timeout time.Duration) *Client {
	return &Client{
		HTTP:    &http.Client{Timeout: timeout},
		Circuit: NewCircuitBreaker(),
	}
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
	data, err := io.ReadAll(resp.Body)
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
		return fmt.Errorf("SSE: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		raw, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			return fmt.Errorf("SSE: HTTP %d (body read error: %w)", resp.StatusCode, readErr)
		}
		return fmt.Errorf("SSE: HTTP %d: %s", resp.StatusCode, string(raw))
	}

	br := bufio.NewReader(resp.Body)
	cancel := context.AfterFunc(ctx, func() { resp.Body.Close() })
	defer cancel()
	var eventType string
	var dataBuf bytes.Buffer

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				if dataBuf.Len() > 0 {
					_ = onEvent(eventType, bytes.TrimRight(dataBuf.Bytes(), "\n\r"))
				}
				return nil
			}
			return err
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

		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")

		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(data)

		case strings.HasPrefix(line, ":"):
			// SSE comment — ignore.

		default:
			// Ignore unrecognized lines (e.g. "id:", "retry:").
		}
	}
}
