package engine

import (
	"cmp"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"sync"
)

// SubmitTool is the tool through which a model hands in a structured answer
// (RunRequest.Schema) while it still has other tools; its input schema is the
// run's schema.
const SubmitTool = "submit_result"

// ToolCallback is called before each tool execution (optional, for UI progress).
type ToolCallback func(name string, input map[string]any)

// TokenCallback is called for each text token yielded by a streaming provider
// call (optional, for real-time TUI display).
type TokenCallback func(text string)

// Engine runs the agent loop: the model proposes tool calls, the harness
// validates/executes them, until a final (optionally structured) answer or a
// budget ceiling. "Model proposes — harness executes."
//
// An Engine is safe for concurrent use: totems run several agents on one
// Engine, and the setters may be called while a run is in progress (the chat
// switches model mid-session). A run works on a RunEnv snapshotted when it
// starts, so a setter applies from the next run on.
type Engine struct {
	agent  Agent // the loop strategy (ReAct by default); stateless
	tools  *Tools
	budget *tracker
	tracer *Tracer

	// mu guards the fields a setter may change while runs read them: the
	// provider, the logger, the token callback and cfg's Provider/Model/Tools.
	mu       sync.RWMutex
	provider Provider
	cfg      *Config
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
	// Agent is the loop strategy; nil means ReAct.
	Agent Agent
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
	e.agent = cmp.Or[Agent](cfg.Agent, NewReAct())
	if cfg.Config.Tools != nil && cfg.Config.Tools.Bash != nil {
		if len(cfg.Config.Tools.Bash.Allowlist) > 0 {
			e.allowlist.Set(cfg.Config.Tools.Bash.Allowlist)
		}
		e.tools.sandbox = cfg.Config.Tools.Bash.Sandbox
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
func (e *Engine) Logger() *slog.Logger {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.logger
}

// SetLogger replaces the default logger. Useful to silence output (pass a
// logger that writes to io.Discard) or to inject a structured handler.
// A nil logger restores the default.
func (e *Engine) SetLogger(l *slog.Logger) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.logger = cmp.Or(l, newDefaultLogger())
}

// SetToolCallback registers a function called before each tool execution.
// The callback receives the tool name and its input map. Intended for UI
// progress reporting (e.g. showing "read_file: foo.php" in a chat TUI).
func (e *Engine) SetToolCallback(fn ToolCallback) {
	e.gate.update(func(g *gateState) { g.toolCb = fn })
}

// SetTokenCallback registers a function called for each text token yielded
// by a streaming provider call. Intended for real-time token display in a TUI.
func (e *Engine) SetTokenCallback(fn TokenCallback) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.tokenCb = fn
}

// SetModel changes the active model name, from the next run on.
func (e *Engine) SetModel(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.cfg.Model = name
}

// SetProvider replaces the provider instance, from the next run on. The
// caller is responsible for creating the new Provider (e.g. via
// app.BuildProvider). The old Provider is discarded.
func (e *Engine) SetProvider(p Provider) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.provider = p
	e.cfg.Provider = p.Name()
}

// SetApprovalManager sets the approval manager used for user-prompted
// tool approval. When nil (default), Ask-level tools are denied.
func (e *Engine) SetApprovalManager(am *ApprovalManager) {
	e.gate.update(func(g *gateState) { g.approvalMgr = am })
}

// ResetApprovals clears the session cache in the approval manager, if set.
func (e *Engine) ResetApprovals() {
	if am := e.gate.snapshot().approvalMgr; am != nil {
		am.Reset()
	}
}

// requestEscapeApproval is called by Tools.Run when a tool's path argument
// escapes the sandbox. It delegates to the approval manager with an
// "escape:<toolName>" session key so the TUI renderer can identify it.
// The escaped absolute path is embedded in the session key (after a null
// byte) so that "Session" approval is per-path, not per-tool.
func (e *Engine) requestEscapeApproval(ctx context.Context, toolName, escapedAbs string) bool {
	am := e.gate.snapshot().approvalMgr
	if am == nil {
		return false
	}
	// null byte can never appear in a file path, making it a clean separator
	sessionKey := "escape:" + toolName + "\x00" + escapedAbs
	result, err := am.Request(ctx, sessionKey, map[string]any{
		"path": escapedAbs,
	})
	return err == nil && result.Allowed
}

// SetPermissionConfig sets the per-tool permission level mapping.
// When nil (default), read-only tools are allowed and write tools are denied.
// Change the levels of a config already in use with its SetTool/SetDefault.
func (e *Engine) SetPermissionConfig(cfg *PermissionConfig) {
	e.gate.update(func(g *gateState) { g.permConfig = cfg })
}

// PermissionConfig returns the current permission config (may be nil).
func (e *Engine) PermissionConfig() *PermissionConfig { return e.gate.snapshot().permConfig }

// EnsurePermissionConfig creates a default config if none exists, for runtime changes.
func (e *Engine) EnsurePermissionConfig() *PermissionConfig {
	var pc *PermissionConfig
	e.gate.update(func(g *gateState) {
		if g.permConfig == nil {
			g.permConfig = NewPermissionConfig(Ask)
		}
		pc = g.permConfig
	})
	return pc
}

// Close releases the engine's resources (trace file). Safe to call
// multiple times — subsequent calls are no-ops.
func (e *Engine) Close() { e.tracer.Reset() }

// Config exposes the active configuration to totems. Treat it as read-only:
// change the model, provider or allowlist through the Engine's setters.
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
	e.mu.Lock()
	defer e.mu.Unlock()
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
// The API key written is the one the file itself already declares, never the
// resolved cfg.APIKey: that one usually comes from the global store, and
// copying it into a project file (often committed) would leak it.
func (e *Engine) SaveConfigAt(path string) error {
	e.mu.RLock()
	c := *e.cfg // runs read the shared config without the lock: never edit it
	e.mu.RUnlock()
	c.ProjectRoot = "."
	c.APIKey = apiKeyOnDisk(path)
	return c.Save(path)
}

// apiKeyOnDisk returns the apiKey a config file declares, "" when the file is
// missing, unreadable or has none.
func apiKeyOnDisk(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var f struct {
		APIKey string `json:"apiKey"`
	}
	if json.Unmarshal(b, &f) != nil {
		return ""
	}
	return f.APIKey
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
	return e.agent.Run(ctx, e.env(), req, msgs)
}

// env snapshots what one run needs, so setters called during the run cannot
// change it underneath.
func (e *Engine) env() RunEnv {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return RunEnv{
		Provider: e.provider,
		Model:    e.cfg.Model,
		Config:   e.cfg,
		Tools:    e.tools,
		RunTool:  e.runner.Run,
		Budget:   e.budget,
		Tracer:   e.tracer,
		Logger:   e.logger,
		OnToken:  e.tokenCb,
		MayRun:   e.runner.MayRun,
	}
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
