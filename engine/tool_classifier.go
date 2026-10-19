package engine

// parallelSafe reports whether a tool call may run concurrently with others
// in the same turn: read-only tools (no side effects, no conflicts) may;
// write and exec tools, submit_result (deferred to last) and unknown tools
// (fail closed) run one at a time, in order. An extra tool is classified by
// its own RiskTier, which overrides a built-in of the same name.
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

// classifyToolCalls splits already-deduplicated calls into the parallel-safe
// and the sequential ones, keeping the original order within each group.
func classifyToolCalls(calls []ToolCall, extraRisk map[string]RiskTier) (parallel, sequential []ToolCall) {
	for _, call := range calls {
		if parallelSafe(call, extraRisk) {
			parallel = append(parallel, call)
		} else {
			sequential = append(sequential, call)
		}
	}
	return parallel, sequential
}
