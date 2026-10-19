package engine

import "testing"

func TestClassifyToolCallSubmitIsSequential(t *testing.T) {
	call := ToolCall{Name: submitTool, Input: map[string]any{}}
	cls := classifyToolCall(call, nil)
	if cls != ToolClassSequential {
		t.Fatalf("submitTool should be sequential, got %v", cls)
	}
}

func TestClassifyToolCallReadOnlyIsParallel(t *testing.T) {
	builtins := []string{"read_file", "grep", "glob", "list_dir"}
	for _, name := range builtins {
		call := ToolCall{Name: name, Input: map[string]any{}}
		cls := classifyToolCall(call, nil)
		if cls != ToolClassParallel {
			t.Fatalf("%q should be parallel (read-only), got %v", name, cls)
		}
	}
}

func TestClassifyToolCallWriteIsSequential(t *testing.T) {
	builtins := []string{"write_file", "edit_file", "delete_file", "bash"}
	for _, name := range builtins {
		call := ToolCall{Name: name, Input: map[string]any{}}
		cls := classifyToolCall(call, nil)
		if cls != ToolClassSequential {
			t.Fatalf("%q should be sequential (write), got %v", name, cls)
		}
	}
}

func TestClassifyToolCallUnknownFailsClosed(t *testing.T) {
	call := ToolCall{Name: "make_coffee", Input: map[string]any{}}
	cls := classifyToolCall(call, nil)
	if cls != ToolClassSequential {
		t.Fatalf("unknown tool should default to sequential (fail closed), got %v", cls)
	}
}

func TestClassifyToolCallExtraRiskOverrides(t *testing.T) {
	tests := []struct {
		name  string
		risk  RiskTier
		want  ToolCallClass
		label string
	}{
		{"custom_read", RiskReadOnly, ToolClassParallel, "extra read-only → parallel"},
		{"custom_write", RiskWrite, ToolClassSequential, "extra write → sequential"},
	}
	for _, tc := range tests {
		t.Run(tc.label, func(t *testing.T) {
			call := ToolCall{Name: tc.name, Input: map[string]any{}}
			cls := classifyToolCall(call, map[string]RiskTier{tc.name: tc.risk})
			if cls != tc.want {
				t.Fatalf("classifyToolCall(%q) = %v, want %v", tc.name, cls, tc.want)
			}
		})
	}
}

func TestClassifyToolCallExtraRiskShadowsBuiltin(t *testing.T) {
	// An extra tool with the same name as a built-in, but explicitly marked
	// as write — should override the built-in's read-only classification.
	call := ToolCall{Name: "read_file", Input: map[string]any{}}
	cls := classifyToolCall(call, map[string]RiskTier{"read_file": RiskWrite})
	if cls != ToolClassSequential {
		t.Fatalf("read_file with extraRisk=Write should be sequential, got %v", cls)
	}
}

// ─── classifyToolCalls (batch) ─────────────────────────────────────────

func TestClassifyToolCallsEmpty(t *testing.T) {
	p, s := classifyToolCalls(nil, nil)
	if len(p) != 0 || len(s) != 0 {
		t.Fatalf("empty input: parallel=%d sequential=%d", len(p), len(s))
	}
}

func TestClassifyToolCallsAllParallel(t *testing.T) {
	calls := []ToolCall{
		{Name: "read_file"},
		{Name: "grep"},
		{Name: "glob"},
	}
	p, s := classifyToolCalls(calls, nil)
	if len(p) != 3 {
		t.Fatalf("expected 3 parallel, got %d", len(p))
	}
	if len(s) != 0 {
		t.Fatalf("expected 0 sequential, got %d", len(s))
	}
}

func TestClassifyToolCallsAllSequential(t *testing.T) {
	calls := []ToolCall{
		{Name: "write_file"},
		{Name: "bash"},
		{Name: "edit_file"},
	}
	p, s := classifyToolCalls(calls, nil)
	if len(p) != 0 {
		t.Fatalf("expected 0 parallel, got %d", len(p))
	}
	if len(s) != 3 {
		t.Fatalf("expected 3 sequential, got %d", len(s))
	}
}

func TestClassifyToolCallsMixed(t *testing.T) {
	calls := []ToolCall{
		{Name: "read_file"},  // parallel
		{Name: "write_file"}, // sequential
		{Name: "grep"},       // parallel
		{Name: "bash"},       // sequential
		{Name: "glob"},       // parallel
	}
	p, s := classifyToolCalls(calls, nil)
	if len(p) != 3 {
		t.Fatalf("expected 3 parallel, got %d", len(p))
	}
	if len(s) != 2 {
		t.Fatalf("expected 2 sequential, got %d", len(s))
	}
	// Check order is preserved within each group.
	for i, name := range []string{"read_file", "grep", "glob"} {
		if p[i].Name != name {
			t.Fatalf("parallel[%d] = %q, want %q", i, p[i].Name, name)
		}
	}
	for i, name := range []string{"write_file", "bash"} {
		if s[i].Name != name {
			t.Fatalf("sequential[%d] = %q, want %q", i, s[i].Name, name)
		}
	}
}

func TestClassifyToolCallsSubmitInSequential(t *testing.T) {
	calls := []ToolCall{
		{Name: "read_file"},
		{Name: submitTool},
		{Name: "grep"},
	}
	p, s := classifyToolCalls(calls, nil)
	if len(p) != 2 {
		t.Fatalf("expected 2 parallel, got %d", len(p))
	}
	if len(s) != 1 || s[0].Name != submitTool {
		t.Fatalf("submitTool should be in sequential group, got %v", s)
	}
}
