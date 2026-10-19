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

	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/engine"
	"github.com/discorgento/ouija/internal/registry"
	"github.com/discorgento/ouija/internal/version"
)

const (
	// ProtocolVersion is the MCP revision this server speaks.
	ProtocolVersion = "2025-06-18"
	serverName      = "ouija"
	toolPrefix      = "ouija_"

	resourceProjectURI = "ouija://project/"
)

// Logf receives server diagnostics. stdout belongs to the protocol, so these
// lines must go to stderr.
type Logf func(format string, a ...any)

// Server is the stdio MCP server, bound to one sandbox root for resource reads.
type Server struct {
	root string
	logf Logf
}

// New resolves the sandbox root (the config's ProjectRoot when it points
// somewhere specific, else the working directory) and returns a server ready to
// Serve. A nil logf discards diagnostics.
func New(logf Logf) (*Server, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	root, err := resolveProjectRoot()
	if err != nil {
		return nil, err
	}
	return &Server{root: root, logf: logf}, nil
}

// Logf lets callers (like cmd/ouija-mcp) emit diagnostics through the same
// stderr logger the server uses internally.
func (s *Server) Logf(f string, a ...any) {
	s.logf(f, a...)
}

// resolveProjectRoot resolves the sandbox root. Doing this here rather than in
// init() keeps file I/O out of process startup, so a usage error can still be
// reported before any config lookup.
func resolveProjectRoot() (string, error) {
	root := ""
	cfg, _, err := app.DiscoverConfig("")
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
// Returns when in reaches EOF or an error occurs.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
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
	srv.logf("%s started (%d modules)", serverName, len(registry.List()))
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
			"serverInfo": map[string]any{"name": serverName, "version": version.Version},
		})
	case "notifications/initialized":
		// notification — no response
	case "ping":
		s.writeResult(out, req.ID, map[string]any{})
	case "tools/list":
		s.writeResult(out, req.ID, map[string]any{"tools": toolList()})
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

func toolList() []map[string]any {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{"type": "string", "description": "the task/request to work on"},
		},
		"required": []string{"task"},
	}
	var tools []map[string]any
	for _, m := range registry.List() {
		tools = append(tools, map[string]any{
			"name":        toolPrefix + m.Name(),
			"description": m.Summary(),
			"inputSchema": schema,
		})
	}
	return tools
}

func (s *Server) handleToolCall(ctx context.Context, out *bufio.Writer, req rpcRequest) {
	var p struct {
		Name      string `json:"name"`
		Arguments struct {
			Task string `json:"task"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(out, req.ID, -32602, "invalid params")
		return
	}
	module := strings.TrimPrefix(p.Name, toolPrefix)
	if _, ok := registry.Get(module); !ok {
		s.writeToolResult(out, req.ID, "unknown tool: "+p.Name, true)
		return
	}
	s.logf("tools/call %s", p.Name)

	outcome, runErr := app.Run(ctx, module,
		registry.Args{Task: p.Arguments.Task}, "", false, "")
	if runErr != nil {
		s.writeToolResult(out, req.ID, toolErrorText(runErr), true)
		return
	}
	s.writeToolResult(out, req.ID, render(outcome.Result), false)
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

// render turns a totem result into text: its Render() when it has one, else
// indented JSON.
func render(v any) string {
	if r, ok := v.(registry.Renderer); ok {
		return r.Render()
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
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

// ServerName returns the MCP server name.
func ServerName() string {
	return serverName
}

// ListTotems returns the registered totems.
func ListTotems() []registry.Totem {
	return registry.List()
}
