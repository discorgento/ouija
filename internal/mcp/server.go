// Package mcp exposes ouija's totems as Model Context Protocol tools over stdio
// (JSON-RPC 2.0) and wires that server into the MCP-capable agent harnesses
// found on this machine (see install.go). Any MCP client — OpenCode, Claude
// Code, Cursor — can then call the totems as native tools. The host gates each
// call with its own approval UX, and ouija's budget ceiling bounds cost.
//
// Protocol notes (security/correctness):
//   - stdout carries ONLY protocol messages; all logging goes to stderr.
//   - messages are newline-delimited JSON (the MCP stdio framing).
//   - only registered totems are exposed; nothing else is callable.
//   - tools/call runs in its own goroutine: a long totem never blocks ping,
//     tools/list or other calls, and notifications/cancelled stops it.
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/version"
	"github.com/discorgento/ouija/registry"
)

const (
	// ProtocolVersion is the MCP revision this server speaks.
	ProtocolVersion = "2025-06-18"
	defaultName     = "ouija"
	defaultPrefix   = "ouija_"

	resourceProjectURI = "ouija://project/"
)

// Logf receives server diagnostics. stdout belongs to the protocol, so these
// lines must go to stderr.
type Logf func(format string, a ...any)

// Server is the stdio MCP server, bound to one sandbox root for resource reads.
// name and prefix identify the server to harnesses (serverInfo.name and the
// tool namespace); they default to the ouija CLI identity and are overridden
// when the server is embedded in an external totem binary.
type Server struct {
	root       string
	configPath string // explicit project config; "" = discover from the cwd
	logf       Logf
	name       string
	prefix     string

	// run executes a totem for tools/call; nil means app.Run. Tests stub it.
	run func(ctx context.Context, totem string, args registry.Args) (any, error)

	writeMu sync.Mutex // concurrent tool calls share the output stream

	callsMu sync.Mutex
	calls   map[string]*inflightCall // request id → running tools/call
	wg      sync.WaitGroup           // running tools/call goroutines
}

// inflightCall is a running tools/call the client can cancel.
type inflightCall struct {
	cancel context.CancelFunc
}

// New resolves the sandbox root (the config's ProjectRoot when it points
// somewhere specific, else the working directory) and returns a server ready to
// Serve. A nil logf discards diagnostics.
func New(logf Logf) (*Server, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	root, err := resolveProjectRoot("")
	if err != nil {
		return nil, err
	}
	return &Server{root: root, logf: logf, name: defaultName, prefix: defaultPrefix}, nil
}

// SetName overrides the server identity: the name reported in serverInfo and
// the prefix every tool gets (the harness sees `<name>_<totem>`). An empty
// name restores the defaults.
func (s *Server) SetName(name string) {
	if name == "" {
		s.name, s.prefix = defaultName, defaultPrefix
		return
	}
	s.name, s.prefix = name, name+"_"
}

// SetConfigPath pins the project config every tool call runs with (and the
// sandbox root resources are read from), instead of discovering it from the
// working directory. An empty path restores discovery.
func (s *Server) SetConfigPath(path string) error {
	root, err := resolveProjectRoot(path)
	if err != nil {
		return err
	}
	s.root, s.configPath = root, path
	return nil
}

// Logf lets callers (like cmd/ouija-mcp) emit diagnostics through the same
// stderr logger the server uses internally.
func (s *Server) Logf(f string, a ...any) {
	s.logf(f, a...)
}

// resolveProjectRoot resolves the sandbox root. Doing this here rather than in
// init() keeps file I/O out of process startup, so a usage error can still be
// reported before any config lookup.
func resolveProjectRoot(configPath string) (string, error) {
	root := ""
	cfg, _, err := app.DiscoverConfig(configPath)
	if err == nil && cfg.ProjectRoot != "." {
		root = cfg.ProjectRoot
	} else {
		cwd, werr := os.Getwd()
		if werr != nil {
			return "", werr
		}
		root = cwd
	}
	return filepath.Abs(root)
}

// Serve reads newline-delimited JSON-RPC requests from in and writes responses
// to out until in reaches EOF.
func (s *Server) Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	return s.Run(ctx, in, out)
}

// Run is the in-process entry point used by tests and cmd/ouija-mcp.
// It reads from in, writes to out, and logs via s.logf.
// Returns when in reaches EOF or an error occurs. Tool calls still running
// then are cancelled (the client is gone; stop spending budget), and Run waits
// for them, so nothing writes to out after it returns.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		s.wg.Wait()
	}()
	reader := bufio.NewReader(in)
	writer := bufio.NewWriter(out)
	for {
		line, err := reader.ReadBytes('\n')
		if len(line) > 0 {
			s.handleLine(ctx, line, writer)
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
	}
}

