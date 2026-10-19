package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"

	_ "github.com/discorgento/ouija/totems" // register the real totems for tools/list
)

// testServer returns a server with a silent logger and the given sandbox root.
func testServer(root string) *Server {
	return &Server{root: root, logf: func(string, ...any) {}, name: defaultName, prefix: defaultPrefix}
}

// callOn sends one newline-terminated JSON-RPC line through a server and decodes
// the response. A notification (no id) produces no output, so it returns nil.
func callOn(t *testing.T, s *Server, request string) map[string]any {
	t.Helper()

	var buf bytes.Buffer
	s.handleLine(context.Background(), []byte(request), bufio.NewWriter(&buf))

	out := strings.TrimSpace(buf.String())
	if out == "" {
		return nil
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("response is not valid JSON: %v\n%s", err, out)
	}
	return resp
}

// call runs a request against a throwaway sandbox root.
func call(t *testing.T, request string) map[string]any {
	t.Helper()
	return callOn(t, testServer(t.TempDir()), request)
}

func resultOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	if resp == nil {
		t.Fatal("no response (nil)")
	}
	if e, ok := resp["error"].(map[string]any); ok {
		t.Fatalf("got error response: %v", e)
	}
	res, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result is %T, want object: %#v", resp["result"], resp["result"])
	}
	return res
}

func errorOf(t *testing.T, resp map[string]any) map[string]any {
	t.Helper()
	if resp == nil {
		t.Fatal("no response (nil), want an error response")
	}
	e, ok := resp["error"].(map[string]any)
	if !ok {
		t.Fatalf("no error in response: %#v", resp)
	}
	return e
}

func TestInitialize(t *testing.T) {
	res := resultOf(t, call(t, `{"jsonrpc":"2.0","id":1,"method":"initialize"}`))

	if res["protocolVersion"] != ProtocolVersion {
		t.Errorf("protocolVersion = %v, want %v", res["protocolVersion"], ProtocolVersion)
	}

	info, ok := res["serverInfo"].(map[string]any)
	if !ok || info["name"] != defaultName {
		t.Errorf("serverInfo = %#v, want name %q", res["serverInfo"], defaultName)
	}

	caps, ok := res["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %#v, want an object", res["capabilities"])
	}
	for _, want := range []string{"tools", "resources"} {
		if _, ok := caps[want]; !ok {
			t.Errorf("capabilities is missing %q", want)
		}
	}
}

func TestPing(t *testing.T) {
	res := resultOf(t, call(t, `{"jsonrpc":"2.0","id":7,"method":"ping"}`))
	if len(res) != 0 {
		t.Errorf("ping result = %#v, want an empty object", res)
	}
}

// Notifications and blank lines must produce nothing on stdout: stdout carries
// protocol messages only, one per line.
func TestNotificationsAndBlankLinesAreSilent(t *testing.T) {
	for _, line := range []string{
		"",
		"   ",
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":null,"method":"no/such/method"}`,
	} {
		if resp := call(t, line); resp != nil {
			t.Errorf("line %q produced a response, want silence: %#v", line, resp)
		}
	}
}

func TestParseError(t *testing.T) {
	resp := call(t, `{"jsonrpc": truncated`)
	if code := errorOf(t, resp)["code"]; code != float64(-32700) {
		t.Errorf("error code = %v, want -32700", code)
	}
}

func TestUnknownMethodWithID(t *testing.T) {
	resp := call(t, `{"jsonrpc":"2.0","id":3,"method":"no/such/method"}`)
	if code := errorOf(t, resp)["code"]; code != float64(-32601) {
		t.Errorf("error code = %v, want -32601", code)
	}
}

