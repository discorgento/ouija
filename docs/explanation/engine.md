🔮 ouija docs · [Index](../index.md)

---

# Engine

> How the shared engine works: loop, streaming, budget, tools, providers.
> Audience: explanation

## Key design principle

The **engine knows nothing** about tasks, documents or specific frameworks. It
knows only providers, tools, messages and budgets. Each totem (an agent
workflow such as `task` or `doc`) builds its own flow on top, so the engine is
written once and every totem reuses it.

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

Everything lives flat in `package engine`; the only sub-package is
`providers/`. There are no cyclic imports, and the package is about 5k lines.

## Agent interface + ReAct loop

```go
type Agent interface {
    Run(ctx context.Context, env RunEnv, req RunRequest, msgs []Message) (*RunResult, []Message, error)
}
```

An `Agent` holds no per-run state. Everything a run needs arrives in a
`RunEnv`, which the Engine snapshots when the run starts: provider, model,
config, tool definitions, the gated `RunTool`, the shared `Budget`, tracer,
logger and token callback. One Agent can therefore serve concurrent runs (such
as a totem's parallel passes), and the Engine's setters (`SetModel`,
`SetProvider`, `SetLogger`, …) are safe to call while runs are in progress: a
change applies from the next run on. To use your own loop strategy, pass it in
`engine.EngineConfig{Agent: …}`.

`ReAct`, the default, is the built-in implementation of the standard ReAct
loop: the model plans, acts and observes, then repeats. The model proposes tool
calls; the harness validates and gates them (permissions, allowlist, doom
detection), executes them, and feeds structured observations back. Each
iteration calls `compact()`, which trims old tool-result content and keeps a
sliding window of at most 60 messages, so the context stops growing.

## Modes

- **`Run(ctx, RunRequest)`**: single-shot execution, used by totems. It sets a
  fresh system prompt, runs the agent until it calls `submit_result`, and
  returns the output.
- **`Chat(ctx, RunRequest)`**: conversational mode, used by the TUI. It keeps
  history across turns within the sliding window and expects no final result.

Both call `runAgent`, the single point every turn passes through. It gives the
run a fresh doom-loop detector scoped to that run, then hands off to the loop
strategy (`Agent.Run`).

When `RunRequest.Schema` is set, ouija offers the model a `submit_result` tool
whose input schema is the run's schema. If the model stops calling tools
without submitting, the loop tries three fallbacks in order. It takes JSON
already present in the reply text; failing that, it makes one native
structured-output call (for providers whose `Capabilities().StructuredOutput`
is true); failing that, it nudges the model back to `submit_result`, up to
`maxNudges` times.

## The gate: one place where a tool call is allowed or refused

`ToolRunner.Run` is the **only** path a tool call can take. It applies, in
order:

1. **Static bash gates**: the denylist (with semantic analysis for flag-order
   variants such as `rm -fr /`, wrappers such as `sudo rm -fr /`, and raw
   device writes) and references to protected paths (`cat .env`, `~/.ssh/…`).
   These run *before* any prompt, because no "yes" can override a hard rule.
2. **Permission level**: `Deny` refuses, `Ask` prompts (unless the bash command
   is on the allowlist), `Allow` runs unattended.
3. **Bash allowlist**: pre-approves known commands so they skip the prompt and,
   under `Allow`, acts as the guard rail for unattended runs.
4. **Execution**: a built-in tool or a scoped `CustomTool`.

`Engine` and `ToolRunner` share the permission and approval wiring through one
`gateState` **pointer**, so a setter can never leave the runner with a stale
config.

Session approvals (the **Session** option in the prompt) are keyed by
**target**, not by tool name: `write_file\x00/abs/path`,
`bash\x00<exact command>`. Approving one path or command therefore never
silently approves another for the rest of the run. A plain "yes" runs the call
once; only **Session** adds a bash command to the allowlist and writes it back
to the config file.

[Security](security.md) explains the policy behind the gate, and
[Architecture → Tool gating](architecture.md#tool-gating-permissions) walks
through the code path step by step.

## ToolResult — structured tool feedback

Every tool returns a `ToolResult`, not a raw string:

```go
type ToolResult struct {
    Data  string     // text content for the model
    Error *ToolError // nil = success
}
```

Each error carries a `Code` (e.g. `NOT_ALLOWED`, `DOOM_LOOP`,
`PERMISSION_DENIED`), so the agent loop can treat each kind differently: never
retry a denied tool, always retry a timeout.

## Streaming

Every provider call streams tokens as they arrive, through an
`iter.Seq2[StreamEvent, error]`. The TUI renders text as it arrives, while the
engine collects the full response to run tools. The `CollectStream` helper
reassembles stream events for callers that do not stream. A configurable
`streamTimeout` (default 120s) stops a call when the provider goes silent.

## Tool sandbox

Every file tool stays inside the project root, both lexically and physically.
`SafeJoin` blocks `../`, and `resolveSymlinkEscape` rejects symlinks that point
outside. It walks the path one component at a time, so it detects a symlink
even when the target file does not exist yet, which closes the TOCTOU
(time-of-check to time-of-use) window. The `delete_file` tool also resolves
symlinks before it calls `os.RemoveAll`.

## Sliding window (context management)

Conversation history never grows without bound: `compact()` runs at the start
of every loop iteration.

| Constant | Value | Effect |
|---|---|---|
| `compactMaxMsgs` | 60 | Drops the oldest messages beyond this limit |
| `compactKeepRecent` | 6 | Keeps the last 6 tool results verbatim |
| `compactTrimAt` | 800 chars | Truncates older tool-result content |

The window never opens on a tool result whose tool call was dropped, because
providers reject an orphan tool result. When the window opens on an assistant
turn, the first user message (the task) stays pinned in front.

This keeps cost predictable and latency low in long chat sessions (15-30
turns).

## Budget enforcement

Three independent ceilings stop a run gracefully:

| Field | What it bounds |
|---|---|
| `budget.maxTokens` | Total tokens across all turns |
| `budget.maxSteps` | Number of tool-call cycles |
| `budget.maxUSD` | Estimated cost (requires `costPer1MInput`/`costPer1MOutput`) |

The tracker also rejects negative token counts from buggy providers before it
sums them. Each run prints its stats to stderr:

```
[ouija] anthropic · claude-opus-5-5 · 184320 tokens · 14 steps · $1.05
```

The ` · $…` suffix appears only when cost pricing (`costPer1MInput`/`Output`)
is set; without it, the line ends at the step count.

## Token efficiency

- **Prompt caching** (Anthropic) of the stable `system` block and the tool
  definitions, which repeat in every pass.
- **Compaction and a sliding window** over the history: old tool-result content
  is cut to 800 chars, and messages beyond 60 are dropped. This keeps cost
  predictable in long sessions.
- **Budget** ceilings (tokens, steps, USD) that stop the run gracefully,
  without downgrading the model or cutting passes, so the analysis keeps its
  full strength.

### A note on cost

`ouija` calls the model API directly with your key, which means **metered,
pay-per-token** billing. The per-token price matches any other client; what
differs is the bill it lands on. If your editor's agent runs on a flat
**subscription**, running ouija with an API key is extra spend. Use the
`ollama` provider for a local run at no cost, or set `maxUSD` (with the cost
fields) as a hard ceiling.

## Structured logging (`slog`)

The engine writes structured logs to stderr with `log/slog` (Go 1.21+). Set
`OUIJA_LOG_LEVEL` to control verbosity (see
[Environment variables](../reference/environment.md)):

```
OUIJA_LOG_LEVEL=debug ouija task "fix sorting bug"
```

Levels: `debug`, `info` (default), `warn`, `error`. The TUI silences engine
logging with a handler that writes to `io.Discard`.

## Modern Go features used

| Feature | Where |
|---|---|
| `iter.Seq2[T, error]` | Provider streaming (every Chat call) |
| `range-over-int` | `for i := range len(msgs)` instead of `i < len` |
| `clear()` | Zeroing slices after compaction and the sliding window |
| `log/slog` | Structured logging everywhere |
| `errors.Join` | Config validation collects every error |
| `slices` package | `slices.Contains` in totem reconcile |
| `sync.Map` / `sync.Mutex` / `sync.RWMutex` | Thread safety for allowlist, doom detector, budget |
| `context.AfterFunc` | Streaming cleanup (no race window) |
| `context.WithTimeout` | Streaming timeout (default 120s) |

## Providers

Every LLM backend implements the `Provider` interface: `Name()`,
`Capabilities()` and `Chat(ctx, req) iter.Seq2[StreamEvent, error]`.

| Provider | Notes |
|---|---|
| `anthropic` | Messages API + SSE (server-sent events) streaming + prompt caching |
| `openai` | `/chat/completions` + SSE + `stream_options: include_usage` |
| `openrouter` | OpenAI-compatible shim, multi-model gateway |
| `opencode` | OpenCode Zen gateway (`/chat/completions`); its free tier works only inside the OpenCode app (403 `FreeTierError` elsewhere) |
| `ollama` | OpenAI-compatible shim, local, free |

All providers share `providers/http.go`, which handles retry with backoff on
429/5xx responses and parses SSE. It uses `time.NewTimer` (no timer leak),
`context.AfterFunc` after bufio init (no race window), and the streaming
timeout (default 120s). The **provider registry** (`providers.Build(name,
key)`), a stateless map of constructors, creates provider instances.
`app.DiscoverConfig` reads the active provider's API key from the config files
(global first, then the project override) and passes it to the constructor.

[Adding a provider](../how-to/add-provider.md) walks through adding one in four
steps.

## Known limitations (honest debts)

- The **sliding window** drops old messages entirely; it does not summarize
  them. That is fine for chat. If a very long conversation needs all its
  context, raise `compactMaxMsgs`, or run `/clear` and paste the essential
  context back.
- **maxUSD** is enforced only when `costPer1MInput/Output` are set. The values
  in the presets are only examples; set them to your real prices.
- **Doom-loop detection** matches the exact input fingerprint, so two calls
  with the same meaning but different text do not trigger it.
- **MCP resources** give read-only file access through the sandbox: no
  directory writes, no git operations, no project scaffolding.

---

🔮 ouija docs — [Index](../index.md) · [Architecture](architecture.md)
