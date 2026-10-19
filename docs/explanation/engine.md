🔮 ouija docs · [Index](../index.md)

---

# Engine

> How the shared engine works: loop, streaming, budget, tools, providers.
> Audience: explanation

## Key design principle

The **engine knows nothing** about tasks, documents, or specific frameworks. It
only knows providers, tools, messages, and budgets. Every totem defines its own
flow on top — the engine is written once, reused by everything.

## Components

```
Engine (coordinator)
  ├── Agent (interface) → ReAct (loop strategy)
  ├── ToolRegistry (interface) → Tools (concrete)
  ├── ToolRunner (gating pipeline)
  │     ├── DoomDetector
  │     ├── PermissionConfig + ApprovalManager
  │     ├── BashAllowlist
  │     └── Sandbox (SafeJoin, symlink detection)
  ├── Tracker (budget)
  └── Tracer (in-memory span tree → Perfetto JSON export)
```

All flat in `package engine` — the only sub-package is `providers/`; no cyclic
imports, ~5k lines.

## Agent interface + ReAct loop

```go
type Agent interface {
    Run(ctx context.Context, env RunEnv, req RunRequest, msgs []Message) (*RunResult, []Message, error)
}
```

An `Agent` holds no per-run state. Everything a run needs — provider, model,
config, tool definitions, the gated `RunTool`, the shared `Budget`, tracer,
logger and token callback — arrives in a `RunEnv` the Engine snapshots when the
run starts. So one Agent serves concurrent runs (a totem's parallel passes),
and the Engine's setters (`SetModel`, `SetProvider`, `SetLogger`, …) are safe
while runs are in progress: they apply from the next run on. Pass your own loop
strategy in `engine.EngineConfig{Agent: …}`.

`ReAct` (the default) is the built-in implementation: a standard plan→act→observe loop.
The model proposes tool calls; the harness validates, gates (permissions,
allowlist, doom detection), executes, and injects structured observations back.
Each loop iteration calls `compact()` which trims old tool-result content and
applies a sliding window (max 60 messages) to bound context growth.

## Modes

- **`Run(ctx, RunRequest)`** — single-shot execution. Used by totems. Sets a
  fresh system prompt, runs the agent until `submit_result`, returns the output.
- **`Chat(ctx, RunRequest)`** — conversational mode. Used by the TUI. Preserves
  history across turns with sliding window, no final result expectation.

Both call `runAgent`, the single point every turn passes through: it gives the
run a fresh, run-scoped doom-loop detector and delegates to the loop strategy
(`Agent.Run`).

When `RunRequest.Schema` is set, the model is offered `submit_result` (its input
schema is the run's schema). If it stops calling tools without submitting, the
loop takes JSON already in the text, else makes one native structured-output
call (providers whose `Capabilities().StructuredOutput` is true), else nudges it
back to `submit_result`, up to `maxNudges`.

## The gate: one place where a tool call is allowed or refused

`ToolRunner.Run` is the **only** path a tool call can take. It applies, in
order:

1. **Static bash gates** — denylist (with semantic analysis for flag-order
   variants such as `rm -fr /`, wrappers such as `sudo rm -fr /`, and raw
   device writes) and protected-path references (`cat .env`, `~/.ssh/…`).
   These run *before* any prompt: a hard rule is never something a "yes"
   can override.
2. **Permission level** — `Deny` refuses, `Ask` prompts (unless the bash
   command sits on the allowlist), `Allow` runs unattended.
3. **Bash allowlist** — pre-approves known commands (no prompt) and, under
   `Allow`, acts as the guard rail for unattended runs.
4. **Execution** — built-in tool or scoped `CustomTool`.

The permission/approval wiring lives in one `gateState` **shared by pointer**
between `Engine` and `ToolRunner`, so a setter can never leave the runner with
a stale config.

Session approvals ("Session" in the prompt) are keyed by **target**, not by
tool name: `write_file\x00/abs/path`, `bash\x00<exact command>`. Approving one
path or command therefore never silently approves another for the rest of the
run. A plain "yes" runs the call once; only "Session" persists a bash command
to the allowlist and writes it back to the config file.

The policy behind the gate is explained in [Security](security.md); the code
path step by step in [Architecture → Tool gating](architecture.md#tool-gating-permissions).

## ToolResult — structured tool feedback

Every tool returns `ToolResult` (not a raw string):

```go
type ToolResult struct {
    Data     string     // text content for the model
    Mime     string     // optional MIME type hint
    Truncated bool      // content was truncated
    Error    *ToolError // nil = success
}
```

Errors have a `Code` (e.g. `NOT_ALLOWED`, `DOOM_LOOP`, `PERMISSION_DENIED`) so
the agent loop can react differently (never retry a denied tool, always retry a
timeout).

## Streaming

Every provider call streams tokens in real time via
`iter.Seq2[StreamEvent, error]`. The TUI renders text as it arrives; the engine
collects the full response internally for tool execution. The `CollectStream`
helper reassembles stream events for non-streaming callers. A configurable
`streamTimeout` (default 120s) prevents hanging on silent providers.

## Tool sandbox

Every file tool is confined to the project root — both lexically (`SafeJoin`
blocks `../`) and physically (symlinks pointing outside are rejected via
`resolveSymlinkEscape`, which walks the path component by component to detect
symlinks even when the target file doesn't exist yet — closing the TOCTOU
window). The `delete_file` tool also resolves symlinks before `os.RemoveAll`.

## Sliding window (context management)

Conversation history never grows unbounded. `compact()` is called at the start
of every loop iteration:

| Constant | Value | Effect |
|---|---|---|
| `compactMaxMsgs` | 60 | Drops oldest messages beyond this limit |
| `compactKeepRecent` | 6 | Preserves last 6 tool results verbatim |
| `compactTrimAt` | 800 chars | Truncates older tool-result content |

The window never opens on a tool result whose tool call was dropped (providers
reject an orphan tool result), and when it opens on an assistant turn the first
user message (the task) stays pinned in front.

This keeps cost predictable and latency low on long chat sessions (15-30 turns).

## Budget enforcement

Three independent ceilings with graceful stop:

| Field | What it bounds |
|---|---|
| `budget.maxTokens` | Total tokens across all turns |
| `budget.maxSteps` | Number of tool-call cycles |
| `budget.maxUSD` | Estimated cost (requires `costPer1MInput`/`costPer1MOutput`) |

The tracker also rejects negative token counts from buggy providers (sanity
check before summing). Per-run stats go to stderr:

```
[ouija] anthropic · claude-opus-5-5 · 184320 tokens · 14 steps · $1.05
```

The ` · $…` suffix appears only when cost pricing (`costPer1MInput`/`Output`) is
set — without it the line ends at the step count.

## Token efficiency

- **Prompt caching** of the stable `system` block and tool definitions
  (both repeat across every pass — Anthropic).
- **Compaction + sliding window** of conversation history: old tool-result
  content is truncated to 800 chars, and messages beyond 60 are dropped
  entirely. Keeps cost predictable on long sessions.
- **Budget** ceilings (tokens / steps / USD) with a graceful stop (no model
  downgrade and no pass reduction — the analysis stays at full strength).

### A note on cost

`ouija` calls the model API directly with your key — that is **metered,
pay-per-token** billing. The per-token price is identical to any other client;
what differs is the *bucket*: if your editor agent runs on a flat **subscription**,
running ouija via API key is additional spend. Use the `ollama` provider for a
zero-cost local run, or set `maxUSD` (with the cost fields) as a hard ceiling.

## Structured logging (`slog`)

The engine uses `log/slog` (Go 1.21+) for structured logging to stderr. Set
`OUIJA_LOG_LEVEL` to control verbosity (see
[Environment variables](../reference/environment.md)):

```
OUIJA_LOG_LEVEL=debug ouija task "fix sorting bug"
```

Levels: `debug`, `info` (default), `warn`, `error`. The TUI silences engine
logging by discarding logs via a handler on `io.Discard`.

## Modern Go features used

| Feature | Where |
|---|---|
| `iter.Seq2[T, error]` | Provider streaming (every Chat call) |
| `range-over-int` | `for i := range len(msgs)` — no more `i < len` |
| `clear()` | Slice zeroing after compaction/sliding window |
| `log/slog` | Structured logging everywhere |
| `errors.Join` | Config validation accumulates all errors |
| `slices` package | `slices.Contains` in totem reconcile |
| `sync.Map` / `sync.Mutex` / `sync.RWMutex` | Thread safety for allowlist, doom detector, budget |
| `context.AfterFunc` | Streaming cleanup (no race window) |
| `context.WithTimeout` | Streaming timeout (default 120s) |

## Providers

All LLM backends implement the `Provider` interface — `Name()`,
`Capabilities()` and `Chat(ctx, req) iter.Seq2[StreamEvent, error]`:

| Provider | Notes |
|---|---|
| `anthropic` | Messages API + SSE streaming + prompt caching |
| `openai` | `/chat/completions` + SSE + `stream_options: include_usage` |
| `openrouter` | OpenAI-compatible shim, multi-model gateway |
| `opencode` | OpenCode Zen gateway (`/chat/completions`); its free tier only works inside the OpenCode app (403 `FreeTierError` elsewhere) |
| `ollama` | OpenAI-compatible shim, local, free |

All share `providers/http.go` for retry/backoff on 429/5xx and SSE parsing — with
`time.NewTimer` (no timer leak), `context.AfterFunc` after bufio init (no race
window), and streaming timeout (default 120s). Provider instances are created via
the **provider registry** (`providers.Build(name, key)`), a stateless map of
constructors. `app.DiscoverConfig` resolves the active provider's API key from the
config files (global, then project override) and passes it to the constructor.

Adding one is a four-step walkthrough: [Adding a provider](../how-to/add-provider.md).

## Known limitations (honest debts)

- **Sliding window** drops old messages entirely (not summarization). Fine for
  chat; if a very long conversation needs all context, increase `compactMaxMsgs`
  or use `/clear` to reset and paste essential context back.
- **maxUSD** is only enforced when `costPer1MInput/Output` are set; the values in
  the presets are illustrative — set them to your real pricing.
- **Doom-loop detection** is input-fingerprint exact — two calls with semantically
  identical but textually different inputs won't trigger it.
- **MCP resources** are read-only file access through the sandbox — no directory
  write, no git operations, no project scaffolding.

---

🔮 ouija docs — [Index](../index.md) · [Architecture](architecture.md)
