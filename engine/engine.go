package engine

import (
	"context"
	"log/slog"
	"os"
)

// SubmitTool is the tool through which a model hands in a structured answer
// (RunRequest.Schema). Providers without a native JSON mode force a call to it.
const SubmitTool = "submit_result"

const submitTool = SubmitTool

// ToolCallback is called before each tool execution (optional, for UI progress).
type ToolCallback func(name string, input map[string]any)

// TokenCallback is called for each text token yielded by a streaming provider
// call (optional, for real-time TUI display).
type TokenCallback func(text string)

// Engine runs the agent loop: the model proposes tool calls, the harness
// validates/executes them, until a final (optionally structured) answer or a
// budget ceiling. "Model proposes — harness executes."
type Engine struct {
	provider Provider
	agent    Agent // the loop strategy (ReAct, etc.)
	cfg      *Config
	tools    *Tools
	budget   *tracker
	tracer   *Tracer
	logger   *slog.Logger
	tokenCb  TokenCallback // streaming token callback (e.g. TUI)

	// Components — each owns a single responsibility.
	doom      *DoomDetector  // doom-loop detection
	allowlist *BashAllowlist // bash command allowlist
	runner    *ToolRunner    // tool gating + execution pipeline

	// gate is the permission/approval wiring shared with ToolRunner: both
	// hold the same pointer, so setters can never leave the runner stale.
	gate *gateState
}

// EngineConfig groups the dependencies needed to create an Engine.
type EngineConfig struct {
	Provider Provider
	Config   *Config
	Tracer   *Tracer
}

// New wires a provider, config, tracer and the sandboxed toolset together.
func New(cfg EngineConfig) *Engine {
	gate := &gateState{}
	// Apply the file's permission section up front so headless entrypoints
	// (CLI task, MCP) honour it too — previously only the TUI wired it, and
	// everything else silently fell back to the risk-tier default.
	if cfg.Config != nil && cfg.Config.Permission != nil {
		gate.permConfig = cfg.Config.Permission.ToEngine()
	}
	e := &Engine{
		provider: cfg.Provider,
		cfg:      cfg.Config,
		tools:    NewTools(cfg.Config.ProjectRoot),
		budget: newTracker(
			cfg.Config.Budget.MaxTokens, cfg.Config.Budget.MaxSteps, cfg.Config.MaxUSD(),
			cfg.Config.CostPer1MInput, cfg.Config.CostPer1MOutput,
		),
		tracer:    cfg.Tracer,
		logger:    newDefaultLogger(),
		doom:      &DoomDetector{},
		allowlist: NewBashAllowlist(nil),
		gate:      gate,
	}
	e.runner = NewToolRunner(ToolRunnerConfig{
		Tools:     e.tools,
		Gate:      e.gate,
		Doom:      e.doom,
		Allowlist: e.allowlist,
		Tracer:    e.tracer,
	})
	e.tools.escapeApprover = func(ctx context.Context, toolName, path string) bool {
		return e.requestEscapeApproval(ctx, toolName, path)
	}
	// Logger and callbacks are shared with the agent AFTER they are set
	// via SetLogger/SetTokenCallback, so ReAct gets a pointer-to-logger
	// that can be swapped at runtime.
	e.agent = NewReAct(ReActConfig{
		Provider: e.provider,
		ToolReg:  e.tools,
		Config:   e.cfg,
		Logger: func() *slog.Logger {
			if e.logger != nil {
				return e.logger
			}
			return newDefaultLogger()
		}(),
		TokenCb: e.tokenCb,
		Tracer:  e.tracer,
		Budget:  e.budget,
		RunTool: e.runner.Run,
	})
	if cfg.Config.Tools != nil && cfg.Config.Tools.Bash != nil && len(cfg.Config.Tools.Bash.Allowlist) > 0 {
		e.allowlist.Set(cfg.Config.Tools.Bash.Allowlist)
	}
	return e
}

// defaultLogLevel is a runtime-settable log level, initialised from
// OUIJA_LOG_LEVEL (default: info). Using a LevelVar allows the level to be
// changed at runtime (e.g. via a future /debug command in chat).
var defaultLogLevel slog.LevelVar

func init() {
	lvl := slog.LevelInfo
	if s := os.Getenv("OUIJA_LOG_LEVEL"); s != "" {
		var parsed slog.Level
		if err := parsed.UnmarshalText([]byte(s)); err == nil {
			lvl = parsed
		}
	}
	defaultLogLevel.Set(lvl)
}

