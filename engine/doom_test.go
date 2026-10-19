package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ─── Doom-loop tests ────────────────────────────────────────────────

// TestDoomLoopDirect: three identical tool calls in a row — the third is blocked.
func TestDoomLoopDirect(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(&mockProvider{}, root)
	cfg := NewPermissionConfig(Allow) // allow everything, no approval interference
	eng.SetPermissionConfig(cfg)

	call := ToolCall{Name: "read_file", Input: map[string]any{"path": "f.txt"}}

	// Call 1: should succeed.
	r1 := eng.runner.Run(context.Background(), "test", call, nil, nil)
	if strings.HasPrefix(r1.String(), "ERROR:") {
		t.Fatalf("first call should succeed, got: %s", r1.String())
	}

	// Call 2: same input, should still succeed (< 3 threshold).
	r2 := eng.runner.Run(context.Background(), "test", call, nil, nil)
	if strings.HasPrefix(r2.String(), "ERROR:") {
		t.Fatalf("second call should succeed, got: %s", r2.String())
	}

	// Call 3: same input again — must be blocked by doom loop.
	r3 := eng.runner.Run(context.Background(), "test", call, nil, nil)
	if !strings.Contains(r3.String(), "DOOM_LOOP") {
		t.Fatalf("third identical call must be blocked by doom loop, got: %s", r3.String())
	}

	// Also create other.txt so the different-input call succeeds too.
	if err := os.WriteFile(filepath.Join(root, "other.txt"), []byte("world"), 0o644); err != nil {
		t.Fatal(err)
	}
	call2 := ToolCall{Name: "read_file", Input: map[string]any{"path": "other.txt"}}
	r4 := eng.runner.Run(context.Background(), "test", call2, nil, nil)
	if strings.HasPrefix(r4.String(), "ERROR:") {
		t.Fatalf("different input should reset doom counter, got: %s", r4.String())
	}
	if !strings.Contains(r4.String(), "world") {
		t.Fatalf("expected read_file output to contain 'world', got: %s", r4.String())
	}
}

// TestDoomLoopResetsAfterApproval: user approval breaks the loop counter.
func TestDoomLoopResetsAfterApproval(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(&mockProvider{}, root)

	// Set to Ask so every call triggers approval.
	cfg := NewPermissionConfig(Deny)
	cfg.Tools["read_file"] = Ask
	eng.SetPermissionConfig(cfg)

	// Auto-approve every call.
	am := NewApprovalManager()
	am.SetCallback(func(ctx context.Context, tool string, input map[string]any) (ApprovalResult, error) {
		return ApprovalResult{Allowed: true}, nil
	})
	eng.SetApprovalManager(am)

	call := ToolCall{Name: "read_file", Input: map[string]any{"path": "f.txt"}}

	// Even 5 identical calls should work because approval resets doom counter.
	for i := 0; i < 5; i++ {
		r := eng.runner.Run(context.Background(), "test", call, nil, nil)
		if strings.HasPrefix(r.String(), "ERROR:") {
			t.Fatalf("call %d should succeed after approval, got: %s", i+1, r.String())
		}
	}
}

// TestDoomLoopResetsOnNewRun: each engine Run gets a fresh doom state.
func TestDoomLoopResetsOnNewRun(t *testing.T) {
	// First run: make 2 identical calls (below threshold), then submit.
	mp := &mockProvider{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: "read_file", Input: map[string]any{"path": "f.txt"}}}},
		{ToolCalls: []ToolCall{{ID: "2", Name: submitTool, Input: map[string]any{"ok": true}}}},
	}}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(mp, root)
	cfg := NewPermissionConfig(Allow)
	eng.SetPermissionConfig(cfg)

	_, err := eng.Run(context.Background(), RunRequest{Schema: map[string]any{"type": "object"}, UseTools: true})
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	// The engine now has doomRepeat=1 (one read_file call).

	// Second run: same single read_file — should work because getHandler resets doom.
	mp2 := &mockProvider{responses: []ChatResponse{
		{ToolCalls: []ToolCall{{ID: "1", Name: "read_file", Input: map[string]any{"path": "f.txt"}}}},
		{ToolCalls: []ToolCall{{ID: "2", Name: submitTool, Input: map[string]any{"ok": true}}}},
	}}
	eng.SetProvider(mp2)

	_, err = eng.Run(context.Background(), RunRequest{Schema: map[string]any{"type": "object"}, UseTools: true})
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	// If this test passes, the doom state was correctly reset between runs.
}

// Concurrent runs on one Engine (the task totem's parallel passes) must not
// share doom-loop state: three different runs reading the same file, one
// after the other, is not a loop.
func TestDoomStateIsRunScoped(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	eng := newTestEngine(&mockProvider{}, root)
	call := ToolCall{ID: "1", Name: "read_file", Input: map[string]any{"path": "f.txt"}}

	runs := []context.Context{
		withDoomDetector(context.Background(), &DoomDetector{}),
		withDoomDetector(context.Background(), &DoomDetector{}),
		withDoomDetector(context.Background(), &DoomDetector{}),
	}
	for i, ctx := range runs {
		if res := eng.runner.Run(ctx, "p", call, nil, nil); res.Error != nil {
			t.Fatalf("run %d blocked by another run's state: %v", i, res.Error.Message)
		}
	}

	// Within one run the guard still trips on the third identical call.
	ctx := runs[0]
	eng.runner.Run(ctx, "p", call, nil, nil)
	res := eng.runner.Run(ctx, "p", call, nil, nil)
	if res.Error == nil || res.Error.Code != ErrCodeDoomLoop {
		t.Fatalf("third identical call in one run must be blocked, got %+v", res)
	}
}
