package engine

// parallelSafe reports whether a tool call may run concurrently with others
// in the same turn: read-only tools (no side effects, no conflicts) may;
// write and exec tools, submit_result and unknown tools (fail closed) run one
// at a time, in order. An extra tool is classified by its own RiskTier, which
// overrides a built-in of the same name.
func parallelSafe(call ToolCall, extraRisk map[string]RiskTier) bool {
	if call.Name == SubmitTool {
		return false
	}
	risk := toolRisk(call.Name)
	if r, ok := extraRisk[call.Name]; ok {
		risk = r
	}
	return risk == RiskReadOnly
}

// batchToolCalls splits calls into batches run one after the other, in the
// model's order: a run of consecutive parallel-safe calls is one batch (run
// concurrently), every other call is a batch of its own. Pulling all reads
// to the front instead let `[write_file a.go, read_file a.go]` read the file
// as it was before the write.
func batchToolCalls(calls []ToolCall, extraRisk map[string]RiskTier) [][]ToolCall {
	var batches [][]ToolCall
	for i, call := range calls {
		if i > 0 && parallelSafe(call, extraRisk) && parallelSafe(calls[i-1], extraRisk) {
			last := len(batches) - 1
			batches[last] = append(batches[last], call)
			continue
		}
		batches = append(batches, []ToolCall{call})
	}
	return batches
}
