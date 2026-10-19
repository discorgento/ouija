🔮 ouija docs · [Index](../index.md)

---

# Architecture

> High-level design of the ouija project. Audience: developer

## Layers

```
cmd/              → Entry points (ouija CLI + `ouija mcp`, ouija-mcp standalone MCP server)
  └─ imports _ "github.com/discorgento/ouija/totems"  → auto-registered via go generate
internal/
  ├─ app/         → Orchestration: config discovery, engine setup, totem lifecycle
  ├─ engine/      → Core agent loop, providers, tools, budget, tracing
  ├─ mcp/         → MCP server (stdio) + harness registration (install/status)
  ├─ registry/    → Totem registration and lookup
  ├─ chat/        → Bubble Tea TUI (interactive chat)
  ├─ commands/    → CLI commands (setup, init, config, learn, generate)
  ├─ ouijaconfig/ → XDG-compliant config management
  ├─ knowledge/   → Project knowledge storage
  ├─ schema/      → JSON Schema generation helpers
  ├─ models/      → Provider/model catalog (in memory, no file I/O)
  ├─ art/         → Shared figlet banner/logo (CLI + TUI)
  ├─ version/     → Build version (injectable via -ldflags -X)
  └─ tools/genimports → Build helper: auto-generates totem import list
totems/           → Registered totems (task, doc, sticker)
```

## Data flow

```
CLI args → app.DiscoverConfig()
           → merges global + project config
           → app.ValidateConfig()
           → app.BuildProvider() → creates provider from registry
           → engine.New() → wires provider, config, budget, tracer, logger
           → totem.Init(ctx, eng, cfg)
           → totem.Run(ctx, eng, args)
               → eng.Run() or eng.Chat()
                   → runAgent() → doom.Reset() → agent.Run()
                       → model proposes tool call
                       → harness validates + executes
                       → repeat until a final answer (text or submit_result)
           → totem.Cleanup()
           → render result (Renderer or JSON)
```

## Engine pipeline

```
Engine.Run(ctx, req) / Engine.Chat(ctx, req)
  └─ runAgent() → doom.Reset() → agent.Run()
       └─ (*ReAct).Run(ctx, req, msgs)   ← the loop strategy (Agent)
            ├─ streamChat() → provider.Chat() → iter.Seq2[StreamEvent, error]
            ├─ tool execution (gated by ToolRunner, sandboxed)
            ├─ budget tracking
            ├─ trace events
            └─ repeat until a final answer or budget ceiling
```

Every `Engine.Run` / `Engine.Chat` call goes through `runAgent` once, which
resets doom-loop state and delegates to the loop strategy (`ReAct`); the turns
themselves loop inside `ReAct.Run`. The permission/approval wiring is a single
`gateState` value shared **by pointer** between `Engine` and its `ToolRunner`, so
the setters (`SetPermissionConfig`, `SetApprovalManager`, `SetToolCallback`) can
never leave the runner holding stale state.

## Tool gating (permissions)

Every tool call goes through `ToolRunner.Run` (`internal/engine/toolrunner.go`),
which applies its checks in a fixed order:

1. **UI progress callback** — `ToolCallback` fires before anything else.
2. **Doom-loop detection** — the same tool + input three turns in a row is
   blocked.
3. **Static bash gates** — for `bash` only, and *before any prompt*:
   - the hardcoded **denylist** (`IsDenied`) rejects destructive commands
     (`rm -rf /`, fork bombs, raw-device writes, …), with a semantic pass that
     catches flag-order, `sudo`/`sh -c`, and device variants;
   - `commandRefsProtectedPath` rejects any command that names a protected path.
   These are hard rules — approving the call in the TUI cannot override them.
4. **Permission check** — `PermissionConfig.Check(tool, path)` returns a `Level`
   (`Deny` / `Ask` / `Allow`). Protected paths always return `Deny`
   (fail-closed). On `Ask`, a `bash` command already on the allowlist skips the
   prompt; otherwise the approval prompt runs (no approval UI configured ⇒
   denied).
5. **Bash allowlist** — under `Allow`, a *non-empty* allowlist is the guard rail
   for unattended runs; an *empty* allowlist means "nothing pre-approved", not
   "everything allowed" (`Restricts()` is false, so nothing is restricted beyond
   the static gates above).
6. **Execute** — the built-in tool or the extra `CustomTool.Fn`.
7. **Trace** — the call is recorded as a child span.

`NewDefaultPermissionConfig` is the interactive (chat TUI) default: `Default` is
`Ask`, with `read_file`, `list_dir`, `grep`, and `glob` set to `Allow` and
`write_file`, `edit_file`, `delete_file`, and `bash` left at `Ask`. So only
potentially destructive calls prompt; the user can change any level with
`/permit` or project config.

Without a `PermissionConfig`, the legacy risk tier applies (`toolRisk`):
`RiskReadOnly` tools run, while `RiskWrite`/`RiskDraft` are denied. Unknown tools
default to `RiskWrite`, so they fail closed until classified.

### Protected paths

`isProtectedPath` (`permission.go`) is checked for both file tools (via the
resolved absolute path) and bash commands. It always yields `Deny`:

- filenames: `.env*`, `.gitconfig`, `.netrc`, `.npmrc`,
  `.dockercfg`/`.dockerconfigjson`;
- directories at any path level: `.ssh`, `.gnupg`, `.aws`, `.docker`, `.kube`,
  `.azure`, `.ouija`, `.config/gcloud`, `.config/kube`;
- system directories: `/etc/**`, `/var/lib/**`.

## Provider architecture

```
providers/registry.go   → map[string]func(apiKey string) engine.Provider (stateless, no key store)
providers/<name>.go     → init() calls providers.Register("name", newWithKey)
providers/client.go     → Shared *Client: PostJSON, PostSSE, circuit breaker
providers/http.go       → Retry/backoff helpers (429/5xx, Retry-After)
```

Each provider registers itself via `init()` and implements the full `Provider`
interface (`Name()`, `Chat()`, `Capabilities()`). Registration lives in
`anthropic.go`, `openai.go` (OpenAI plus the OpenRouter and Ollama shims), and
`opencode.go`. The registry is stateless: `app.DiscoverConfig` resolves the
active provider's API key into `cfg.APIKey` and `app.BuildProvider(name, key)`
passes it to the constructor. The shared `*Client` wraps
retry/backoff on 429/5xx, SSE parsing, a circuit breaker, streaming timeout, and
context-aware cleanup (no race windows, no timer leaks).

## Totem lifecycle

```
app.Run(totemName, args)
  ├─ Init(ctx, eng, cfg)    → setup (returns error → abort)
  ├─ Run(ctx, eng, args)    → main logic (uses eng.Run/Chat internally)
  └─ Cleanup()              → always runs, even on Init/Run error
```

---

🔮 ouija docs — [Index](../index.md) · [Adding a totem](../how-to/add-totem.md)
