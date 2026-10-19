package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"sync/atomic"
	"testing"
	"time"

	"github.com/discorgento/ouija/registry"
)

// pipeServer runs s.Run over in-memory pipes. send writes one request line;
// next reads one response; closeIn signals EOF and waits for Run to return.
type pipeServer struct {
	t     *testing.T
	inW   *io.PipeWriter
	lines chan map[string]any
	done  chan error
}

func startPipeServer(t *testing.T, s *Server) *pipeServer {
	t.Helper()
	inR, inW := io.Pipe()
	outR, outW := io.Pipe()
	ps := &pipeServer{t: t, inW: inW, lines: make(chan map[string]any, 16), done: make(chan error, 1)}
	go func() {
		ps.done <- s.Run(context.Background(), inR, outW)
		outW.Close()
	}()
	go func() {
		sc := bufio.NewScanner(outR)
		for sc.Scan() {
			var m map[string]any
			if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
				t.Errorf("bad response line %q: %v", sc.Text(), err)
				continue
			}
			ps.lines <- m
		}
		close(ps.lines)
	}()
	return ps
}

func (ps *pipeServer) send(line string) {
	ps.t.Helper()
	if _, err := io.WriteString(ps.inW, line+"\n"); err != nil {
		ps.t.Fatalf("send: %v", err)
	}
}

func (ps *pipeServer) next() map[string]any {
	ps.t.Helper()
	select {
	case m, ok := <-ps.lines:
		if !ok {
			ps.t.Fatal("output closed, want a response")
		}
		return m
	case <-time.After(5 * time.Second):
		ps.t.Fatal("timed out waiting for a response")
		return nil
	}
}

func (ps *pipeServer) closeIn() error {
	ps.t.Helper()
	ps.inW.Close()
	select {
	case err := <-ps.done:
		return err
	case <-time.After(5 * time.Second):
		ps.t.Fatal("Run did not return after EOF")
		return nil
	}
}

const callTask = `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ouija_task","arguments":{"task":"x"}}}`

// A long tool call must not block other requests.
func TestToolCallDoesNotBlockOtherRequests(t *testing.T) {
	release := make(chan struct{})
	s := testServer(t.TempDir())
	s.run = func(ctx context.Context, _ string, _ registry.Args) (any, error) {
		<-release
		return map[string]string{"ok": "yes"}, nil
	}
	ps := startPipeServer(t, s)

	ps.send(callTask)
	ps.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if got := ps.next(); got["id"] != float64(2) {
		t.Fatalf("first response id = %v, want the ping (2) while the call runs", got["id"])
	}

	close(release)
	got := ps.next()
	if got["id"] != float64(1) {
		t.Fatalf("second response id = %v, want the tool call (1)", got["id"])
	}
	if res := resultOf(t, got); res["isError"] != false {
		t.Fatalf("tool call failed: %v", res)
	}
	if err := ps.closeIn(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// notifications/cancelled stops the call, and the cancelled request gets no
// response.
func TestToolCallCancelledByClient(t *testing.T) {
	var sawCancel atomic.Bool
	started := make(chan struct{})
	s := testServer(t.TempDir())
	s.run = func(ctx context.Context, _ string, _ registry.Args) (any, error) {
		close(started)
		<-ctx.Done()
		sawCancel.Store(true)
		return nil, ctx.Err()
	}
	ps := startPipeServer(t, s)

	ps.send(callTask)
	<-started
	ps.send(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":"user"}}`)
	ps.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if got := ps.next(); got["id"] != float64(2) {
		t.Fatalf("got response %v, want only the ping", got)
	}
	if err := ps.closeIn(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawCancel.Load() {
		t.Fatal("the totem's context was not cancelled")
	}
	for m := range ps.lines {
		if m["id"] == float64(1) {
			t.Fatalf("cancelled request was answered: %v", m)
		}
	}
}

// On EOF the server cancels running calls and waits for them: Run returns
// only after their goroutines are done writing.
func TestRunCancelsAndWaitsForInflightCallsOnEOF(t *testing.T) {
	started := make(chan struct{})
	var finished atomic.Bool
	s := testServer(t.TempDir())
	s.run = func(ctx context.Context, _ string, _ registry.Args) (any, error) {
		close(started)
		<-ctx.Done()
		time.Sleep(20 * time.Millisecond) // cleanup still in progress
		finished.Store(true)
		return nil, ctx.Err()
	}
	ps := startPipeServer(t, s)

	ps.send(callTask)
	<-started
	if err := ps.closeIn(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !finished.Load() {
		t.Fatal("Run returned before the in-flight call finished")
	}
}