// Serve runs the stdio server on in/out, logging to stderr, until in reaches
// EOF. This is what a harness launches for `ouija mcp`.
func Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	srv, err := New(func(f string, a ...any) {
		fmt.Fprintf(os.Stderr, "[ouija-mcp] "+f+"\n", a...)
	})
	if err != nil {
		return err
	}
	srv.logf("%s started (%d modules)", srv.name, len(registry.List()))
	return srv.Serve(ctx, in, out)
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handleLine(ctx context.Context, line []byte, out *bufio.Writer) {
	line = []byte(strings.TrimSpace(string(line)))
	if len(line) == 0 {
		return
	}
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.writeError(out, nil, -32700, "parse error")
		return
	}
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"

	switch req.Method {
	case "initialize":
		s.writeResult(out, req.ID, map[string]any{
			"protocolVersion": ProtocolVersion,
			"capabilities": map[string]any{
				"tools":     map[string]any{},
				"resources": map[string]any{},
			},
			"serverInfo": map[string]any{"name": s.name, "version": version.Version},
		})
	case "notifications/initialized":
		// notification — no response
	case "notifications/cancelled":
		var p struct {
			RequestID json.RawMessage `json:"requestId"`
		}
		if json.Unmarshal(req.Params, &p) == nil && s.cancelCall(p.RequestID) {
			s.logf("request %s cancelled by the client", p.RequestID)
		}
	case "ping":
		s.writeResult(out, req.ID, map[string]any{})
	case "tools/list":
		s.writeResult(out, req.ID, map[string]any{"tools": toolList(s.prefix)})
	case "tools/call":
		s.handleToolCall(ctx, out, req)
	case "resources/list":
		s.writeResult(out, req.ID, map[string]any{"resources": resourceList()})
	case "resources/read":
		s.handleResourceRead(ctx, out, req)
	default:
		if !isNotification {
			s.writeError(out, req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func toolList(prefix string) []map[string]any {
	var tools []map[string]any
	for _, m := range registry.List() {
		tools = append(tools, map[string]any{
			"name":        prefix + m.Name(),
			"description": m.Summary(),
			"inputSchema": registry.InputSchema(m), // the totem's own, or {task}
		})
	}
	return tools
}

func (s *Server) handleToolCall(ctx context.Context, out *bufio.Writer, req rpcRequest) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(out, req.ID, -32602, "invalid params")
		return
	}
	module := strings.TrimPrefix(p.Name, s.prefix)
	if _, ok := registry.Get(module); !ok {
		s.writeToolResult(out, req.ID, "unknown tool: "+p.Name, true)
		return
	}
	s.logf("tools/call %s", p.Name)

	task, _ := p.Arguments["task"].(string)
	args := registry.Args{Task: task, Params: p.Arguments}
	s.startCall(ctx, req.ID, func(callCtx context.Context) {
		result, runErr := s.runTotem(callCtx, module, args)
		if callCtx.Err() != nil && ctx.Err() == nil {
			// Cancelled by the client: MCP says not to answer a cancelled request.
			s.logf("tools/call %s stopped", p.Name)
			return
		}
		if runErr != nil {
			s.writeToolResult(out, req.ID, toolErrorText(runErr), true)
			return
		}
		s.writeToolResult(out, req.ID, registry.Render(result), false)
	})
}

// runTotem runs one totem for a tool call, on a fresh engine (its own budget).
func (s *Server) runTotem(ctx context.Context, totem string, args registry.Args) (any, error) {
	if s.run != nil {
		return s.run(ctx, totem, args)
	}
	outcome, err := app.Run(ctx, totem, args, s.configPath, false, "")
	if err != nil {
		return nil, err
	}
	return outcome.Result, nil
}

// startCall runs fn in its own goroutine, under a context the client can
// cancel with notifications/cancelled (keyed by the request id).
func (s *Server) startCall(ctx context.Context, id json.RawMessage, fn func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(ctx)
	call := &inflightCall{cancel: cancel}
	key := callKey(id)

	s.callsMu.Lock()
	if s.calls == nil {
		s.calls = make(map[string]*inflightCall)
	}
	s.calls[key] = call
	s.callsMu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.callsMu.Lock()
			if s.calls[key] == call { // a reused id may have replaced it
				delete(s.calls, key)
			}
			s.callsMu.Unlock()
			cancel()
		}()
		fn(ctx)
	}()
}

// cancelCall cancels the running tools/call with this request id, reporting
// whether one was running.
func (s *Server) cancelCall(id json.RawMessage) bool {
	s.callsMu.Lock()
	call, ok := s.calls[callKey(id)]
	s.callsMu.Unlock()
	if ok {
		call.cancel()
	}
	return ok
}

