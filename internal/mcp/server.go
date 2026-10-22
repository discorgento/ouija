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
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math/big"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

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

	// maxLineBytes caps one JSON-RPC message: an endless line used to be read
	// into memory whole (and copied twice more on the way to the parser).
	maxLineBytes = 4 << 20
	// maxCalls caps the tools/call and resources/read running at once: each
	// totem call has its own budget, so a client firing hundreds of them in
	// parallel multiplied the spend without limit.
	maxCalls = 8
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
	calls   map[string]*inflightCall // request id → running call
	wg      sync.WaitGroup           // running tools/call goroutines
}

// inflightCall is a running tools/call the client can cancel.
type inflightCall struct {
	cancel context.CancelFunc
	// byClient records that notifications/cancelled stopped the call. The
	// call's context alone cannot tell: the server's own shutdown (EOF)
	// cancels it too, and may land between the client's cancel and the check.
	byClient atomic.Bool
}

// inflightKey carries a call's *inflightCall in its context.
type inflightKey struct{}

// cancelledByClient reports whether the client cancelled the call ctx runs
// under — the one case MCP says must get no response.
func cancelledByClient(ctx context.Context) bool {
	c, _ := ctx.Value(inflightKey{}).(*inflightCall)
	return c != nil && c.byClient.Load()
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

// resolveProjectRoot resolves the sandbox root. Doing this here rather than in
// init() keeps file I/O out of process startup, so a usage error can still be
// reported before any config lookup.
func resolveProjectRoot(configPath string) (string, error) {
	root := ""
	cfg, _, err := app.DiscoverConfig(configPath)
	if err != nil && configPath != "" {
		return "", err // the path was asked for: don't fall back to the cwd
	}
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

// Run reads newline-delimited JSON-RPC requests from in and writes responses
// to out, logging via s.logf, until in reaches EOF or a read fails. Tool calls still running
// then are cancelled (the client is gone; stop spending budget), and Run waits
// for them, so nothing writes to out after it returns.
func (s *Server) Run(ctx context.Context, in io.Reader, out io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer func() {
		cancel()
		s.wg.Wait()
	}()
	writer := bufio.NewWriter(out)
	// Reads happen on their own goroutine so ctx can end Run: blocked in a
	// read, Run ignored SIGINT/SIGTERM (caught by the caller's NotifyContext)
	// and only stopped when stdin closed. That goroutine may outlive Run while
	// a read is pending — stdin cannot be interrupted — but it hands over
	// nothing more.
	type read struct {
		line    []byte
		tooLong bool
		err     error
	}
	lines := make(chan read)
	go func() {
		reader := bufio.NewReader(in)
		for {
			line, tooLong, err := readLine(reader, maxLineBytes)
			select {
			case lines <- read{line, tooLong, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return nil
		case r := <-lines:
			if r.tooLong {
				s.writeError(writer, nil, -32700, fmt.Sprintf("parse error: message over %d bytes", maxLineBytes))
			} else if len(r.line) > 0 {
				s.handleLine(ctx, r.line, writer)
			}
			if r.err == io.EOF {
				return nil
			}
			if r.err != nil {
				return r.err
			}
		}
	}
}

// readLine reads one newline-terminated message of at most max bytes. A
// longer one is read to its end and dropped (tooLong), so the stream stays in
// step and the next message is read normally.
func readLine(r *bufio.Reader, max int) (line []byte, tooLong bool, err error) {
	for {
		chunk, err := r.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > max {
				tooLong, line = true, nil
			} else {
				line = append(line, chunk...)
			}
		}
		if err != bufio.ErrBufferFull {
			return line, tooLong, err
		}
	}
}

// Serve runs the stdio server on in/out until in reaches EOF, writing
// diagnostics to logw (stdout belongs to the protocol). This is what a harness
// launches for `ouija mcp` and `ouija-mcp`.
func Serve(ctx context.Context, in io.Reader, out, logw io.Writer) error {
	srv, err := New(func(f string, a ...any) {
		fmt.Fprintf(logw, "[ouija-mcp] "+f+"\n", a...)
	})
	if err != nil {
		return err
	}
	srv.logf("%s started (%d totems)", srv.name, len(registry.List()))
	return srv.Run(ctx, in, out)
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
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	if !json.Valid(line) {
		s.writeError(out, nil, -32700, "parse error")
		return
	}
	// Valid JSON that is not a request object (null, an array, a non-string
	// method, no jsonrpc 2.0) is an Invalid Request — answered with whatever
	// id it carries, so the client can match it.
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil || req.JSONRPC != "2.0" || req.Method == "" {
		var probe struct {
			ID json.RawMessage `json:"id"`
		}
		_ = json.Unmarshal(line, &probe)
		s.writeError(out, probe.ID, -32600, "invalid request")
		return
	}
	// A notification (no id) is never answered, and only notifications/*
	// methods mean anything as one: an id-less tools/call used to run a totem
	// and reply with "id": null.
	isNotification := len(req.ID) == 0 || string(req.ID) == "null"
	if isNotification && !strings.HasPrefix(req.Method, "notifications/") {
		s.logf("ignored %s sent as a notification (no id)", req.Method)
		return
	}

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
		// Off the read loop, like tools/call: a slow file (a FIFO, a network
		// mount) must not stop every other request.
		s.startCall(ctx, out, req.ID, func(callCtx context.Context) { s.handleResourceRead(callCtx, out, req) })
	default:
		if !isNotification {
			s.writeError(out, req.ID, -32601, "method not found: "+req.Method)
		}
	}
}

func toolList(prefix string) []map[string]any {
	var tools []map[string]any
	for _, m := range registry.List() {
		tool := map[string]any{
			"name":        prefix + m.Name(),
			"description": m.Summary(),
			"inputSchema": registry.InputSchema(m), // the totem's own, or {task}
		}
		if a := annotations(m); a != nil {
			tool["annotations"] = a
		}
		tools = append(tools, tool)
	}
	return tools
}

// annotations are the MCP tool annotations of a totem that declares itself
// read-only, so a host may run it without asking. Without that, a tool gets
// none, and MCP's defaults (not read-only, possibly destructive) apply. Every
// totem calls a model provider, so it is open-world either way.
func annotations(t registry.Totem) map[string]any {
	h, ok := t.(registry.Hinter)
	if !ok || !h.Hints().ReadOnly {
		return nil
	}
	return map[string]any{"readOnlyHint": true, "destructiveHint": false, "openWorldHint": true}
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
	// Started from / or the home folder (a harness that launches servers
	// there), the sandbox a totem runs in would be every file of the user:
	// its file tools could read and search the whole home and write into it.
	// Refuse, as resources/read does, rather than run there.
	if broadRoot(s.root) {
		s.logf("tools/call %s refused: broad project root %s", p.Name, s.root)
		s.writeToolResult(out, req.ID, broadRootToolError(s.root), true)
		return
	}
	s.logf("tools/call %s", p.Name)

	task, _ := p.Arguments["task"].(string)
	args := registry.Args{Task: task, Params: p.Arguments}
	s.startCall(ctx, out, req.ID, func(callCtx context.Context) {
		result, runErr := s.runTotem(callCtx, module, args)
		if cancelledByClient(callCtx) {
			// MCP says not to answer a cancelled request.
			s.logf("tools/call %s stopped", p.Name)
			return
		}
		if runErr != nil {
			s.writeToolResult(out, req.ID, toolErrorText(runErr), true)
			return
		}
		text, ferr := registry.Format(result)
		if ferr != nil {
			s.writeToolResult(out, req.ID, fmt.Sprintf("the result cannot be encoded: %v\n%+v", ferr, result), true)
			return
		}
		s.writeToolResult(out, req.ID, text, false)
	})
}

// runTotem runs one totem for a tool call, on a fresh engine (its own budget).
func (s *Server) runTotem(ctx context.Context, totem string, args registry.Args) (any, error) {
	if s.run != nil {
		return s.run(ctx, totem, args)
	}
	outcome, err := app.Run(ctx, totem, args, s.configPath, false, "", nil)
	if err != nil {
		return nil, err
	}
	return outcome.Result, nil
}

// startCall runs fn in its own goroutine, under a context the client can
// cancel with notifications/cancelled (keyed by the request id). It refuses an
// id that is still running — a second call under it could not be cancelled on
// its own — and a call over maxCalls running at once.
func (s *Server) startCall(ctx context.Context, out *bufio.Writer, id json.RawMessage, fn func(ctx context.Context)) {
	ctx, cancel := context.WithCancel(ctx)
	call := &inflightCall{cancel: cancel}
	ctx = context.WithValue(ctx, inflightKey{}, call)
	key := callKey(id)

	s.callsMu.Lock()
	if s.calls == nil {
		s.calls = make(map[string]*inflightCall)
	}
	if _, dup := s.calls[key]; dup {
		s.callsMu.Unlock()
		cancel()
		s.writeError(out, id, -32600, "invalid request: id "+key+" is already in use by a running request")
		return
	}
	if len(s.calls) >= maxCalls {
		s.callsMu.Unlock()
		cancel()
		s.writeError(out, id, -32000, fmt.Sprintf("too many requests running (limit %d): retry when one finishes", maxCalls))
		return
	}
	s.calls[key] = call
	s.callsMu.Unlock()

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer func() {
			s.callsMu.Lock()
			delete(s.calls, key)
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
		call.byClient.Store(true) // before cancel: the call checks it once it stops
		call.cancel()
	}
	return ok
}

// callKey normalizes a JSON-RPC id (number or string) into a map key, so
// `7` and ` 7 ` in a cancellation name the same request.
// A numeric id is keyed by its value, so 1, 1.0 and 1e0 are one request; a
// string id stays distinct from the number it spells.
func callKey(id json.RawMessage) string {
	raw := strings.TrimSpace(string(id))
	var n json.Number
	if dec := json.NewDecoder(strings.NewReader(raw)); !strings.HasPrefix(raw, `"`) && dec.Decode(&n) == nil {
		// Exactly, as a rational: float64 made 2^53+1 and 2^53 one key.
		if r, ok := new(big.Rat).SetString(string(n)); ok {
			return r.RatString()
		}
	}
	return raw
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
//
// A request the client cancelled gets no answer, as MCP says.
func (s *Server) handleResourceRead(ctx context.Context, out *bufio.Writer, req rpcRequest) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		s.writeError(out, req.ID, -32602, "invalid params")
		return
	}
	data, mime, err := s.readResource(ctx, p.URI)
	if cancelledByClient(ctx) {
		return
	}
	if err != nil {
		code := -32602
		if errors.Is(err, errInternal) {
			code = -32603
		}
		s.writeError(out, req.ID, code, err.Error())
		return
	}
	s.writeResourceData(out, req.ID, p.URI, data, mime)
}

// errInternal marks a resources/read failure that is the server's, not the
// request's (-32603 rather than -32602).
var errInternal = errors.New("internal error")

// readResource reads one ouija://project/ resource. Its errors name the
// resource by its project-relative path, never by where it is on disk.
func (s *Server) readResource(ctx context.Context, uri string) ([]byte, string, error) {
	if !strings.HasPrefix(uri, resourceProjectURI) {
		return nil, "", fmt.Errorf("invalid resource URI: must start with %s", resourceProjectURI)
	}
	// Started from / or the home folder (a harness that launches servers
	// there), the "project" is every file of the user: the resource API has
	// no permission gate, so it serves nothing then.
	if broadRoot(s.root) {
		return nil, "", fmt.Errorf("resources are off: the project root is %s — start the MCP server in a project folder", s.root)
	}
	if uri == resourceProjectURI {
		entries, err := os.ReadDir(s.root)
		if err != nil {
			return nil, "", fmt.Errorf("%w: cannot list the project directory", errInternal)
		}
		var lines []string
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() {
				name += "/"
			}
			lines = append(lines, name)
		}
		return []byte(strings.Join(lines, "\n")), "text/plain", nil
	}

	rel := strings.TrimPrefix(uri, resourceProjectURI)
	// SafeResolve, not SafeJoin: it also refuses symlinks pointing out of the
	// sandbox and protected paths (credentials/system files), so an MCP client
	// cannot reach through the resource API what the file tools refuse.
	full, err := engine.SafeResolve(ctx, s.root, rel)
	if err != nil {
		return nil, "", fmt.Errorf("access denied: %s is outside the project or protected", rel)
	}

	// Regular files only, within a size cap: a FIFO blocked the server and a
	// multi-GB file would be read into memory whole.
	fi, err := os.Stat(full)
	switch {
	case err != nil:
		return nil, "", fmt.Errorf("cannot read %s: %w", rel, pathlessError(err))
	case !fi.Mode().IsRegular():
		return nil, "", fmt.Errorf("not a regular file: %s", rel)
	case fi.Size() > maxResourceBytes:
		return nil, "", fmt.Errorf("file too large (%d bytes, limit %d): %s", fi.Size(), maxResourceBytes, rel)
	}
	// Opened through the sandbox, not by name: a link swapped in after the
	// checks above must not be followed. The opened file is checked again.
	f, err := engine.SafeOpenFile(ctx, s.root, rel, os.O_RDONLY, 0)
	if err != nil {
		return nil, "", fmt.Errorf("cannot read %s: %w", rel, pathlessError(err))
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, "", fmt.Errorf("not a regular file: %s", rel)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxResourceBytes+1))
	switch {
	case err != nil:
		return nil, "", fmt.Errorf("cannot read %s: %w", rel, pathlessError(err))
	case int64(len(data)) > maxResourceBytes:
		return nil, "", fmt.Errorf("file too large (limit %d bytes): %s", maxResourceBytes, rel)
	}
	return data, mimeType(rel), nil
}