func TestToolsList(t *testing.T) {
	res := resultOf(t, call(t, `{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))

	tools, ok := res["tools"].([]any)
	if !ok || len(tools) == 0 {
		t.Fatalf("tools = %#v, want a non-empty array", res["tools"])
	}

	names := make(map[string]bool)
	for _, raw := range tools {
		tool, ok := raw.(map[string]any)
		if !ok {
			t.Fatalf("tool = %#v, want an object", raw)
		}
		name, _ := tool["name"].(string)
		if !strings.HasPrefix(name, defaultPrefix) {
			t.Errorf("tool name %q does not start with %q", name, defaultPrefix)
		}
		if desc, _ := tool["description"].(string); desc == "" {
			t.Errorf("tool %q has an empty description", name)
		}
		names[name] = true
	}

	for _, want := range []string{defaultPrefix + "task", defaultPrefix + "doc", defaultPrefix + "sticker"} {
		if !names[want] {
			t.Errorf("tools/list is missing %q", want)
		}
	}
}

func TestToolCallUnknownTool(t *testing.T) {
	res := resultOf(t, call(t, `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"ouija_nope","arguments":{"task":"x"}}}`))

	if isErr, _ := res["isError"].(bool); !isErr {
		t.Error("isError = false for an unknown tool, want true")
	}
	content, _ := res["content"].([]any)
	if len(content) == 0 {
		t.Fatal("content is empty")
	}
	first, _ := content[0].(map[string]any)
	if text, _ := first["text"].(string); !strings.Contains(text, "unknown tool") {
		t.Errorf("text = %q, want it to mention an unknown tool", text)
	}
}

func TestToolCallInvalidParams(t *testing.T) {
	resp := call(t, `{"jsonrpc":"2.0","id":5,"method":"tools/call","params":"not-an-object"}`)
	if code := errorOf(t, resp)["code"]; code != float64(-32602) {
		t.Errorf("error code = %v, want -32602", code)
	}
}

func TestResourceList(t *testing.T) {
	res := resultOf(t, call(t, `{"jsonrpc":"2.0","id":6,"method":"resources/list"}`))

	resources, ok := res["resources"].([]any)
	if !ok || len(resources) == 0 {
		t.Fatalf("resources = %#v, want a non-empty array", res["resources"])
	}
	first, _ := resources[0].(map[string]any)
	if first["uri"] != resourceProjectURI {
		t.Errorf("resource uri = %v, want %v", first["uri"], resourceProjectURI)
	}
}

func TestResourceRead_DirectoryListing(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	res := resultOf(t, callOn(t, testServer(dir), `{"jsonrpc":"2.0","id":8,"method":"resources/read","params":{"uri":"ouija://project/"}}`))

	contents, _ := res["contents"].([]any)
	if len(contents) != 1 {
		t.Fatalf("contents = %#v, want one entry", res["contents"])
	}
	entry, _ := contents[0].(map[string]any)
	text, _ := entry["text"].(string)

	if !strings.Contains(text, "a.go") {
		t.Errorf("listing = %q, want it to contain a.go", text)
	}
	if !strings.Contains(text, "sub/") {
		t.Errorf("listing = %q, want directories suffixed with /", text)
	}
}

func TestResourceRead_FileWithMimeType(t *testing.T) {
	dir := t.TempDir()
	const body = "package main\n"
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(body), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	res := resultOf(t, callOn(t, testServer(dir), `{"jsonrpc":"2.0","id":9,"method":"resources/read","params":{"uri":"ouija://project/main.go"}}`))

	contents, _ := res["contents"].([]any)
	entry, _ := contents[0].(map[string]any)
	if text, _ := entry["text"].(string); text != body {
		t.Errorf("text = %q, want the file contents", text)
	}
	if entry["mimeType"] != "text/x-go" {
		t.Errorf("mimeType = %v, want text/x-go", entry["mimeType"])
	}
}

// The resource API must not be a way around the file-tool sandbox.
func TestResourceRead_EscapeIsDenied(t *testing.T) {
	resp := callOn(t, testServer(t.TempDir()), `{"jsonrpc":"2.0","id":10,"method":"resources/read","params":{"uri":"ouija://project/../../etc/passwd"}}`)
	e := errorOf(t, resp)
	if msg, _ := e["message"].(string); !strings.Contains(msg, "access denied") {
		t.Errorf("error message = %q, want it to say access denied", msg)
	}
}

func TestResourceRead_URIWithoutPath(t *testing.T) {
	// "ouija://project" (no trailing slash) leaves an empty relative path.
	resp := callOn(t, testServer(t.TempDir()), `{"jsonrpc":"2.0","id":11,"method":"resources/read","params":{"uri":"ouija://project"}}`)
	if code := errorOf(t, resp)["code"]; code != float64(-32602) {
		t.Errorf("error code = %v, want -32602", code)
	}
}

func TestResourceRead_InvalidParams(t *testing.T) {
	resp := call(t, `{"jsonrpc":"2.0","id":12,"method":"resources/read","params":42}`)
	if code := errorOf(t, resp)["code"]; code != float64(-32602) {
		t.Errorf("error code = %v, want -32602", code)
	}
}

func TestMimeType(t *testing.T) {
	tests := map[string]string{
		"main.go":     "text/x-go",
		"ouija.json":  "application/json",
		"README.md":   "text/markdown",
		"index.html":  "text/html",
		"style.css":   "text/css",
		"app.js":      "text/javascript",
		"app.ts":      "text/typescript",
		"layout.xml":  "application/xml",
		"ci.yml":      "text/yaml",
		"logo.svg":    "image/svg+xml",
		"shot.png":    "image/png",
		"photo.jpeg":  "image/jpeg",
		"MAIN.GO":     "text/x-go", // extension is lowercased
		"unknown.zzz": "text/plain",
		"noextension": "text/plain",
	}

	for path, want := range tests {
		if got := mimeType(path); got != want {
			t.Errorf("mimeType(%q) = %q, want %q", path, got, want)
		}
	}
}

func TestRawID(t *testing.T) {
	if got := rawID(nil); got != nil {
		t.Errorf("rawID(nil) = %v, want nil", got)
	}
	if got := rawID(json.RawMessage(`7`)); string(got.(json.RawMessage)) != "7" {
		t.Errorf("rawID(7) = %v, want it passed through", got)
	}
}

func TestToolErrorText(t *testing.T) {
	t.Run("typed error carries its code", func(t *testing.T) {
		got := toolErrorText(engine.ErrAuth("no key"))
		if !strings.HasPrefix(got, "AUTH: ") {
			t.Errorf("toolErrorText() = %q, want an AUTH prefix", got)
		}
	})

	t.Run("plain error uses generic prefix", func(t *testing.T) {
		got := toolErrorText(errors.New("boom"))
		if !strings.HasPrefix(got, "error: ") {
			t.Errorf("toolErrorText() = %q, want an error prefix", got)
		}
	})
}

// MCP stdio framing: exactly one JSON object per line, so a value containing a
// newline must be escaped, never emitted raw.
func TestWrite_FramingIsOneLinePerMessage(t *testing.T) {
	var buf bytes.Buffer
	testServer(".").write(bufio.NewWriter(&buf), map[string]any{"text": "line1\nline2"})

	out := buf.String()
	if strings.Count(out, "\n") != 1 {
		t.Errorf("output has %d newlines, want exactly 1 (framing):\n%q", strings.Count(out, "\n"), out)
	}
	if !strings.HasSuffix(out, "\n") {
		t.Error("output does not end with a newline")
	}
	if !strings.Contains(out, `\n`) {
		t.Error("newline inside the value was not escaped")
	}
}

func TestServerName(t *testing.T) {
	if ServerName() != "ouija" {
		t.Errorf("ServerName() = %q, want \"ouija\"", ServerName())
	}
}

func TestListTotems(t *testing.T) {
	totems := ListTotems()
	if len(totems) == 0 {
		t.Fatal("ListTotems() = 0, want at least the built-in totems")
	}
	names := make(map[string]bool)
	for _, t := range totems {
		names[t.Name()] = true
	}
	for _, want := range []string{"doc", "sticker", "task"} {
		if !names[want] {
			t.Errorf("ListTotems() missing %q: %v", want, totems)
		}
	}
}
