package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// ─── New permission system integration tests ─────────────────────────

// TestEngineNewPermissionAllow: tool set to Allow runs without approval.
func TestEngineNewPermissionAllow(t *testing.T) {
	mp := &mockProvider{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: "read_file", Input: map[string]any{"path": "f.txt"}}}},
		{ToolCalls: []ToolCall{{ID: "2", Name: submitTool, Input: map[string]any{"ok": true}}}},
	}}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(mp, root)

	// New permission config: read_file = Allow.
	cfg := NewPermissionConfig(Ask)
	cfg.Tools["read_file"] = Allow
	eng.SetPermissionConfig(cfg)
	eng.SetApprovalManager(NewApprovalManager()) // no callback = Ask denied

	res, err := eng.Run(context.Background(), RunRequest{
		Schema: map[string]any{"type": "object"}, UseTools: true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"ok":true}` {
		t.Fatalf("got %q, want {\"ok\":true}", res.Text)
	}
}

// TestEngineNewPermissionDeny: tool set to Deny is rejected immediately.
func TestEngineNewPermissionDeny(t *testing.T) {
	mp := &mockProvider{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: "read_file", Input: map[string]any{"path": "f.txt"}}}},
		{ToolCalls: []ToolCall{{ID: "2", Name: submitTool, Input: map[string]any{"done": true}}}},
	}}
	eng := newTestEngine(mp, t.TempDir())

	// New permission config: read_file = Deny.
	cfg := NewPermissionConfig(Allow)
	cfg.Tools["read_file"] = Deny
	eng.SetPermissionConfig(cfg)

	res, err := eng.Run(context.Background(), RunRequest{
		Schema: map[string]any{"type": "object"}, UseTools: true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"done":true}` {
		t.Fatalf("got %q, want {\"done\":true}", res.Text)
	}
}

// TestEngineNewPermissionAskWithApproval: Ask level + approval callback grants access.
func TestEngineNewPermissionAskWithApproval(t *testing.T) {
	mp := &mockProvider{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: "read_file", Input: map[string]any{"path": "f.txt"}}}},
		{ToolCalls: []ToolCall{{ID: "2", Name: submitTool, Input: map[string]any{"ok": true}}}},
	}}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(mp, root)

	// New permission config: read_file = Ask.
	cfg := NewPermissionConfig(Deny)
	cfg.Tools["read_file"] = Ask
	eng.SetPermissionConfig(cfg)

	// Approval manager with a callback that auto-approves.
	am := NewApprovalManager()
	am.SetCallback(func(ctx context.Context, tool string, input map[string]any) (ApprovalResult, error) {
		return ApprovalResult{Allowed: true, ForSession: false}, nil
	})
	eng.SetApprovalManager(am)

	res, err := eng.Run(context.Background(), RunRequest{
		Schema: map[string]any{"type": "object"}, UseTools: true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"ok":true}` {
		t.Fatalf("got %q, want {\"ok\":true}", res.Text)
	}
}

// TestEngineNewPermissionAskDenied: Ask level + callback denies → tool blocked.
func TestEngineNewPermissionAskDenied(t *testing.T) {
	mp := &mockProvider{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: "read_file", Input: map[string]any{"path": "f.txt"}}}},
		{ToolCalls: []ToolCall{{ID: "2", Name: submitTool, Input: map[string]any{"ok": true}}}},
	}}
	eng := newTestEngine(mp, t.TempDir())

	cfg := NewPermissionConfig(Deny)
	cfg.Tools["read_file"] = Ask
	eng.SetPermissionConfig(cfg)

	// Approval manager that denies.
	am := NewApprovalManager()
	am.SetCallback(func(ctx context.Context, tool string, input map[string]any) (ApprovalResult, error) {
		return ApprovalResult{Allowed: false}, nil
	})
	eng.SetApprovalManager(am)

	res, err := eng.Run(context.Background(), RunRequest{
		Schema: map[string]any{"type": "object"}, UseTools: true,
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"ok":true}` {
		t.Fatalf("got %q, want {\"ok\":true}", res.Text)
	}
}

// TestEngineNewPermissionExtraTool: extra (scoped) tools work with new perm system.
func TestEngineNewPermissionExtraTool(t *testing.T) {
	mp := &mockProvider{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: "custom_tool", Input: map[string]any{"x": "y"}}}},
		{ToolCalls: []ToolCall{{ID: "2", Name: submitTool, Input: map[string]any{"ok": true}}}},
	}}
	eng := newTestEngine(mp, t.TempDir())

	// New permission config: custom_tool = Allow.
	cfg := NewPermissionConfig(Deny)
	cfg.Tools["custom_tool"] = Allow
	eng.SetPermissionConfig(cfg)

	res, err := eng.Run(context.Background(), RunRequest{
		Schema:   map[string]any{"type": "object"},
		UseTools: true,
		ExtraTools: []CustomTool{
			{
				Def: ToolDef{Name: "custom_tool", Description: "test"},
				Fn: func(_ context.Context, root string, input map[string]any) ToolResult {
					return ToolResult{Data: "custom ok"}
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"ok":true}` {
		t.Fatalf("got %q, want {\"ok\":true}", res.Text)
	}
}

// A denied (write-tier) tool yields an error observation, never executes.
func TestEngineDeniesWriteTool(t *testing.T) {
	mp := &mockProvider{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: "delete_everything", Input: map[string]any{}}}},
		{ToolCalls: []ToolCall{{ID: "2", Name: submitTool, Input: map[string]any{"done": true}}}},
	}}
	eng := newTestEngine(mp, t.TempDir())

	res, err := eng.Run(context.Background(), RunRequest{Schema: map[string]any{"type": "object"}, UseTools: true})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.Text != `{"done":true}` {
		t.Fatalf("got %q", res.Text)
	}
}
