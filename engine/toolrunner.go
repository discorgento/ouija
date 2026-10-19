package engine

import (
	"context"
	"maps"
	"path/filepath"
	"strconv"
	"sync"
)

// gateState is the live permission/approval wiring shared by Engine and
// ToolRunner. Both hold the SAME pointer, so a setter on the Engine is
// immediately visible to the runner — the previous design mirrored three
// fields by hand ("setters keep both in sync"), which could silently drift
// (e.g. a stale permConfig letting a tool through un-gated).
//
// The Engine's setters may run while a run is executing tools (the chat TUI
// changes permissions mid-session), so access goes through mu; each tool call
// reads one consistent snapshot.
type gateState struct {
	mu          sync.RWMutex
	permConfig  *PermissionConfig
	approvalMgr *ApprovalManager
	toolCb      ToolCallback
}

// gateSnapshot is one consistent read of a gateState.
type gateSnapshot struct {
	permConfig  *PermissionConfig
	approvalMgr *ApprovalManager
	toolCb      ToolCallback
}

func (g *gateState) snapshot() gateSnapshot {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return gateSnapshot{permConfig: g.permConfig, approvalMgr: g.approvalMgr, toolCb: g.toolCb}
}

func (g *gateState) update(fn func(g *gateState)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	fn(g)
}

// ToolRunner is the gating and execution pipeline for a single tool call.
// It coordinates:
//  1. UI progress callback
//  2. Doom-loop detection
//  3. Static bash gates (denylist + protected-path references) — before any prompt
//  4. Permission checks (config-based or legacy risk-tier)
//  5. Bash allowlist (pre-approves known commands; enforces unattended runs)
//  6. Tool execution (built-in or extra)
//  7. Tracer logging
type ToolRunner struct {
	tools     ToolRegistry
	gate      *gateState
	doom      *DoomDetector
	allowlist *BashAllowlist
	tracer    *Tracer
}

type ToolRunnerConfig struct {
	Tools     ToolRegistry
	Gate      *gateState
	Doom      *DoomDetector
	Allowlist *BashAllowlist
	Tracer    *Tracer
}

func NewToolRunner(cfg ToolRunnerConfig) *ToolRunner {
	gate := cfg.Gate
	if gate == nil {
		gate = &gateState{}
	}
	allowlist := cfg.Allowlist
	if allowlist == nil {
		// Never leave the runner with a nil allowlist: the gate calls its
		// methods on every bash call ("empty = nothing pre-approved").
		allowlist = NewBashAllowlist(nil)
	}
	return &ToolRunner{
		tools:     cfg.Tools,
		gate:      gate,
		doom:      cfg.Doom,
		allowlist: allowlist,
		tracer:    cfg.Tracer,
	}
}

