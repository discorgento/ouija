🔮 ouija docs · [Index](../index.md)

---

# Adding a totem

> How to write a new totem, an AI agent written as ordinary Go code, from
> scratch. Audience: developer

**Prerequisite:** [Install ouija](../../README.md#install) first. You need its
`ouija generate totem` command, which creates, registers and installs a new
totem in one step.

## Where to put your totem

A totem can live in the ouija repo or in your own. Both use the same
interface:

| | In the ouija repo | In **your** repo |
|---|---|---|
| Package | `totems/<name>/` | `github.com/you/your-agents/totems/<name>/` |
| Import | `github.com/discorgento/ouija/engine` + `.../registry` | same |
| Registration | auto via `go:generate ./totems/` | manual: `init() { registry.Register(...) }` |
| Distribution | ships inside the `ouija` binary | `go build ./cmd/…` → your own binary |
| Engine features | all | all (budget, trace, sandbox, MCP) |

You never have to fork ouija. `engine`, `registry` and `schema` are public Go
packages: import them into your own module, register your totems, and build a
standalone binary. If you also want the standard ouija CLI (`setup`, `chat`,
`mcp`), ship it alongside your binary or call `ouija task` as a subprocess.

## The Totem interface

```go
type Totem interface {
    Name() string
    Summary() string
    Run(ctx context.Context, eng *engine.Engine, args registry.Args) (any, error)
}
```

- **Name** — the CLI command name (e.g. `"review"` becomes `ouija review`).
- **Summary** — the one-line description shown in `ouija help`.
- **Run** — the main logic. Call `eng.Run()` or `eng.Chat()` whenever you need
  the model. `eng.Config()` returns the project config.

A host is any program that runs totems: the ouija CLI, the MCP server and
`bootstrap.Main` are all hosts. Each runs a totem through `registry.Invoke`,
which checks the totem's required inputs and calls the optional hooks below,
so a totem behaves the same in every host.

### Optional: lifecycle hooks

```go
type Initializer interface { Init(ctx context.Context, eng *engine.Engine) error }
type Cleaner     interface { Cleanup() error }
```

- **Init** — runs before Run. Open connections and validate the config here.
  If Init returns an error, the run stops.
- **Cleanup** — runs after Run, and also after a failed Init. Close files and
  release resources here.

Implement them only when you need them, because they change how calls run.
One registered value serves every call, and the MCP server runs calls
concurrently. `Invoke` treats a totem with `Init` or `Cleanup` as holding
per-run state, so it runs that totem's calls one at a time. A totem without
them runs concurrently, so it must not keep per-run state in its struct. If
`Run` panics, the host gets an error instead of crashing.

`Init` used to take a third `*engine.Config` argument. `registry.Register`
refuses a totem that still uses that signature and says what to change.

### Optional: typed inputs

`args.Task` holds the free-text request: the CLI's first argument, or the MCP
tool's `task` argument. For named inputs, declare a JSON Schema and decode
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

The MCP server publishes this schema as the tool's `inputSchema`. The CLI
takes each input as `--set key=value`, for example
`ouija review "the auth flow" --set depth=2`, and types the value by the
schema. A `string` property keeps the text as typed (`--set ticket=1234` is
`"1234"`); any other property is read as JSON (`3`, `true`, `["a","b"]`).

A missing required input fails before Init or Run. A `task` given as
`--set task=…` fills `Args.Task`. A totem without `InputSchema` takes a single
required `task` string.

The host prints a result with its `Render()` method (see
[Renderer](#optional-renderer)) or, without one, as indented JSON. A result
that JSON cannot encode, such as a `NaN` or a channel, fails the run instead
of printing nothing. In a host of your own, `registry.Format` returns that
error.

### Optional: settings

A totem's own settings live in the project config under `totems.<name>`. Fill
a struct `v` with your defaults, then read the settings with
`eng.Config().TotemConfig(name, &v)`. If the section is absent, `v` keeps your
defaults:

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

If your result type implements `Render() string`, the CLI prints its output in
the terminal. Otherwise the CLI prints JSON.

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

The command asks for:

- **Name** — a short command name (e.g. `"review"` becomes `ouija review`).
- **Description** — a one-line summary for `ouija help`.

It creates `totems/<name>/<name>.go` with the full boilerplate: the package,
`init()`, every interface method, a `result` struct with JSON tags and a
`Render()` method. Then it runs `go generate ./totems/` and
`go install ./cmd/ouija` for you.

Your new command works right away:

```sh
ouija review "review the last commit"
```

To scaffold a **standalone module** in your own repo instead, add
`--external`. You get a `go.mod`, the totem and a `cmd/` host built on
`bootstrap`:

```sh
ouija generate totem --external [dir]
```

### 2. Implement the logic

Replace the placeholder `Run()` body with your totem's logic. The walkthrough
below builds a useful totem piece by piece, and the [Engine
API](#engine-api-for-totems) table lists everything else.

## Walkthrough: a useful totem, piece by piece

This walkthrough builds `review`, a totem that reviews a branch's changes
before you open a pull request. It asks the model to find problems, asks a
second, skeptical model call to check each one, and reports only the problems
the skeptic confirms. The finished totem ships with ouija:
[`totems/review/review.go`](../../totems/review/review.go), with its
[tests](../../totems/review/review_test.go).

### The one question: what is Go's job, what is the model's?

Before writing any code, split the work between Go and the model:

| Go decides | The model judges |
|---|---|
| which steps, in what order | is this line a bug? |
| what data each step gets | is this finding real? |
| what to do with each answer | |
| what is reported | |

Go does everything certain and repeatable: running `git`, reading a file,
filtering, counting, saving. Your code calls the model, through `eng.Run`,
only when a step needs judgment. This split is what sets a totem apart from a
prompt file. A prompt can only ask the model to "verify each finding", and the
model may skip it; a totem verifies each finding in a `for` loop.

### 1. Draw the steps

Write the steps down in order, and mark who does each one:

```
git diff (Go) → find (model) → verify each finding (model, one call each) → keep the confirmed (Go)
```

Each arrow becomes a line of Go, and each model step becomes one `eng.Run`
call.

### 2. Gather the context in Go

Do not ask the model to discover what your code can read directly. Getting the
diff takes one command:

```go
cmd := exec.CommandContext(ctx, "git", "diff", "--no-ext-diff", "--merge-base", base)
cmd.Dir = eng.Config().ProjectRoot
diff, err := cmd.Output()
```

ouija hands you more context ready-made:

- `eng.Config().Rules` — the project's rules, from `.ouija/config.json`.
- `knowledge.Load(root)` — the facts saved with `ouija learn` (available
  inside this repo).
- `engine.ReadProjectFile(ctx, root, path)` — reads a file through the
  sandbox.

### 3. Tell the model how to work — `System`, `Prompt`, tools

`eng.Run` takes a `RunRequest`. You pass context to the model through four of
its fields:

| Field | What goes there | In `review` |
|---|---|---|
| `System` | how to work: role, rules, what to ignore. Stable across calls (and cached) | "You review code changes. Report only real problems… Never style." + the project rules |
| `Prompt` | this call's data | the focus + the diff |
| `UseTools` | let it read more on its own (`read_file`, `grep`, `glob`, `list_dir`) | `true`: the diff alone is not always enough |
| `ExtraTools` | functions of **yours** it may call | — (see [Custom tools](#custom-tools-extratools), e.g. a `run_tests`) |

```go
found, err := eng.Run(ctx, engine.RunRequest{
    Label:    "find", // shown in the progress and the trace
    System:   system,
    Prompt:   "FOCUS: " + args.Task + "\n\nDIFF:\n" + diff,
    UseTools: true,
    Schema:   findingsSchema(),
})
```

### 4. Make it answer in a shape — `Schema`

Without a schema, the model answers in prose, and your code has to guess at
its meaning. With a schema, the answer **is** a JSON object with the fields
you declared. The model hands it in through a `submit_result` tool call, and
`res.Text` decodes straight into a struct:

```go
func findingsSchema() map[string]any {
    item := schema.Obj(map[string]any{
        "file":     schema.Str("path of the file, as in the diff"),
        "line":     schema.Str("the offending line, quoted exactly"),
        "problem":  schema.Str("what is wrong and what it breaks"),
        "severity": schema.Enum("how bad it is", "high", "medium", "low"),
    }, "file", "line", "problem", "severity")
    return schema.Obj(map[string]any{
        "findings": map[string]any{"type": "array", "items": item},
    }, "findings")
}

var out struct{ Findings []finding }
if err := json.Unmarshal([]byte(found.Text), &out); err != nil { … }
```

Use `schema.Enum` to make the model pick from fixed answers instead of
improvising. In the next step, the verify call must answer `"yes"` or `"no"`,
and Go branches on that answer.

### 5. Enforce the order in Go

A prompt can only *ask* the model to verify each finding. Here the
verification is code, so it always runs:

```go
for _, f := range out.Findings {
    v, err := eng.Run(ctx, engine.RunRequest{
        Label:    "verify",
        System:   "You are a skeptical reviewer… decide whether it is real.",
        Prompt:   f.File + "\n" + f.Line + "\n" + f.Problem,
        UseTools: true,
        Schema:   schema.Obj(map[string]any{"real": schema.Enum("is it real", "yes", "no")}, "real"),
    })
    // keep it only when the skeptic said yes
}
```

Calls that do not depend on each other can run in parallel. The built-in
`review` uses the same helper as the `task` totem's passes; in your own repo,
`errgroup` does the job. The engine applies the budget, the sandbox and the
trace to every call, with no extra code from you.

### 6. Take inputs — `InputSchema` + `args.Decode`

Declare the inputs your totem accepts, then decode them in `Run`:

```go
func (t *Totem) InputSchema() map[string]any {
    return schema.Obj(map[string]any{
        "task": schema.Str("what to focus on (optional)"),
        "base": schema.Str("branch to compare against (default: main)"),
    })
}

var in struct{ Base string `json:"base"` }
if err := args.Decode(&in); err != nil { … }
```

This one schema serves every host. On the command line it becomes
`--set base=develop`; over MCP it defines the tool's arguments. The host checks
its required fields before `Run` starts.

### 7. Show the result — `Render()`

Return a struct from `Run`. If the struct has a `Render() string` method, the
CLI prints its markdown output instead of JSON. `--json` still prints the
struct.

### 8. Test it without a model

`engine/enginetest` provides a scripted provider: you decide what "the model"
answers at each step, then assert what your Go code does with those answers:

```go
p := &enginetest.Provider{Respond: func(req engine.ChatRequest) enginetest.Reply {
    if strings.HasPrefix(req.System, "You review") {
        return enginetest.Submit(map[string]any{"findings": []any{ /* two findings */ }})
    }
    return enginetest.Submit(map[string]any{"real": "yes", "reason": "…"}) // the skeptic
}}
out, err := registry.Invoke(ctx, enginetest.NewEngine(p, root), &Totem{}, registry.Args{})
```

`review`'s tests check that the diff reaches the find step, that each finding
gets its own verify call, and that the report holds only the confirmed
finding. They need no API key, cost nothing and give the same result every
run.

### Run it

The finished totem runs in three places:

```sh
ouija review "concurrency"      # terminal
/review concurrency             # in the chat
ouija_review                    # a tool in Claude Code, Cursor or OpenCode (MCP)
```

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

A totem can give the model tools of its own for a single agent call. These
tools need no global registration or separate setup, and they leave nothing
behind for the next call.

Pass them in `RunRequest.ExtraTools` as a `[]engine.CustomTool`:

```go
type CustomTool struct {
    Def ToolDef // schema the model sees
    Fn  ToolFn  // implementation
}

// ToolFn receives a context, the project root (sandboxed), and the model's
// input map, and returns a structured ToolResult.
type ToolFn func(ctx context.Context, root string, input map[string]any) ToolResult
```

`ToolDef.Risk` sets the tool's permission tier and defaults to `RiskReadOnly`.
When a `PermissionConfig` is set, as it is by default in the chat TUI, every
tool follows the config's `deny` / `ask` / `allow` levels. Without a config,
`RiskReadOnly` tools run automatically, and `RiskWrite` and `RiskDraft` tools
are denied.

`ToolDef.Risk` constants:

| Constant | Purpose | Without PermissionConfig | With PermissionConfig |
|---|---|---|---|
| `RiskReadOnly` | No side effects (default) | Runs automatically | Subject to permission level |
| `RiskDraft` | Simulate, no external effect | Denied | Subject to permission level |
| `RiskWrite` | Mutates files / state | Denied | Subject to permission level |

In the **chat TUI**, the default permission config
(`NewDefaultPermissionConfig`) sets `Default` to `ask`. It allows the
read-only tools (`read_file`, `list_dir`, `grep`, `glob`) without approval and
keeps `write_file`, `edit_file`, `delete_file` and `bash` at `ask`, so only
calls that could do damage prompt the user. The user can change any level with
`/permit` or in the project config. **Totems** that call `eng.Run()` get the
risk-tier default unless they call `SetPermissionConfig` or the project config
sets `permission`.

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
- Extra tools are **scoped to one call**: each `Run` starts without them.
- `submit_result` always runs last. The extra tools run first, so the model can call a tool with side effects and signal completion in the same turn.
- Resolve every path you read **or write** with `engine.SafeResolve(ctx, root, ...)`. It keeps the path inside the project root and rejects protected paths (`.env`, `.ssh/*`, `/etc/*`) and symlinks that point outside the root. It rejects dangling symlinks too, so a planted `docs -> ~/.ssh` cannot redirect a write. The path need not exist yet. `engine.SafeJoin` checks only the path text, which is not enough for a write into a repository you did not write.
- The `root` parameter is the engine's sandbox root. Use it instead of `os.Getwd()` or hardcoded paths.
- The host gates every custom tool: before your `Fn` runs, the `ToolRunner` applies the permission level and, for `Ask`, shows the approval prompt. When no `PermissionConfig` is set, a write-tier custom tool is denied by default.
- After `Run`, check `res.Tools` to see which tools the model called. The `task` totem uses `slices.Contains(res.Tools, "write_plan")` to decide whether to run its Go fallback.

## Testing a totem

`engine/enginetest` runs a totem against a scripted model, with no network
and no API key. Its `Provider` answers each model call with whatever your
`Respond` function returns for that request. `Submit` builds a reply that
hands in a run's structured result.

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

Return a `Reply{Err: ...}` to fail that call and test how the totem handles
an error. The provider is safe for concurrent use, so parallel passes work.

---

## Build your own binary

Once your totem lives in your repo, a minimal `cmd/totem/main.go` wires it up:

```go
package main

import (
    "os"

    "github.com/discorgento/ouija/bootstrap"
    _ "github.com/you/your-agents/totems/review"
)

func main() { os.Exit(bootstrap.Main(bootstrap.WithName("my-totem"))) }
```

`bootstrap.Main` is the whole command line:

```sh
my-totem                                   # list the totems
my-totem review "concurrency" --set base=develop   # run one (--json for raw JSON, --config <file>)
my-totem mcp [--config <file>]             # serve them over MCP (stdio)
```

To write a host of your own, use `bootstrap.Setup`, the layer under
`bootstrap.Main`. It finds the config (the project layers plus the global
`~/.config/ouija/config.json` that `ouija setup` writes), validates it, builds
the provider and returns a ready engine. Then
`app.Run(ctx, "review", registry.Args{Task: "…"})` runs a totem through
`registry.Invoke`, and `app.ServeMCP` serves the totems over MCP.

`app.Engine` keeps one budget across every `app.Run`. A long-lived host that
runs a totem per request should therefore call `Setup` for a fresh `App` on
each request. `ServeMCP` already gives each tool call a fresh engine from the
same config, with its own budget; setters you call on `app.Engine` do not
apply there.

`bootstrap` accepts these options:

| Option | Effect |
|---|---|
| `bootstrap.WithConfigPath(path)` | Load an explicit config file instead of discovering |
| `bootstrap.WithName(name)` | MCP identity (`<name>_<totem>` tools in the harness); defaults to the binary's name |
| `bootstrap.WithTrace(true)` | Console trace tree on stderr (development) |

`go mod tidy` pulls in `github.com/discorgento/ouija` and its engine. To
build against a local clone, run `go mod edit -replace
github.com/discorgento/ouija=/path/to/ouija`. Build with
`go build -o my-totem ./cmd/totem`, or cross-compile with the flags ouija's
`make dist` uses: `CGO_ENABLED=0 go build -trimpath`.

A totem registered this way is a normal Go package. It has tests, it can ship
in a monorepo with your product, and it gets engine upgrades when you bump one
`go.mod` line instead of rebasing a fork.

---

🔮 ouija docs — [Index](../index.md) · [Adding a provider](add-provider.md)
