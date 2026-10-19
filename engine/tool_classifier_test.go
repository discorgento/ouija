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
		{SubmitTool, nil, false},    // runs alone
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

// Batches keep the model's order: only consecutive read-only calls share one.
func TestBatchToolCallsKeepsOrder(t *testing.T) {
	calls := []ToolCall{{Name: "read_file"}, {Name: "grep"}, {Name: "write_file"}, {Name: "read_file"}, {Name: SubmitTool}, {Name: "bash"}, {Name: "glob"}, {Name: "list_dir"}}
	var got [][]string
	for _, b := range batchToolCalls(calls, nil) {
		var names []string
		for _, c := range b {
			names = append(names, c.Name)
		}
		got = append(got, names)
	}
	want := [][]string{{"read_file", "grep"}, {"write_file"}, {"read_file"}, {SubmitTool}, {"bash"}, {"glob", "list_dir"}}
	if !slices.EqualFunc(got, want, slices.Equal) {
		t.Errorf("batches = %v, want %v", got, want)
	}
	if b := batchToolCalls(nil, nil); b != nil {
		t.Errorf("empty input → %v", b)
	}
}