// callKey normalizes a JSON-RPC id (number or string) into a map key, so
// `7` and ` 7 ` in a cancellation name the same request.
func callKey(id json.RawMessage) string {
	return strings.TrimSpace(string(id))
}

// toolErrorText renders a totem failure as the tool result text. When the error
// carries a stable engine code (AUTH, CONFIG, BUDGET, …) it is prefixed so the
// calling agent can branch on the class without parsing the message.
func toolErrorText(err error) string {
	if code, ok := engine.Code(err); ok {
		return string(code) + ": " + err.Error()
	}
	return "error: " + err.Error()
}

// --- MCP resources ---

// resourceList returns the available project resources.
func resourceList() []map[string]any {
	return []map[string]any{{
		"uri":         resourceProjectURI,
		"name":        "Project Root",
		"description": "Project root directory and files accessible via ouija://project/<relative-path>",
		"mimeType":    "text/plain",
	}}
}

// handleResourceRead handles an MCP resources/read request.
// Supported URIs:
//   - ouija://project/         → directory listing
//   - ouija://project/<path>   → file content (sandboxed to the project root)
func (s *Server) handleResourceRead(ctx context.Context, out *bufio.Writer, req rpcRequest) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(out, req.ID, -32602, "invalid params")
		return
	}

	if p.URI == resourceProjectURI {
		entries, err := os.ReadDir(s.root)
		if err != nil {
			s.writeError(out, req.ID, -32603, "cannot list directory: "+err.Error())
			return
		}
		var lines []string
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			lines = append(lines, name)
		}
		s.writeResourceResult(out, req.ID, p.URI, strings.Join(lines, "\n"), "text/plain")
		return
	}

	rel := strings.TrimPrefix(p.URI, resourceProjectURI)
	if rel == "" {
		s.writeError(out, req.ID, -32602, "invalid resource URI: must start with "+resourceProjectURI)
		return
	}

	// SafeResolve, not SafeJoin: it also refuses symlinks pointing out of the
	// sandbox and protected paths (credentials/system files), so an MCP client
	// cannot reach through the resource API what the file tools refuse.
	full, err := engine.SafeResolve(ctx, s.root, rel)
	if err != nil {
		s.writeError(out, req.ID, -32602, "access denied: "+err.Error())
		return
	}

	data, err := os.ReadFile(full)
	if err != nil {
		s.writeError(out, req.ID, -32602, "cannot read: "+err.Error())
		return
	}

	s.writeResourceResult(out, req.ID, p.URI, string(data), mimeType(rel))
}

// mimeType returns a best-guess MIME type for a file path.
func mimeType(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "text/x-go"
	case ".json":
		return "application/json"
	case ".md":
		return "text/markdown"
	case ".html", ".htm":
		return "text/html"
	case ".css":
		return "text/css"
	case ".js":
		return "text/javascript"
	case ".ts":
		return "text/typescript"
	case ".xml":
		return "application/xml"
	case ".yaml", ".yml":
		return "text/yaml"
	case ".svg":
		return "image/svg+xml"
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	default:
		return "text/plain"
	}
}

// writeResourceResult sends a resources/read response.
func (s *Server) writeResourceResult(out *bufio.Writer, id json.RawMessage, uri, text, mime string) {
	s.writeResult(out, id, map[string]any{
		"contents": []map[string]any{{
			"uri":      uri,
			"mimeType": mime,
			"text":     text,
		}},
	})
}

// --- JSON-RPC writers (single-line JSON + newline framing) ---

func (s *Server) writeResult(out *bufio.Writer, id json.RawMessage, result any) {
	s.write(out, map[string]any{"jsonrpc": "2.0", "id": rawID(id), "result": result})
}

func (s *Server) writeError(out *bufio.Writer, id json.RawMessage, code int, msg string) {
	s.write(out, map[string]any{"jsonrpc": "2.0", "id": rawID(id), "error": rpcError{Code: code, Message: msg}})
}

func (s *Server) writeToolResult(out *bufio.Writer, id json.RawMessage, text string, isError bool) {
	s.writeResult(out, id, map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	})
}

func (s *Server) write(out *bufio.Writer, msg any) {
	b, err := json.Marshal(msg)
	if err != nil {
		s.logf("marshal error: %v", err)
		return
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	out.Write(b)
	out.WriteByte('\n')
	out.Flush()
}

// rawID returns the id as-is, or JSON null when absent.
func rawID(id json.RawMessage) any {
	if len(id) == 0 {
		return nil
	}
	return id
}

// ServerName returns the default MCP server name (the ouija CLI identity).
func ServerName() string {
	return defaultName
}

// ListTotems returns the registered totems.
func ListTotems() []registry.Totem {
	return registry.List()
}