// SetLogLevel changes the log level for all loggers created via newDefaultLogger
// (which includes both engine.defaultLogLevel and any loggers that share the
// same LevelVar pointer). Safe to call at any time — the change is visible
// immediately to all default loggers.
func SetLogLevel(lvl slog.Level) { defaultLogLevel.Set(lvl) }

// LogLevel returns the current default log level.
func LogLevel() slog.Level { return defaultLogLevel.Level() }

// newDefaultLogger creates a slog.Logger that writes to stderr with the
// standard `[ouija]`-like format.
func newDefaultLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level: &defaultLogLevel,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Strip timestamp and source — stderr context is enough.
			if a.Key == slog.TimeKey || a.Key == slog.SourceKey {
				return slog.Attr{}
			}
			return a
		},
	}))
}

// Logger returns the engine's logger. Useful for wiring a component that
// shares the same log level / output.
func (e *Engine) Logger() *slog.Logger { return e.logger }

// SetLogger replaces the default logger. Useful to silence output (pass a
// logger that writes to io.Discard) or to inject a structured handler.
func (e *Engine) SetLogger(l *slog.Logger) {
	e.logger = l
	if r, ok := e.agent.(*ReAct); ok {
		r.logger = l
	}
}

// SetToolCallback registers a function called before each tool execution.
// The callback receives the tool name and its input map. Intended for UI
// progress reporting (e.g. showing "read_file: foo.php" in a chat TUI).
func (e *Engine) SetToolCallback(fn ToolCallback) { e.gate.toolCb = fn }

// SetTokenCallback registers a function called for each text token yielded
// by a streaming provider call. Intended for real-time token display in a TUI.
func (e *Engine) SetTokenCallback(fn TokenCallback) {
	e.tokenCb = fn
	if r, ok := e.agent.(*ReAct); ok {
		r.tokenCb = fn
	}
}

// SetModel changes the active model name. The loop reads the model on every
// provider call, so a run already in progress switches on its next turn.
func (e *Engine) SetModel(name string) { e.cfg.Model = name }

// SetProvider replaces the provider instance. The caller is responsible
// for creating the new Provider (e.g. via app.BuildProvider or
// providers.NewOpenAI()). The old Provider is discarded.
// Also updates the agent's provider to keep the loop in sync.
func (e *Engine) SetProvider(p Provider) {
	e.provider = p
	e.cfg.Provider = p.Name()
	if r, ok := e.agent.(*ReAct); ok {
		r.provider = p
	}
}

// SetApprovalManager sets the approval manager used for user-prompted
// tool approval. When nil (default), Ask-level tools are denied.
func (e *Engine) SetApprovalManager(am *ApprovalManager) { e.gate.approvalMgr = am }

// ResetApprovals clears the session cache in the approval manager, if set.
func (e *Engine) ResetApprovals() {
	if e.gate.approvalMgr != nil {
		e.gate.approvalMgr.Reset()
	}
}

// requestEscapeApproval is called by Tools.Run when a tool's path argument
// escapes the sandbox. It delegates to the approval manager with an
// "escape:<toolName>" session key so the TUI renderer can identify it.
// The escaped absolute path is embedded in the session key (after a null
// byte) so that "Session" approval is per-path, not per-tool.
func (e *Engine) requestEscapeApproval(ctx context.Context, toolName, escapedAbs string) bool {
	if e.gate.approvalMgr == nil {
		return false
	}
	// null byte can never appear in a file path, making it a clean separator
	sessionKey := "escape:" + toolName + "\x00" + escapedAbs
	result, err := e.gate.approvalMgr.Request(ctx, sessionKey, map[string]any{
		"path": escapedAbs,
	})
	return err == nil && result.Allowed
}

// SetPermissionConfig sets the per-tool permission level mapping.
// When nil (default), read-only tools are allowed and write tools are denied.
func (e *Engine) SetPermissionConfig(cfg *PermissionConfig) { e.gate.permConfig = cfg }

// PermissionConfig returns the current permission config (may be nil).
func (e *Engine) PermissionConfig() *PermissionConfig { return e.gate.permConfig }

// EnsurePermissionConfig creates a default config if none exists, for runtime changes.
func (e *Engine) EnsurePermissionConfig() *PermissionConfig {
	if e.gate.permConfig == nil {
		e.gate.permConfig = NewPermissionConfig(Ask)
	}
	return e.gate.permConfig
}

