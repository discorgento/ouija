package mcp

import (
	"bufio"
	"bytes"
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

// The client cancels a call and the server shuts down before the call
// notices: it is still the client's cancellation, and gets no response.
func TestToolCallCancelledByClientThenShutdown(t *testing.T) {
	s := testServer(t.TempDir())
	stopped := make(chan struct{})
	proceed := make(chan struct{})
	s.run = func(ctx context.Context, _ string, _ registry.Args) (any, error) {
		<-ctx.Done()
		close(stopped)
		<-proceed
		return nil, ctx.Err()
	}
	server, shutdown := context.WithCancel(context.Background())
	defer shutdown()
	var buf bytes.Buffer
	out := bufio.NewWriter(&buf)
	s.handleToolCall(server, out, rpcRequest{JSONRPC: "2.0", ID: json.RawMessage("1"), Method: "tools/call",
		Params: json.RawMessage(`{"name":"ouija_task","arguments":{"task":"x"}}`)})
	if !s.cancelCall(json.RawMessage("1")) {
		t.Fatal("the call was not running")
	}
	<-stopped
	shutdown() // EOF lands before the call checks why it stopped
	close(proceed)
	s.wg.Wait()
	s.writeMu.Lock()
	out.Flush()
	s.writeMu.Unlock()
	if buf.Len() != 0 {
		t.Fatalf("cancelled request was answered: %s", buf.String())
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

// tools/list publishes each totem's own input schema: sticker declares an
// optional task, the others keep the default required one.
func TestToolsListUsesTotemInputSchema(t *testing.T) {
	res := resultOf(t, call(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	tools, _ := res["tools"].([]any)
	required := map[string]any{}
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		schema, _ := tool["inputSchema"].(map[string]any)
		required[tool["name"].(string)] = schema["required"]
	}
	if r, _ := required[defaultPrefix+"sticker"].([]any); len(r) != 0 {
		t.Errorf("sticker requires %v; want an optional task", r)
	}
	if r, _ := required[defaultPrefix+"task"].([]any); len(r) != 1 || r[0] != "task" {
		t.Errorf("task requires %v; want [task]", r)
	}
}

// A totem that only reads (review) is annotated read-only for MCP hosts; the
// ones that write files get no annotations, so hosts keep asking.
func TestToolsListAnnotations(t *testing.T) {
	res := resultOf(t, call(t, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	tools, _ := res["tools"].([]any)
	got := map[string]any{}
	for _, raw := range tools {
		tool, _ := raw.(map[string]any)
		got[tool["name"].(string)] = tool["annotations"]
	}
	review, _ := got[defaultPrefix+"review"].(map[string]any)
	if review["readOnlyHint"] != true || review["destructiveHint"] != false {
		t.Errorf("review annotations = %v, want read-only and not destructive", got[defaultPrefix+"review"])
	}
	for _, name := range []string{"task", "doc", "sticker"} {
		if a, ok := got[defaultPrefix+name]; ok && a != nil {
			t.Errorf("%s writes files but is annotated %v", name, a)
		}
	}
}

// tools/call hands every argument to the totem: Task from "task", and all of
// them as Params.
func TestToolCallPassesParams(t *testing.T) {
	var got registry.Args
	s := testServer(t.TempDir())
	s.run = func(_ context.Context, _ string, args registry.Args) (any, error) {
		got = args
		return "ok", nil
	}
	ps := startPipeServer(t, s)
	ps.send(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ouija_task","arguments":{"task":"x","depth":2}}}`)
	ps.next()
	if err := ps.closeIn(); err != nil {
		t.Fatal(err)
	}
	if got.Task != "x" || got.Params["depth"] != float64(2) || got.Params["task"] != "x" {
		t.Fatalf("args = %+v", got)
	}
}

// An explicit config path that cannot be loaded is an error, not a silent
// fallback to the working directory.
func TestSetConfigPathMissingFile(t *testing.T) {
	s := testServer(t.TempDir())
	if err := s.SetConfigPath(t.TempDir() + "/missing.json"); err == nil {
		t.Fatal("SetConfigPath(missing) = nil; want an error")
	}
}
