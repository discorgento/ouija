package engine

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// recordingApproval returns an ApprovalManager whose callback records every
// prompt key it sees and approves it for the session.
func recordingApproval() (*ApprovalManager, func() []string) {
	var mu sync.Mutex
	var keys []string
	am := NewApprovalManager()
	am.SetCallback(func(ctx context.Context, tool string, input map[string]any) (ApprovalResult, error) {
		mu.Lock()
		keys = append(keys, tool)
		mu.Unlock()
		return ApprovalResult{Allowed: true, ForSession: true}, nil
	})
	return am, func() []string {
		mu.Lock()
		defer mu.Unlock()
		out := make([]string, len(keys))
		copy(out, keys)
		return out
	}
}

func askBashEngine(t *testing.T, allowlist []string) (*Engine, func() []string) {
	t.Helper()
	eng := newTestEngine(&mockProvider{}, t.TempDir())
	cfg := NewPermissionConfig(Deny)
	cfg.Tools["bash"] = Ask
	eng.SetPermissionConfig(cfg)
	if allowlist != nil {
		eng.SetBashAllowlist(allowlist)
	}
	am, prompts := recordingApproval()
	eng.SetApprovalManager(am)
	return eng, prompts
}

func runBash(t *testing.T, eng *Engine, command string) ToolResult {
	t.Helper()
	return eng.runner.Run(context.Background(), "test",
		ToolCall{Name: "bash", Input: map[string]any{"command": command}}, nil, nil)
}

// The session cache used to be keyed by tool name alone: one "Session" click
// on any bash command whitelisted every later command for the whole run.
func TestBashSessionApprovalScopedToCommand(t *testing.T) {
	skipOnWindows(t, "runs /bin/sh")
	eng, prompts := askBashEngine(t, nil) // empty allowlist pre-approves nothing

	if r := runBash(t, eng, "echo one"); r.Error != nil {
		t.Fatalf("first command should run after approval: %v", r.Error)
	}
	got := prompts()
	if len(got) != 1 || got[0] != "bash\x00echo one" {
		t.Fatalf("prompt keys = %q, want [bash\\x00echo one]", got)
	}

	// Same command → session cache, no second prompt.
	if r := runBash(t, eng, "echo one"); r.Error != nil {
		t.Fatalf("repeat of the approved command should run: %v", r.Error)
	}
	if len(prompts()) != 1 {
		t.Fatalf("repeat of the same command must reuse the session approval, prompts = %q", prompts())
	}

	// A different command is NOT covered by the earlier approval.
	if r := runBash(t, eng, "echo two"); r.Error != nil {
		t.Fatalf("new command should be approved on its own: %v", r.Error)
	}
	if len(prompts()) != 2 {
		t.Fatalf("a different command must prompt again, prompts = %q", prompts())
	}
}

// The static gates are hard rules: they must reject before the user is asked,
// so approving can never authorise a denylisted or protected-path command.
func TestBashStaticGatesRunBeforeApproval(t *testing.T) {
	skipOnWindows(t, "runs /bin/sh")
	tests := []struct {
		name    string
		command string
	}{
		{"denylist flag order", "rm -fr /"},
		{"denylist wrapper", "sudo rm -fr /"},
		{"protected file", "cat .env"},
		{"protected dir", "cat ~/.ssh/id_rsa"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			eng, prompts := askBashEngine(t, nil)
			res := runBash(t, eng, tt.command)
			if res.Error == nil {
				t.Fatalf("command %q must be blocked", tt.command)
			}
			if res.Error.Code != ErrCodeNotAllowed {
				t.Fatalf("error code = %v, want %v", res.Error.Code, ErrCodeNotAllowed)
			}
			if len(prompts()) != 0 {
				t.Fatalf("no approval prompt must be shown for %q, got %q", tt.command, prompts())
			}
		})
	}
}

// An allowlisted command is pre-approved: it runs with no prompt at all.
func TestBashAllowlistSkipsPrompt(t *testing.T) {
	skipOnWindows(t, "runs /bin/sh")
	eng, prompts := askBashEngine(t, []string{"echo"})
	res := runBash(t, eng, "echo hi")
	if res.Error != nil {
		t.Fatalf("allowlisted command should run: %v", res.Error)
	}
	if len(prompts()) != 0 {
		t.Fatalf("allowlisted command must not prompt, got %q", prompts())
	}
}

// With bash set to Allow (unattended), the allowlist is the guard rail.
func TestBashAllowlistEnforcedWhenLevelAllow(t *testing.T) {
	skipOnWindows(t, "runs /bin/sh")
	eng := newTestEngine(&mockProvider{}, t.TempDir())
	cfg := NewPermissionConfig(Deny)
	cfg.Tools["bash"] = Allow
	eng.SetPermissionConfig(cfg)
	eng.SetBashAllowlist([]string{"echo"})

	if res := runBash(t, eng, "curl http://example.com"); res.Error == nil {
		t.Fatal("command outside the allowlist must be rejected when level is allow")
	} else if !strings.Contains(res.Error.Message, "allowlist") {
		t.Fatalf("error should mention the allowlist, got %q", res.Error.Message)
	}
	if res := runBash(t, eng, "echo hi"); res.Error != nil {
		t.Fatalf("allowlisted command should run: %v", res.Error)
	}
}

