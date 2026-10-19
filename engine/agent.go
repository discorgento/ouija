package engine

import (
	"context"
	"log/slog"
)

// Agent runs one conversation loop: call provider → execute tools → repeat
// until a final answer or budget ceiling. Different implementations can
// provide different loop strategies (ReAct, reflective, parallel, etc.); pass
// one in EngineConfig.Agent.
//
// An Agent holds no per-run state: everything a run needs arrives in env,
// snapshotted from the Engine when the run starts. One Agent therefore serves
// concurrent runs, and the Engine's setters (SetModel, SetProvider, …) never
// race a run in progress — they apply from the next run on.
type Agent interface {
	// Run executes the agent loop. msgs already contain the user's prompt.
	// Returns the final result, the updated conversation, and any error.
	Run(ctx context.Context, env RunEnv, req RunRequest, msgs []Message) (*RunResult, []Message, error)
}

// RunEnv is the environment of one agent run. It is a snapshot: the Engine
// builds a fresh one for every Run/Chat call.
type RunEnv struct {
	Provider Provider      // model backend for every call of this run
	Model    string        // model name for every call of this run
	Config   *Config       // project config; read-only
	Tools    ToolRegistry  // built-in tool definitions (RunRequest.UseTools)
	RunTool  RunToolFn     // gated execution of any tool call (built-in or extra)
	Budget   BudgetMeter   // token/step/USD ceilings shared by every run of the Engine
	Tracer   *Tracer       // nil-safe
	Logger   *slog.Logger  // never nil
	OnToken  TokenCallback // optional: streamed text tokens (e.g. a TUI)
	// MayRun reports whether RunTool could ever run a call to the tool (extra
	// = a RunRequest.ExtraTools tool), so the agent offers the model only
	// those. nil means every tool may run.
	MayRun func(def ToolDef, extra bool) bool
}

// mayRun is MayRun with the nil default.
func (e RunEnv) mayRun(def ToolDef, extra bool) bool {
	return e.MayRun == nil || e.MayRun(def, extra)
}

// BudgetMeter records provider usage against the Engine's ceilings
// (Config.Budget).
type BudgetMeter interface {
	// Add records one provider call's usage. It returns an ErrBudget error once
	// a ceiling is exceeded; the run must stop.
	Add(Usage) error
}
