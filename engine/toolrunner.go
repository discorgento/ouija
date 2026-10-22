package engine

import (
	"context"
	"encoding/json"
	"maps"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
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
	journal   *Journal // nil: changes are not recorded for undo
}

type ToolRunnerConfig struct {
	Tools     ToolRegistry
	Gate      *gateState
	Doom      *DoomDetector
	Allowlist *BashAllowlist
	Tracer    *Tracer
	Journal   *Journal
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
		journal:   cfg.Journal,
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
	if p, ok := call.Input["pattern"].(string); ok { // grep/glob: what was searched
		span.SetAttr("pattern", p)
	}
	if c, ok := call.Input["command"].(string); ok { // bash: its first line
		c, _, _ = strings.Cut(strings.TrimSpace(c), "\n")
		if len(c) > 120 {
			c = strings.ToValidUTF8(c[:117], "") + "..."
		}
		span.SetAttr("command", c)
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

	checkPath := r.checkPathFor(rawPath)

	_, isExtra := extraFns[call.Name]

	// ── Static bash gates — run BEFORE any human prompt ────────────
	// The denylist and the protected-path policy must not be bypassable by
	// "the user will just approve it": they are hard rules, not suggestions.
	if call.Name == "bash" {
		if cmd, ok := call.Input["command"].(string); ok && cmd != "" {
			// A shell cannot run a NUL byte, and one let a command's approval
			// key spell another key ("bash\x00" + "unsandboxed\x00cat …").
			if strings.ContainsRune(cmd, 0) {
				span.SetError("NUL in command")
				return errInvalidInput("bash: the command contains a NUL byte")
			}
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

	// ── Running outside the sandbox — always the user's call ────────
	// Asked for every time (a session approval covers that exact command
	// only), whatever the permission level or the allowlist says: those
	// approve a command, not lifting the confinement it runs in.
	if sb := sandboxOf(r.tools); call.Name == "bash" && call.Input["unsandboxed"] == true && sb != nil && sb.Enabled {
		cmd, _ := call.Input["command"].(string)
		// The permission level holds here too — deny, and allow's allowlist:
		// lifting the sandbox is asked on top of the permission check, never
		// instead of it.
		if gate.permConfig != nil {
			switch gate.permConfig.Check(call.Name, checkPath) {
			case Deny:
				span.SetError("denied by config")
				return errRejected("tool '" + call.Name + "' was denied by permission config")
			case Allow:
				if r.allowlist.Restricts() && !r.allowlist.IsAllowed(cmd) {
					span.SetError("not in allowlist")
					return allowlistError()
				}
			}
		} else {
			// No permission config: bash, a write-tier tool, is refused
			// outright, and lifting its sandbox must not be laxer.
			span.SetError("denied (write tier)")
			return errRejected("tool '" + call.Name + "' requires approval (write tier) and was denied")
		}
		if !sb.UnsandboxedAllowed() {
			span.SetError("unsandboxed forbidden")
			return errRejected("running bash outside the sandbox is turned off (sandbox.allowUnsandboxed: false); ask the user to allow what the command needs under tools.bash.sandbox")
		}
		if gate.approvalMgr == nil {
			span.SetError("denied (no approval UI)")
			return errRejected("running bash outside the sandbox requires the user's approval (no approval UI configured)")
		}
		approvalInput := maps.Clone(call.Input)
		approvalInput["_preview"] = "⚠ runs OUTSIDE the bash sandbox: full access to your files and network\n" + r.tools.Preview(ctx, call)
		// Its own key space: an ordinary bash approval ("bash\x00" + command,
		// and a command holds no NUL) can never be this one.
		result, aerr := gate.approvalMgr.Request(ctx, "bash\x00\x00unsandboxed\x00"+cmd, approvalInput)
		if aerr != nil {
			return errRejected(aerr.Error())
		}
		if !result.Allowed {
			span.SetError("rejected by user")
			return errRejected("The user REJECTED running this command outside the sandbox. Do not retry it unsandboxed; respond to the user in text instead.")
		}
		ctx = context.WithValue(ctx, unsandboxedKey{}, cmd)
		res := r.tools.Run(ctx, call)
		recordBashResult(span, res)
		return res
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
				// not get a second veto after the user said yes. An answer from
				// the session cache is not one: resetting there let an approved
				// call repeat forever, past the doom-loop limit.
				if !result.Cached {
					doom.ResetCount()
				}
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
	// The file as it was goes to the undo journal; a change that did not
	// happen leaves nothing to undo.
	entry := -1
	if r.journal != nil && journaled[call.Name] && rawPath != "" {
		entry = r.journal.snapshot(ctx, r.tools.Root(), rawPath)
	}
	res := r.tools.Run(ctx, call)
	if res.Error != nil && entry >= 0 {
		r.journal.discard(entry)
	}
	if call.Name == "bash" {
		recordBashResult(span, res)
	}
	return res
}

// bashExit matches the status bash appends to the output of a command that
// failed ("[exit status 1]", or "[exit status 1; ran in the bash sandbox…]").
var bashExit = regexp.MustCompile(`\[exit status (\d+)(?:; [^\]]*)?\]\s*$`)

// maxSpanOutput bounds the output tail a bash span keeps for a UI.
const (
	maxSpanOutputLines = 12
	maxSpanOutputBytes = 2000
)

// recordBashResult puts a bash call's outcome on its span: a non-zero exit
// marks it failed (it is data for the model, not a tool error, so the span
// showed a failing `go test` as a success), and the output's last lines are
// kept for a UI to show under the call.
func recordBashResult(span *Span, res ToolResult) {
	if res.Error != nil {
		return
	}
	out := res.Data
	if m := bashExit.FindStringSubmatchIndex(out); m != nil {
		span.SetError("exit status " + out[m[2]:m[3]])
		out = out[:m[0]]
	}
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) > maxSpanOutputLines {
		lines = lines[len(lines)-maxSpanOutputLines:]
	}
	tail := strings.Join(lines, "\n")
	if len(tail) > maxSpanOutputBytes {
		tail = strings.ToValidUTF8(tail[len(tail)-maxSpanOutputBytes:], "")
	}
	if strings.TrimSpace(tail) != "" && tail != "(no output)" {
		span.SetAttr("output", tail)
	}
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
// sandboxOf is the bash sandbox of the engine's own tool registry, nil for
// another registry (its bash, if any, is not this one).
func sandboxOf(reg ToolRegistry) *SandboxConfig {
	if t, ok := reg.(*Tools); ok {
		return t.sandbox
	}
	return nil
}

func (r *ToolRunner) bashPreApproved(call ToolCall) bool {
	if call.Name != "bash" || !r.allowlist.Restricts() {
		return false
	}
	cmd, _ := call.Input["command"].(string)
	return cmd != "" && r.allowlist.IsAllowed(cmd)
}

// checkPathFor is the path the permission check and the approval key use
// for a call's raw "path": the one the file tools will touch. They join every
// path, an absolute one included, under the project (SafeJoin); taking an
// absolute path as is checked and keyed a different file than the one
// written. A path that escapes is where the escape prompt says it lands.
func (r *ToolRunner) checkPathFor(raw string) string {
	if raw == "" || r.tools == nil {
		return raw
	}
	if full, err := SafeJoin(r.tools.Root(), raw); err == nil {
		return full
	}
	if abs, err := filepath.Abs(filepath.Join(r.tools.Root(), raw)); err == nil {
		return abs
	}
	return raw
}

// approvalSessionKey scopes "approve for session" to the tool's target: the
// absolute path for the built-in file tools, the exact command for bash, and
// for every other tool its path and all its arguments, so approving one URL
// does not approve the next. Only a call with no arguments at all is keyed by
// the bare tool name. The encoding (the tool, a null byte, then the target)
// matches the sandbox-escape approval, so the TUI reads the tool name the same
// way from both — a null byte cannot occur in a tool name.
func approvalSessionKey(tool, absPath string, input map[string]any) string {
	disc := absPath
	switch {
	case tool == "bash":
		if cmd, ok := input["command"].(string); ok {
			disc = cmd
		}
	case !pathKeyedTools[tool]:
		// Every other tool's approval covers its exact arguments, the path
		// among them: an upload approved for one URL must not send the same
		// file to another.
		args := make(map[string]any, len(input))
		for k, v := range input {
			if k != "_preview" { // the engine's own metadata, not an argument
				args[k] = v
			}
		}
		if len(args) > 0 {
			if b, err := json.Marshal(args); err == nil { // map keys come out sorted
				disc = absPath + "\x00" + string(b)
			}
		}
	}
	if disc == "" {
		return tool
	}
	return tool + "\x00" + disc
}

// pathKeyedTools are the built-in tools whose session approval covers a file,
// whatever the change: "edit this file for the session" is the point.
var pathKeyedTools = map[string]bool{"write_file": true, "edit_file": true, "delete_file": true}

func allowlistError() ToolResult {
	return ToolResult{Error: &ToolError{Code: ErrCodeNotAllowed, Message: "command is not in the bash allowlist. " +
		"Add it to .ouija/config.json (tools.bash.allowlist) or approve it interactively in the TUI."}}
}