// An empty allowlist is "nothing pre-approved", not "everything approved":
// with level Allow (explicit user opt-in) it runs; nothing is enforced.
func TestBashAllowEmptyAllowlistRunsWhenAllowed(t *testing.T) {
	skipOnWindows(t, "runs /bin/sh")
	eng := newTestEngine(&mockProvider{}, t.TempDir())
	cfg := NewPermissionConfig(Deny)
	cfg.Tools["bash"] = Allow
	eng.SetPermissionConfig(cfg)

	if res := runBash(t, eng, "echo hi"); res.Error != nil {
		t.Fatalf("empty allowlist + allow level should run: %v", res.Error)
	}
}

// Fresh human consent outranks the allowlist for that one call: the user saw
// the exact command and said yes.
func TestBashFreshApprovalNotVetoedByAllowlist(t *testing.T) {
	skipOnWindows(t, "runs /bin/sh")
	eng, prompts := askBashEngine(t, []string{"git"})
	if res := runBash(t, eng, "curl http://example.com"); res.Error != nil {
		t.Fatalf("freshly approved command should run despite the allowlist: %v", res.Error)
	}
	if len(prompts()) != 1 {
		t.Fatalf("command outside the allowlist must prompt, got %q", prompts())
	}
}

// Write tools: "approve for session" is scoped to the path that was approved.
func TestWriteSessionApprovalScopedToPath(t *testing.T) {
	eng := newTestEngine(&mockProvider{}, t.TempDir())
	cfg := NewPermissionConfig(Deny)
	cfg.Tools["write_file"] = Ask
	eng.SetPermissionConfig(cfg)
	am, prompts := recordingApproval()
	eng.SetApprovalManager(am)

	write := func(path string) ToolResult {
		return eng.runner.Run(context.Background(), "test",
			ToolCall{Name: "write_file", Input: map[string]any{"path": path, "content": "x"}}, nil, nil)
	}

	if r := write("a.txt"); r.Error != nil {
		t.Fatalf("a.txt should be written after approval: %v", r.Error)
	}
	if r := write("a.txt"); r.Error != nil {
		t.Fatalf("repeat write should reuse the session approval: %v", r.Error)
	}
	if got := prompts(); len(got) != 1 {
		t.Fatalf("same path must not prompt twice, prompts = %q", got)
	}

	if r := write("b.txt"); r.Error != nil {
		t.Fatalf("b.txt should be writable after its own approval: %v", r.Error)
	}
	if got := prompts(); len(got) != 2 {
		t.Fatalf("approving one path must not approve another, prompts = %q", got)
	}
}

// The engine applies the config file's permission section at construction, so
// non-TUI entrypoints honour it instead of silently running on risk tiers.
func TestNewAppliesConfigPermission(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ProjectRoot = t.TempDir()
	cfg.Permission = &PermissionRule{Default: "deny"}
	eng := New(EngineConfig{Provider: &mockProvider{}, Config: cfg, Tracer: NewTracer(false)})

	pc := eng.PermissionConfig()
	if pc == nil {
		t.Fatal("config permission section must be applied by New")
	}
	if pc.Default != Deny {
		t.Fatalf("default level = %v, want deny", pc.Default)
	}

	// No permission section → nil, so the risk-tier default (write denied) holds.
	cfg2 := DefaultConfig()
	cfg2.ProjectRoot = t.TempDir()
	eng2 := New(EngineConfig{Provider: &mockProvider{}, Config: cfg2, Tracer: NewTracer(false)})
	if eng2.PermissionConfig() != nil {
		t.Fatal("absent permission section must leave the config nil (risk-tier default)")
	}
}

// The Engine and its ToolRunner must never disagree about the gate wiring.
func TestGateSharedBetweenEngineAndRunner(t *testing.T) {
	eng := newTestEngine(&mockProvider{}, t.TempDir())
	cfg := NewPermissionConfig(Allow)
	eng.SetPermissionConfig(cfg)
	if eng.runner.gate.permConfig != cfg {
		t.Fatal("runner must observe the permission config set on the engine")
	}
	am, _ := recordingApproval()
	eng.SetApprovalManager(am)
	if eng.runner.gate.approvalMgr != am {
		t.Fatal("runner must observe the approval manager set on the engine")
	}
	called := false
	eng.SetToolCallback(func(string, map[string]any) { called = true })
	if eng.runner.gate.toolCb == nil {
		t.Fatal("runner must observe the tool callback set on the engine")
	}
	runBash(t, eng, "true")
	if !called {
		t.Fatal("tool callback should fire during a run")
	}
}
