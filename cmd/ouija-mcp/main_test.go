package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/discorgento/ouija/internal/mcp"
	"github.com/discorgento/ouija/internal/version"
)

// TestMcpSmoke exercises the in-process mcp.Server.Run path, which covers
// cmd/ouija-mcp/main.go.
func TestMcpSmoke(t *testing.T) {
	ctx := context.Background()

	// Capture stdout/stderr.
	var stdout, stderr bytes.Buffer

	// Create server with stderr logger.
	srv, err := mcp.New(func(f string, a ...any) {
		_, _ = stderr.WriteString("[ouija-mcp] " + f + "\n")
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Log startup (mirrors main.go).
	srv.Logf("%s started (%d modules)", mcp.ServerName(), len(mcp.ListTotems()))

	// Prepare stdin with initialize request.
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
		"params": map[string]any{
			"protocolVersion": "2025-06-18",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]string{"name": "test", "version": "1"},
		},
	}
	reqBytes, _ := json.Marshal(req)
	stdin := io.NopCloser(bytes.NewReader(append(reqBytes, '\n')))

	// Run the server in-process.
	if err := srv.Run(ctx, stdin, &stdout); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Parse response.
	var resp map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v\nraw: %s", err, stdout.Bytes())
	}

	if resp["jsonrpc"] != "2.0" {
		t.Errorf("jsonrpc = %v, want 2.0", resp["jsonrpc"])
	}
	if resp["id"] != float64(1) {
		t.Errorf("id = %v, want 1", resp["id"])
	}
	result, ok := resp["result"].(map[string]any)
	if !ok {
		t.Fatalf("result missing or not object: %#v", resp["result"])
	}
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v, want 2025-06-18", result["protocolVersion"])
	}
	serverInfo, ok := result["serverInfo"].(map[string]any)
	if !ok {
		t.Fatalf("serverInfo missing: %#v", result["serverInfo"])
	}
	if serverInfo["name"] != "ouija" {
		t.Errorf("serverInfo.name = %v, want ouija", serverInfo["name"])
	}
	if serverInfo["version"] != version.Version {
		t.Errorf("serverInfo.version = %v, want %s", serverInfo["version"], version.Version)
	}
}