// Close releases the engine's resources (trace file). Safe to call
// multiple times — subsequent calls are no-ops.
func (e *Engine) Close() { e.tracer.Reset() }

// Config exposes the active configuration to totems.
func (e *Engine) Config() *Config { return e.cfg }

// Tracer returns the engine's tracer, nil when tracing is disabled.
func (e *Engine) Tracer() *Tracer { return e.tracer }

// Spent returns cumulative (tokens, steps, usd) across all agents in this run.
func (e *Engine) Spent() (tokens, steps int, usd float64) { return e.budget.spent() }

// ── Bash allowlist (delegates to component) ─────────────────────────

// SetBashAllowlist replaces the bash command allowlist with the given patterns.
// Syncs the config so SaveConfigAt persists the correct data.
func (e *Engine) SetBashAllowlist(cmds []string) {
	e.allowlist.Set(cmds)
	e.syncAllowlistConfig()
}

// AddAllowedCommand adds a command pattern to the allowlist at runtime.
// It also updates the config struct so the allowlist persists on the next
// Config.Save call. Call SaveConfigAt to persist to disk.
func (e *Engine) AddAllowedCommand(cmd string) {
	e.allowlist.Add(cmd)
	e.syncAllowlistConfig()
}

// syncAllowlistConfig copies the current allowlist patterns into the config
// struct so that a subsequent SaveConfigAt persists the up-to-date list.
func (e *Engine) syncAllowlistConfig() {
	patterns := e.allowlist.Patterns()
	if e.cfg.Tools == nil {
		e.cfg.Tools = &ToolsConfig{}
	}
	if e.cfg.Tools.Bash == nil {
		e.cfg.Tools.Bash = &BashConfig{}
	}
	e.cfg.Tools.Bash.Allowlist = patterns
}

// SaveConfigAt persists the current config to the given file path.
// It temporarily sets ProjectRoot to "." so the saved JSON does not contain
// an absolute project path (the same pattern used by /model and /provider).
func (e *Engine) SaveConfigAt(path string) error {
	orig := e.cfg.ProjectRoot
	e.cfg.ProjectRoot = "."
	defer func() { e.cfg.ProjectRoot = orig }()
	return e.cfg.Save(path)
}

// IsCmdAllowed returns true if the command is in the allowlist.
func (e *Engine) IsCmdAllowed(cmd string) bool { return e.allowlist.IsAllowed(cmd) }

// RunRequest is one agent task.
type RunRequest struct {
	Label      string         // shown in trace/progress
	System     string         // stable, cacheable prefix
	Prompt     string         // variable per-call instruction
	Messages   []Message      // optional: existing conversation to continue
	Schema     map[string]any // when set, the answer is the JSON of a submit_result call
	UseTools   bool           // expose the default file tools
	ExtraTools []CustomTool   // scoped custom tools (def + impl, no global registration)
	MaxTokens  int
}

// runAgent is the single point through which every agent turn passes: it
// gives the run a fresh doom-loop detector and delegates to the loop strategy.
// The detector is run-scoped (carried in ctx), not engine-wide: totems run
// several agents concurrently on one Engine.
func (e *Engine) runAgent(ctx context.Context, req RunRequest, msgs []Message) (*RunResult, []Message, error) {
	ctx = withDoomDetector(ctx, &DoomDetector{})
	return e.agent.Run(ctx, req, msgs)
}

// Run executes the agent loop and returns a RunResult (final text + tool metadata).
// When Schema is set, Text is the JSON of a submit_result call.
func (e *Engine) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	msgs := []Message{{Role: RoleUser, Content: req.Prompt}}
	res, _, err := e.runAgent(ctx, req, msgs)
	return res, err
}

// Chat is like Run but continues an existing conversation.
// req.Messages contains prior turns; the new Prompt is appended as the user turn.
// Returns the final text plus the full updated conversation for the next round.
func (e *Engine) Chat(ctx context.Context, req RunRequest) (string, []Message, error) {
	msgs := req.Messages
	if msgs == nil {
		msgs = []Message{}
	}
	msgs = append(msgs, Message{Role: RoleUser, Content: req.Prompt})
	res, msgs, err := e.runAgent(ctx, req, msgs)
	if err != nil {
		return "", msgs, err
	}
	// Compress tool-heavy turns so future calls don't re-read entire file
	// contents. The model's synthesized answer is preserved; intermediate
	// tool calls + file results are dropped to keep context lean.
	msgs = compressConversation(msgs)
	return res.Text, msgs, nil
}
