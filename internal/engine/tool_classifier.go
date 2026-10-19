package engine

// ToolCallClass classifies a tool call for parallel vs sequential execution.
type ToolCallClass int

const (
	// ToolClassParallel indicates the tool is safe to run concurrently with
	// other parallel tools (read-only, no side effects).
	ToolClassParallel ToolCallClass = iota
	// ToolClassSequential indicates the tool must run in order, alone
	// (write, bash, submit_result, or any tool with side effects).
	ToolClassSequential
)

// classifyToolCall determines whether a single tool call is safe to parallelize.
//
// Read-only tools (RiskReadOnly) can run in parallel — they have no side
// effects and don't conflict with each other. Write/exec tools (RiskWrite)
// must run sequentially to avoid races. Extra tools are classified by their
// explicit RiskTier; unknown extra tools default to sequential (fail closed).
func classifyToolCall(call ToolCall, extraRisk map[string]RiskTier) ToolCallClass {
	if call.Name == submitTool {
		return ToolClassSequential
	}

	risk := toolRisk(call.Name)
	if r, ok := extraRisk[call.Name]; ok {
		risk = r
	}

	if risk == RiskReadOnly {
		return ToolClassParallel
	}
	return ToolClassSequential
}

// classifyToolCalls splits tool calls into parallel-safe and sequential
// groups, preserving original order within each group.
//
// The caller should pass already-deduplicated calls. submit_result stays
// in the sequential group so it is deferred to last (the caller handles
// this by checking for submitToolName separately).
func classifyToolCalls(calls []ToolCall, extraRisk map[string]RiskTier) (parallel, sequential []ToolCall) {
	for _, call := range calls {
		if classifyToolCall(call, extraRisk) == ToolClassParallel {
			parallel = append(parallel, call)
		} else {
			sequential = append(sequential, call)
		}
	}
	return parallel, sequential
}
