package engine

import "context"

// Agent runs one conversation loop: call provider → execute tools → repeat
// until a final answer or budget ceiling. Different implementations can
// provide different loop strategies (ReAct, reflective, parallel, etc.).
type Agent interface {
	// Run executes the agent loop. msgs already contain the user's prompt.
	// Returns the final result, the updated conversation, and any error.
	Run(ctx context.Context, req RunRequest, msgs []Message) (*RunResult, []Message, error)
}