// Run gates and executes a tool call. The return value is a ToolResult
// the agent loop can inspect for structured errors.
func (r *ToolRunner) Run(ctx context.Context, label string, call ToolCall, extraFns map[string]ToolFn, extraRisk map[string]RiskTier) ToolResult {
	gate := r.gate.snapshot()
	if cb := gate.toolCb; cb != nil {
		cb(call.Name, call.Input)
	}

	// Create a child span under the active parent (chat span), if any.
	parent := SpanFromContext(ctx)
	span := parent.StartChild("tool." + call.Name)
	span.SetAttr("tool", call.Name) // spans are nil-safe
	if p, ok := call.Input["path"].(string); ok {
		span.SetAttr("path", p)
	}
	defer span.End()

	// ── Doom-loop detection ───────────────────────────────────────
	doom := doomFromContext(ctx, r.doom)
	if doom.Check(call.Name, call.Input) {
		span.SetError("doom loop")
		return ToolResult{Error: &ToolError{Code: ErrCodeDoomLoop, Message: "tool '" + call.Name + "' was called " + strconv.Itoa(doomRepeatLimit) + " times in a row with identical input — blocked. " +
			"Try a different approach or a different argument."}}
	}

	// Extract path from input when present (for permission path checks).
	rawPath, _ := call.Input["path"].(string)

	checkPath := rawPath
	if rawPath != "" && !filepath.IsAbs(rawPath) && r.tools != nil {
		if abs, err := filepath.Abs(filepath.Join(r.tools.Root(), rawPath)); err == nil {
			checkPath = abs
		}
	}

	_, isExtra := extraFns[call.Name]

	// ── Static bash gates — run BEFORE any human prompt ────────────
	// The denylist and the protected-path policy must not be bypassable by
	// "the user will just approve it": they are hard rules, not suggestions.
	if call.Name == "bash" {
		if cmd, ok := call.Input["command"].(string); ok && cmd != "" {
			if IsDenied(cmd) {
				span.SetError("denylist")
				return ToolResult{Error: &ToolError{
					Code:    ErrCodeNotAllowed,
					Message: "command matches a dangerous pattern and is blocked by the denylist.",
				}}
			}
			root := ""
			if r.tools != nil {
				root = r.tools.Root()
			}
			if ref, found := commandRefsProtectedPathIn(root, cmd); found {
				span.SetError("protected path")
				return ToolResult{Error: &ToolError{
					Code:    ErrCodeNotAllowed,
					Message: "command references a protected path (" + ref + ") — credentials and system files are never reachable via bash.",
				}}
			}
		}
	}

	// ── Permission check ──────────────────────────────────────────
	if gate.permConfig != nil {
		lvl := gate.permConfig.Check(call.Name, checkPath)
		switch lvl {
		case Deny:
			span.SetError("denied by config")
			return errRejected("tool '" + call.Name + "' was denied by permission config")
		case Ask:
			if !r.bashPreApproved(call) {
				// Generate preview before requesting approval. It goes on a
				// copy: call.Input is the same map the conversation holds, and
				// a diff written there would be resent to the provider on
				// every later turn.
				approvalInput := call.Input
				if !isExtra && r.tools != nil {
					if preview := r.tools.Preview(ctx, call); preview != "" {
						approvalInput = maps.Clone(call.Input)
						if approvalInput == nil {
							approvalInput = make(map[string]any)
						}
						approvalInput["_preview"] = preview
					}
				}
				if gate.approvalMgr == nil {
					span.SetError("denied (no approval UI)")
					return errRejected("tool '" + call.Name + "' requires approval (no approval UI configured)")
				}
				result, aerr := gate.approvalMgr.Request(ctx, approvalSessionKey(call.Name, checkPath, call.Input), approvalInput)
				if aerr != nil {
					return errRejected(aerr.Error())
				}
				if !result.Allowed {
					span.SetError("rejected by user")
					if result.Cancelled {
						return errRejected("The user CANCELLED your call to '" + call.Name + "'. You may try a different approach or ask for clarification.")
					}
					return errRejected("The user REJECTED your call to '" + call.Name + "'. Do NOT call '" + call.Name + "' again. Respond to the user in text instead.")
				}
				// Tool was already applied externally (e.g. editor) — skip execution.
				if result.SkipExecution {
					doom.ResetCount()
					return ToolResult{Data: "✓ already applied via external editor"}
				}
				// Fresh human consent for this exact call: the allowlist does
				// not get a second veto after the user said yes.
				doom.ResetCount()
			}
		case Allow:
			// Unattended execution: the allowlist is the guard rail, since no
			// human is asked (empty allowlist = no restriction configured).
			if call.Name == "bash" && r.allowlist.Restricts() {
				cmd, _ := call.Input["command"].(string)
				if cmd != "" && !r.allowlist.IsAllowed(cmd) {
					span.SetError("not in allowlist")
					return allowlistError()
				}
			}
		}
	} else if isExtra {
		if extraRisk[call.Name] > RiskReadOnly { // the zero RiskTier is RiskReadOnly
			span.SetError("denied (write tier)")
			return errRejected("tool '" + call.Name + "' requires approval (write tier) and was denied")
		}
	} else if toolRisk(call.Name) != RiskReadOnly {
		span.SetError("denied (write tier)")
		return errRejected("tool '" + call.Name + "' requires approval (write tier) and was denied")
	}

	// ── Execute ───────────────────────────────────────────────────
	if isExtra {
		return extraFns[call.Name](ctx, r.tools.Root(), call.Input)
	}
	return r.tools.Run(ctx, call)
}

// MayRun reports whether a call to the tool could run at all under the
// current gate — regardless of its arguments, which only narrow it further
// (protected paths, the bash denylist). It is false for a tool the gate always
// refuses: write tier with no PermissionConfig, Deny, or Ask with no approval
// UI (except bash, whose allowlisted commands need no prompt).
func (r *ToolRunner) MayRun(def ToolDef, extra bool) bool {
	gate := r.gate.snapshot()
	if gate.permConfig == nil {
		risk := toolRisk(def.Name)
		if extra {
			risk = def.Risk
		}
		return risk == RiskReadOnly
	}
	switch gate.permConfig.Check(def.Name, "") {
	case Allow:
		return true
	case Ask:
		return gate.approvalMgr != nil || (def.Name == "bash" && r.allowlist.Restricts())
	}
	return false
}

// bashPreApproved reports whether the bash command already sits on the
// allowlist (which requires a non-empty allowlist — an empty one means "no
// commands pre-approved yet", NOT "everything approved": the Ask level still
// prompts for each command).
func (r *ToolRunner) bashPreApproved(call ToolCall) bool {
	if call.Name != "bash" || !r.allowlist.Restricts() {
		return false
	}
	cmd, _ := call.Input["command"].(string)
	return cmd != "" && r.allowlist.IsAllowed(cmd)
}

// approvalSessionKey scopes "approve for session" to the tool's target:
// the absolute path for file tools, the exact command for bash. Tools
// without a target fall back to the bare tool name. The encoding (a null byte
// between tool and target) matches the sandbox-escape approval, so the TUI can
// split both keys the same way — a null byte cannot occur in a path.
func approvalSessionKey(tool, absPath string, input map[string]any) string {
	disc := absPath
	if tool == "bash" {
		if cmd, ok := input["command"].(string); ok {
			disc = cmd
		}
	}
	if disc == "" {
		return tool
	}
	return tool + "\x00" + disc
}

func allowlistError() ToolResult {
	return ToolResult{Error: &ToolError{Code: ErrCodeNotAllowed, Message: "command is not in the bash allowlist. " +
		"Add it to .ouija/config.json (tools.bash.allowlist) or approve it interactively in the TUI."}}
}
