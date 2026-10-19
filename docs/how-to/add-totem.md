🔮 ouija docs · [Index](../index.md)

---

# Adding a totem

> Creating a new totem from scratch. Audience: developer

**Prerequisite:** [Install ouija](../../README.md#install) first — you need
`ouija generate totem`, which creates, registers, and installs the new totem
in one step.

## Where to put your totem

Two valid homes, same interface:

| | In the ouija repo | In **your** repo |
|---|---|---|
| Package | `totems/<name>/` | `github.com/you/your-agents/totems/<name>/` |
| Import | `github.com/discorgento/ouija/engine` + `.../registry` | same |
| Registration | auto via `go:generate ./totems/` | manual: `init() { registry.Register(...) }` |
| Distribution | ships inside the `ouija` binary | `go build ./cmd/…` → your own binary |
| Engine features | all | all (budget, trace, sandbox, MCP) |

You never have to fork ouija. `engine`, `registry` and `schema` are public Go
packages — import them into your own module, register your totems, and build a
standalone binary. If you also want the standard ouija CLI (`setup`, `chat`,
`mcp`), you can either ship it alongside or call `ouija task` as a subprocess.

## The Totem interface

```go
type Totem interface {
    Name() string
    Summary() string
    Run(ctx context.Context, eng *engine.Engine, args registry.Args) (any, error)
}
```

- **Name** — CLI command name (e.g. `"review"` becomes `ouija review`)
- **Summary** — one-line description shown in `ouija help`
- **Run** — main logic. Use `eng.Run()` or `eng.Chat()` for agentic work. The
  project config is `eng.Config()`.

Hosts run a totem through `registry.Invoke`, which checks its required inputs
and drives the optional hooks below — the ouija CLI, the MCP server and
`bootstrap.Main` all do, so a totem behaves the same everywhere.

### Optional: lifecycle hooks

```go
type Initializer interface { Init(ctx context.Context, eng *engine.Engine) error }
type Cleaner     interface { Cleanup() error }
```

- **Init** — called before Run: open connections, validate the config. An
  error aborts the run.
- **Cleanup** — called after Run, and after a failed Init too. Close files,
  release resources.

Implement them only when you need them. One registered value serves every
call, and the MCP server runs calls concurrently: a totem with `Init` or
`Cleanup` keeps per-run state, so `Invoke` runs its calls one at a time; a
totem without them runs concurrently and must not keep per-run state in its
struct. A panic in `Run` is returned as an error instead of crashing the
host. (`Init` used to take a third
`*engine.Config` argument; a totem still on that signature is refused at
`registry.Register` with a message saying what to change.)

### Optional: typed inputs

`args.Task` is the free-text request — the CLI's first argument, the MCP
tool's `task`. For named inputs, declare a JSON Schema and decode
`args.Params` into a struct:

```go
func (t *Totem) InputSchema() map[string]any {
    return schema.Obj(map[string]any{
        "task":  schema.Str("what to review"),
        "depth": map[string]any{"type": "integer", "description": "how many layers to follow"},
    }, "task", "depth") // required inputs
}

func (t *Totem) Run(ctx context.Context, eng *engine.Engine, args registry.Args) (any, error) {
    var in struct {
        Depth int `json:"depth"`
    }
    if err := args.Decode(&in); err != nil {
        return nil, err
    }
    // …
}
```

The schema is the MCP tool's `inputSchema`, and the CLI takes the inputs as
`--set key=value`, typed by the schema: a `string` property keeps the text as
typed (`--set ticket=1234` is `"1234"`), anything else is read as JSON (`3`,
`true`, `["a","b"]`): `ouija review "the auth flow" --set depth=2`. A missing
required input is an error before Init or Run; a `task` given as
`--set task=…` fills `Args.Task`. Without `InputSchema`, the input is a
required `task` string.

A result is printed with its `Render()` (a `Renderer`) or as indented JSON;
one JSON cannot encode (a `NaN`, a channel) fails the run instead of printing
nothing — `registry.Format` returns that error for a host of your own.

### Optional: settings

A totem's own settings live in the project config under `totems.<name>`; read
them with `eng.Config().TotemConfig(name, &v)`, after filling `v` with your
defaults (an absent section leaves `v` untouched):

```json
{ "totems": { "review": { "maxFiles": 40 } } }
```

```go
c := reviewConfig{MaxFiles: 20}
if err := eng.Config().TotemConfig("review", &c); err != nil {
    return nil, err
}
```

### Optional: Renderer

If your result type implements `Render() string`, the CLI uses it for terminal
output. Otherwise it falls back to JSON.

```go
type Renderer interface {
    Render() string
}
```

## Step by step

### 1. Scaffold via CLI

```sh
ouija generate totem
```

You will be prompted for:
- **Name** — short command name (e.g. `"review"` becomes `ouija review`)
- **Description** — one-line summary for `ouija help`

This creates `totems/<name>/<name>.go` with the full boilerplate (package,
`init()`, all interface methods, a `result` struct with JSON tags, and a
`Render()` method), then auto-runs `go generate ./totems/` and
`go install ./cmd/ouija`.

Your new command is ready to use immediately:

```sh
ouija review "review the last commit"
```

To scaffold a **standalone module** in your own repo instead (go.mod + totem +
`cmd/` host on `bootstrap`), add `--external`:

```sh
ouija generate totem --external [dir]
```

### 2. Implement the logic

Replace the placeholder `Run()` body with your totem's logic. Use the
[Engine API](#engine-api-for-totems) for agentic work, tool calls, multi-step
orchestration, and structured output.

## Engine API for totems

| Method | Purpose |
|---|---|
| `eng.Run(ctx, req)` | Single agent loop (fresh prompt, returns `*RunResult`) |
| `res.Text` | The final assistant text (JSON string when `Schema` was set) |
| `res.Tools` | Tool names called during this run (useful for post-run inspection) |
| `eng.Chat(ctx, req)` | Continue a conversation (returns text + updated messages) |
| `eng.Config()` | Access the merged config |
| `eng.Logger()` | Get the `*slog.Logger` for structured logging |
| `eng.Spent()` | Get cumulative tokens, steps, USD |
| `eng.SetModel(name)` | Switch model mid-run |
| `eng.SetProvider(p)` | Switch provider mid-run |
| `eng.SetApprovalManager(am)` | Wire the tool-approval prompt implementation |
| `eng.SetPermissionConfig(cfg)` | Set per-tool permission levels |
| `eng.SetToolCallback(fn)` | React to tool calls (UI progress) |
| `eng.SetTokenCallback(fn)` | React to streaming tokens (real-time display) |
| `req.ExtraTools` | Inject scoped tools (def + impl) into one agent call, no global registration |

## Example patterns

### Structured output (JSON schema)

```go
schema := map[string]any{
    "type": "object",
    "properties": map[string]any{
        "issues": map[string]any{
            "type": "array",
            "items": map[string]any{
                "type": "object",
                "properties": map[string]any{
                    "file":  map[string]any{"type": "string"},
                    "line":  map[string]any{"type": "integer"},
                    "issue": map[string]any{"type": "string"},
                },
            },
        },
    },
}
res, err := eng.Run(ctx, engine.RunRequest{
    Label:    "analysis",
    System:   "Analyze the code and return structured results.",
    Prompt:   args.Task,
    Schema:   schema,
    UseTools: true,
})
// res.Text is the structured JSON; res.Tools lists tools the model called.
```

### Multi-agent orchestration

```go
func (t *Totem) Run(ctx context.Context, eng *engine.Engine, args registry.Args) (any, error) {
    // First agent: scout
    scout, err := eng.Run(ctx, engine.RunRequest{Label: "scout", Prompt: args.Task, UseTools: true})
    if err != nil {
        return nil, err
    }
    // Second agent: verify
    verify, err := eng.Run(ctx, engine.RunRequest{Label: "verify", Prompt: fmt.Sprintf("Verify this:\n%s", scout.Text), UseTools: true})
    if err != nil {
        return nil, err
    }
    // Reconcile
    final, err := eng.Run(ctx, engine.RunRequest{Label: "reconcile", Prompt: fmt.Sprintf("Reconcile:\n%s\n%s", scout.Text, verify.Text), UseTools: true})
    return final, err
}
```

### Custom tools (ExtraTools)

Totems can inject tools scoped to a single agent call — no global registration,
no two-step setup, no side effects between calls.

Use `RunRequest.ExtraTools` with `[]engine.CustomTool`:

```go
type CustomTool struct {
    Def ToolDef // schema the model sees
    Fn  ToolFn  // implementation
}

// ToolFn receives a context, the project root (sandboxed), and the model's
// input map, and returns a structured ToolResult.
type ToolFn func(ctx context.Context, root string, input map[string]any) ToolResult
```

`ToolDef.Risk` controls the permission tier (defaults to `RiskReadOnly`). When a
`PermissionConfig` is set (chat TUI default), all tools are subject to the
config's `deny` / `ask` / `allow` levels. Without a config, `RiskReadOnly` tools
run automatically; `RiskWrite` and `RiskDraft` are denied.

`ToolDef.Risk` constants:

| Constant | Purpose | Without PermissionConfig | With PermissionConfig |
|---|---|---|---|
| `RiskReadOnly` | No side effects (default) | Runs automatically | Subject to permission level |
| `RiskDraft` | Simulate, no external effect | Denied | Subject to permission level |
| `RiskWrite` | Mutates files / state | Denied | Subject to permission level |

In the **chat TUI**, the default permission config (`NewDefaultPermissionConfig`)
sets `Default` to `ask`, but allows the read-only tools (`read_file`, `list_dir`,
`grep`, `glob`) without approval and keeps `write_file`, `edit_file`,
`delete_file`, and `bash` at `ask` — so only potentially destructive calls
prompt. The user can change any level with `/permit` or project config. For
**totems** using `eng.Run()`, the risk-tier default applies unless
`SetPermissionConfig` is called explicitly (or the project config sets
`permission`).

#### Example: write_plan (from the task totem)

```go
var writePlanDef = engine.ToolDef{
    Name:        "write_plan",
    Description: "Save the rendered task plan as markdown to docs/task-plan.md",
    Schema: map[string]any{
        "type": "object",
        "properties": map[string]any{
            "content": map[string]any{
                "type":        "string",
                "description": "Markdown content of the plan",
            },
        },
        "required": []string{"content"},
    },
    // ReadOnly is honest for the risk-tier gate: the destination is fixed in
    // code (never model-controlled), so there is nothing for approval to weigh.
    Risk: engine.RiskReadOnly,
}

var writePlanFn engine.ToolFn = func(ctx context.Context, root string, input map[string]any) engine.ToolResult {
    content, _ := input["content"].(string)
    if content == "" {
        return engine.ToolResult{Error: &engine.ToolError{Code: "INVALID_INPUT", Message: "write_plan: content is required"}}
    }
    // SafeResolve, not SafeJoin: a planted docs/ symlink must not redirect
    // this unattended write outside the project.
    dst, err := engine.SafeResolve(ctx, root, "docs/task-plan.md")
    if err != nil {
        return engine.ToolResult{Error: &engine.ToolError{Code: "INVALID_INPUT", Message: err.Error()}}
    }
    if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
        return engine.ToolResult{Error: &engine.ToolError{Code: "INTERNAL", Message: err.Error()}}
    }
    if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
        return engine.ToolResult{Error: &engine.ToolError{Code: "INTERNAL", Message: err.Error()}}
    }
    return engine.ToolResult{Data: "plan saved to " + dst}
}
```

Then pass it in the `RunRequest`:

```go
eng.Run(ctx, engine.RunRequest{
    ExtraTools: []engine.CustomTool{{Def: writePlanDef, Fn: writePlanFn}},
    Schema:     mySchema(),  // submit_result
    UseTools:   true,        // built-in tools too
})
```

**Key behaviors:**
- Extra tools are **scoped to one call** — each `Run` starts fresh.
- `submit_result` is always deferred: extra tools run first, then `submit_result` is processed last. This lets the model call a side-effect tool and signal completion in the same turn.
- Resolve every path you read **or write** with `engine.SafeResolve(ctx, root, ...)`: it keeps the path in the project root, rejects symlinks that point out of it (dangling ones included, so a planted `docs -> ~/.ssh` cannot redirect a write) and protected paths (`.env`, `.ssh/*`, `/etc/*`). The path need not exist yet. `engine.SafeJoin` is only the lexical check — not enough for a write into a repository you did not write.
- The `root` parameter is the engine's sandbox root — don't use `os.Getwd()` or hardcoded paths.
- A custom tool is gated by the host: the `ToolRunner` applies the permission level and, for `Ask`, the approval prompt, before your `Fn` runs. A write-tier custom tool is denied by default when no `PermissionConfig` is set.
- After `Run`, check `res.Tools` to see which tools the model called. The `task` totem uses `slices.Contains(res.Tools, "write_plan")` to decide whether to run its Go fallback.

## Testing a totem

`engine/enginetest` runs a totem against a scripted model — no network, no
key. Its `Provider` answers each model call with whatever your `Respond`
function returns for that request; `Submit` hands in a run's structured
result.

```go
func TestRun(t *testing.T) {
	p := &enginetest.Provider{Respond: func(req engine.ChatRequest) enginetest.Reply {
		if strings.HasPrefix(req.System, "You scout") {
			return enginetest.Submit(map[string]any{"summary": "a calc package"})
		}
		return enginetest.Reply{Text: "# Report"}
	}}
	eng := enginetest.NewEngine(p, t.TempDir())
	out, err := registry.Invoke(context.Background(), eng, &Totem{}, registry.Args{Task: "x"})
	// assert on out, on the files written under the root, on p.Requests()
}
```

A `Reply{Err: ...}` fails that call, to test how the totem handles a pass that
errors. The provider is safe for concurrent use, so parallel passes work.

---

## Build your own binary

Once your totem lives in your repo, a minimal `cmd/totem/main.go` wires it up:

```go
package main

import (
    "os"

    "github.com/discorgento/ouija/bootstrap"
    _ "github.com/you/your-agents/totems/sentinel"
)

func main() { os.Exit(bootstrap.Main(bootstrap.WithName("my-totem"))) }
```

`bootstrap.Main` is the whole command line:

```sh
my-totem                                   # list the totems
my-totem sentinel "task" --set depth=2     # run one (--json for raw JSON, --config <file>)
my-totem mcp [--config <file>]             # serve them over MCP (stdio)
```

For a host of your own, `bootstrap.Setup` is the glue underneath: it
discovers config (project layers + the global `~/.config/ouija/config.json`
written by `ouija setup`), validates it, builds the provider and returns a
ready engine; `app.Run(ctx, "sentinel", registry.Args{Task: "…"})` then runs a
totem through `registry.Invoke`, and `app.ServeMCP` serves them. `app.Engine`
keeps one budget across every `app.Run` — a long-lived host that runs a totem
per request should `Setup` a fresh `App` per request — while `ServeMCP` gives
each tool call a fresh engine from the same config (its own budget; the
setters you call on `app.Engine` do not apply there). Options:

| Option | Effect |
|---|---|
| `bootstrap.WithConfigPath(path)` | Load an explicit config file instead of discovering |
| `bootstrap.WithName(name)` | MCP identity (`<name>_<totem>` tools in the harness); defaults to the binary's name |
| `bootstrap.WithTrace(true)` | Console trace tree on stderr (development) |

`go mod tidy` will pull `github.com/discorgento/ouija` and its engine (to
build against a local clone: `go mod edit -replace
github.com/discorgento/ouija=/path/to/ouija`). Build
with `go build -o my-totem ./cmd/totem`, or cross-compile with the same flags
ouija uses (`make dist` → `CGO_ENABLED=0 go build -trimpath`).

A totem registered this way is a normal Go package: it has tests, it can ship
in a monorepo with your product, and it gets engine upgrades by bumping a
`go.mod` line — not by rebasing a fork.

---

🔮 ouija docs — [Index](../index.md) · [Adding a provider](add-provider.md)
