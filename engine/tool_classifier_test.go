package engine

import (
	"slices"
	"testing"
)

func TestParallelSafe(t *testing.T) {
	cases := []struct {
		name  string
		extra map[string]RiskTier
		want  bool
	}{
		{"read_file", nil, true},
		{"grep", nil, true},
		{"glob", nil, true},
		{"list_dir", nil, true},
		{"write_file", nil, false},
		{"edit_file", nil, false},
		{"delete_file", nil, false},
		{"bash", nil, false},
		{SubmitTool, nil, false},    // deferred to last
		{"make_coffee", nil, false}, // unknown: fail closed
		{"lookup", map[string]RiskTier{"lookup": RiskReadOnly}, true},
		{"deploy", map[string]RiskTier{"deploy": RiskWrite}, false},
		{"read_file", map[string]RiskTier{"read_file": RiskWrite}, false}, // extra shadows built-in
	}
	for _, tc := range cases {
		if got := parallelSafe(ToolCall{Name: tc.name}, tc.extra); got != tc.want {
			t.Errorf("parallelSafe(%q, %v) = %v, want %v", tc.name, tc.extra, got, tc.want)
		}
	}
}

func TestClassifyToolCallsKeepsOrder(t *testing.T) {
	calls := []ToolCall{{Name: "read_file"}, {Name: "write_file"}, {Name: "grep"}, {Name: SubmitTool}, {Name: "bash"}, {Name: "glob"}}
	par, seq := classifyToolCalls(calls, nil)
	names := func(cs []ToolCall) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.Name
		}
		return out
	}
	if got := names(par); !slices.Equal(got, []string{"read_file", "grep", "glob"}) {
		t.Errorf("parallel = %v", got)
	}
	if got := names(seq); !slices.Equal(got, []string{"write_file", SubmitTool, "bash"}) {
		t.Errorf("sequential = %v", got)
	}
	if p, s := classifyToolCalls(nil, nil); p != nil || s != nil {
		t.Errorf("empty input → %v, %v", p, s)
	}
}
