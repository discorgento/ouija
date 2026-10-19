package engine

import "context"

// ToolFn is a tool implementation: receives a context for cancellation, the
// project root, and parsed input, and returns a ToolResult.
type ToolFn func(ctx context.Context, root string, input map[string]any) ToolResult

// CustomTool pairs a tool definition with its implementation. Totems use this
// in RunRequest.ExtraTools to add tools scoped to a single agent call — no
// global registration needed.
type CustomTool struct {
	Def ToolDef
	Fn  ToolFn
}