// pathlessError drops the absolute path an *fs.PathError carries ("open
// /home/me/proj/x: no such file") and keeps the reason.
func pathlessError(err error) error {
	if pe, ok := errors.AsType[*fs.PathError](err); ok {
		return pe.Err
	}
	return err
}

// broadRoot reports whether root is a filesystem root, the home folder or a
// folder above it — after resolving symlinks: a root linked to / passed the
// check made on its spelling. The home folder is taken both from the
// environment ($HOME, %USERPROFILE%) and from the account database, since an
// empty or redirected HOME would otherwise skip the check; when neither
// names one, it fails closed on any root fewer than three levels deep
// (/home/me, C:\Users\me).
func broadRoot(root string) bool {
	var homes []string
	if h, err := os.UserHomeDir(); err == nil && h != "" {
		homes = append(homes, h)
	}
	if u, err := user.Current(); err == nil && u.HomeDir != "" {
		homes = append(homes, u.HomeDir)
	}
	return broadRootOf(root, homes)
}

// broadRootOf is broadRoot with the candidate home folders given.
func broadRootOf(root string, homes []string) bool {
	real := func(p string) string {
		p = filepath.Clean(p)
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return p
	}
	written := filepath.Clean(root)
	root = real(root)
	if filepath.Dir(root) == root {
		return true
	}
	if len(homes) == 0 {
		// Both as written and resolved: on macOS /home is a link to
		// /System/Volumes/Data/home, deep only once resolved.
		return pathDepth(written) < 3 || pathDepth(root) < 3
	}
	for _, home := range homes {
		rel, err := filepath.Rel(root, real(home))
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// pathDepth counts p's components below the volume: /home/me is 2.
func pathDepth(p string) int {
	depth := 0
	for _, part := range strings.Split(p[len(filepath.VolumeName(p)):], string(filepath.Separator)) {
		if part != "" {
			depth++
		}
	}
	return depth
}

// broadRootToolError is the tool result of a call refused because the project
// root is / or the home folder. It names the fix: the agent relays it to the
// user, who is the one who can change where the host starts the server. The
// CONFIG prefix matches toolErrorText, so an agent can branch on it.
func broadRootToolError(root string) string {
	return "CONFIG: tools are off: the project root is " + root +
		" (the filesystem root or the home folder), so a totem could read and write all your files. " +
		"Start the MCP server from a project directory: set the server's cwd in the host config."
}

// maxResourceBytes caps one resources/read.
const maxResourceBytes = 10 << 20

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

// writeResourceData sends file contents: as "text" when they are UTF-8 text,
// else base64 in "blob", as MCP specifies — sent as text, a PNG's bytes were
// replaced by U+FFFD and could not be recovered.
func (s *Server) writeResourceData(out *bufio.Writer, id json.RawMessage, uri string, data []byte, mime string) {
	content := map[string]any{"uri": uri, "mimeType": mime}
	if utf8.Valid(data) && !strings.HasPrefix(mime, "image/") || mime == "image/svg+xml" && utf8.Valid(data) {
		content["text"] = string(data)
	} else {
		content["blob"] = base64.StdEncoding.EncodeToString(data)
	}
	s.writeResult(out, id, map[string]any{"contents": []map[string]any{content}})
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
