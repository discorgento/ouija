🔮 ouija docs · [Index](../index.md)

---

# Architecture

> High-level design of the ouija project. Audience: developer

## Layers

ouija is a harness: it runs a model in a loop and executes the tool calls the
model asks for. The engine does that work; each totem is an agent, written in
Go, that decides which model calls to make and in what order.

```
cmd/              → Entry points (ouija CLI + `ouija mcp`, ouija-mcp standalone MCP server)
  └─ imports _ "github.com/discorgento/ouija/totems"  → auto-registered via go generate

Public packages (importable by external totem modules):
engine/           → Core agent loop, tools, sandbox, permissions, budget, tracing
  └─ providers/   → Model adapters (anthropic, openai/openrouter/ollama, opencode)
registry/         → Totem interface, registration and lookup
schema/           → JSON Schema generation helpers
bootstrap/        → Config discovery → ready engine (+ MCP) for embedders

internal/
  ├─ app/         → Orchestration: config discovery, engine setup, totem lifecycle
  ├─ mcp/         → MCP server (stdio) + harness registration (install/status)
  ├─ chat/        → Bubble Tea TUI (interactive chat)
  ├─ commands/    → CLI commands (setup, init, config, trust, learn, generate, doctor)
  ├─ ouijaconfig/ → XDG-compliant config management
  ├─ knowledge/   → Project knowledge storage
  ├─ models/      → Provider/model catalog, plus the live model lists cached in the user cache dir
  ├─ art/         → Shared figlet banner/logo (CLI + TUI)
  ├─ theme/       → Color themes (OUIJA_THEME, /theme) and the animation switch
  ├─ progress/    → Live progress board for a totem run on a terminal
  ├─ termsafe/    → Strips (or, for `ouija trust`, reveals) control and invisible characters before text reaches the terminal
  ├─ atomicfile/  → Crash-safe file writes (temp file + rename)
  ├─ doccheck/    → Tests that the docs' links, anchors and index stay valid, and that every config key, command and OUIJA_* variable is documented
  ├─ version/     → Build version (injectable via -ldflags -X)
  └─ tools/genimports → Build helper: auto-generates totem import list
totems/           → Registered totems (task, review, doc, sticker)
```

## Data flow

```
CLI args → app.DiscoverConfig()
           → merges global + project config
           → app.ValidateConfig()
           → app.BuildProvider() → creates provider from registry
           → engine.New() → wires provider, config, budget, tracer, logger
           → registry.Invoke(ctx, eng, totem, args)
               → required inputs (InputSchema) → Init (optional)
           → totem.Run(ctx, eng, args)
               → eng.Run() or eng.Chat()
                   → runAgent() → fresh DoomDetector → agent.Run()
                       → model proposes tool call
                       → harness validates + executes
                       → repeat until a final answer (text or submit_result)
           → Cleanup (optional, always)
           → render result (Renderer or JSON)
```

## Engine pipeline

```
Engine.Run(ctx, req) / Engine.Chat(ctx, req)
  └─ runAgent() → fresh DoomDetector → agent.Run()
       └─ (*ReAct).Run(ctx, env, req, msgs)   ← the loop strategy (Agent)
            ├─ streamChat() → provider.Chat() → iter.Seq2[StreamEvent, error]
            ├─ tool execution (gated by ToolRunner, sandboxed)
            ├─ budget tracking
            ├─ trace events
            └─ repeat until a final answer or budget ceiling
```

Every `Engine.Run` or `Engine.Chat` call goes through `runAgent` once.
`runAgent` gives the run its own doom-loop detector, which catches an agent
stuck repeating the same tool call. The detector travels in the context, so the
concurrent passes of a totem never share loop state. `runAgent` then hands off
to the loop strategy (`ReAct`), and the turns themselves loop inside
`ReAct.Run`.

The permission and approval settings live in a single `gateState` value that
`Engine` and its `ToolRunner` share **by pointer**. As a result, the setters
(`SetPermissionConfig`, `SetApprovalManager`, `SetToolCallback`) can never leave
the runner holding stale state.

## Tool gating (permissions)

Every tool call goes through `ToolRunner.Run` (`engine/toolrunner.go`),
which applies its checks in a fixed order:

1. **UI progress callback** — `ToolCallback` fires before anything else.
2. **Doom-loop detection** — the same tool + input three turns in a row is
   blocked.
3. **Path characters** — a `path` argument with a control character (newline
   and tab included), a line or paragraph separator, or an invisible
   (zero-width, tag, other format) or bidi character (`checkPathRunes`) is
   refused for every tool.
4. **Static bash gates** — for `bash` only, and *before any prompt*:
   - a command that contains a NUL byte is refused, and so is one with a
     character the approval prompt would hide (a control character other
     than newline and tab, a line or paragraph separator, an invisible
     character: zero-width, tag, other format, or a bidi character);
   - the hardcoded **denylist** (`IsDenied`) rejects destructive commands
     (`rm -rf /`, fork bombs, raw-device writes, …), with a semantic pass that
     catches flag-order, `sudo`/`sh -c`, and device variants;
   - `commandRefsProtectedPath` rejects any command that names a protected path.
   These are hard rules: approving the call in the TUI cannot override them.
5. **Unsandboxed run** — a `bash` call with `"unsandboxed": true`, while the
   sandbox is on, still meets the permission level (`Deny` refuses it, `Allow`
   checks the allowlist, no `PermissionConfig` refuses it). It is refused when
   `allowUnsandboxed` is `false` or there is no approval UI. Otherwise the user
   is asked every time, in a prompt marked as running outside the sandbox,
   whatever the level or the allowlist says. An approved call runs and skips
   the steps below.
6. **Permission check** — `PermissionConfig.Check(tool, path)` returns a `Level`
   (`Deny` / `Ask` / `Allow`). Protected paths always return `Deny`
   (fail-closed). On `Ask`, a `bash` command already on the allowlist skips the
   prompt; otherwise the approval prompt runs (no approval UI configured ⇒
   denied). The prompt names the file the call physically lands on (`_path`)
   and the name it was reached by when a symlink differs (`_via`), and the
   approval key is that physical path. If the path leads somewhere else once
   the user answers (a link swapped while the prompt waited), the call is
   refused.
7. **Bash allowlist** — under `Allow`, a *non-empty* allowlist limits which
   commands an unattended run may execute. An *empty* allowlist means "nothing
   pre-approved", not "everything allowed": `Restricts()` is false, so only the
   static gates above restrict commands.
8. **Execute** — the built-in tool or the extra `CustomTool.Fn`. A file
   change is snapshotted first for `/undo`.
9. **Trace** — the call is recorded as a child span.

`NewDefaultPermissionConfig` is the interactive (chat TUI) default: `Default` is
`Ask`, with `read_file`, `list_dir`, `grep`, and `glob` set to `Allow` and
`write_file`, `edit_file`, `delete_file`, and `bash` left at `Ask`. Only calls
that could change or destroy something prompt the user, who can change any
level with `/permit` or in the project config.

Without a `PermissionConfig`, the legacy risk tiers apply (`toolRisk`):
`RiskReadOnly` tools run, and `RiskWrite` and `RiskDraft` tools are denied. An
unknown tool defaults to `RiskWrite`, so it stays denied until someone
classifies it.

### Protected paths

ouija checks `isProtectedPath` (`permission.go`) for both file tools, against
the resolved absolute path, and bash commands. A protected path always yields
`Deny`:

- filenames: `.env*`, `.gitconfig`, `.netrc`, `.npmrc`,
  `.dockercfg`/`.dockerconfigjson`, `.git-credentials`, `.pgpass`, `.pypirc`,
  `.my.cnf`, `.vault-token`, `id_rsa`/`id_dsa`/`id_ecdsa`/`id_ed25519`,
  `*.tfstate`, `*.tfstate.backup`, `.ouija.json`, `ouija.json`;
- secrets a project keeps in its own tree: `wp-config.php`, `auth.json`,
  `credentials.json`, `service-account.json`, `*-sa.json`,
  `secrets.yml`/`secrets.yaml`, `credentials.yml.enc`, `terraform.tfvars`,
  `*.auto.tfvars`, `.htpasswd`, `.yarnrc`, `.dev.vars`,
  `local.settings.json`, `kubeconfig`, and at a known place
  `app/etc/env.php`, `config/database.yml`, `.bundle/config`;
- keys and keystores by extension: `*.pem`, `*.key`, `*.p12`, `*.pfx`,
  `*.jks`, `*.keystore`;
- directories at any path level: `.ssh`, `.gnupg`, `.aws`, `.docker`, `.kube`,
  `.azure`, `.ouija`, `.password-store`, `.mozilla`, `.config/gcloud`,
  `.config/kube`, `.config/gh`, and ouija's global config (`.config/ouija`, or
  wherever `os.UserConfigDir()` puts it, plus that dir's `gcloud`);
- git config and hooks inside any `.git` directory (`isGitConfigOrHook`):
  `config`, `config.worktree` and `hooks/*`, including those under
  `.git/modules/*` and `.git/worktrees/*`;
- registry and model-hub token files inside tool dirs: `.cargo/credentials`,
  `.cargo/credentials.toml`, `.m2/settings.xml`, `.m2/settings-security.xml`,
  `.gradle/gradle.properties`, `.docker/config.json`,
  `.terraform.d/credentials.tfrc.json`, `.composer/auth.json`,
  `.gem/credentials`; and, under the home directory only, `huggingface/token`,
  `uv/credentials`, `git/credentials`, `rclone/rclone.conf`, `share/keyrings`,
  `share/kwalletd`, `composer/auth.json`, `pip/pip.conf`,
  `containers/auth.json`, `etc/npmrc`;
- browser profiles and other CLIs' tokens: `.config/google-chrome`,
  `.config/chromium`, `.config/hub`, `.config/glab-cli`, `.yarnrc.yml`,
  `.s3cfg`, `.terraformrc`;
- system directories: `/etc/**`, `/var/lib/**`, `/proc/**`, `/sys/**`, and on
  macOS `/private/etc/**` and `/private/var/lib/**`.

Names are compared case-folded on every OS (`.ENV` is `.env`). Apart from
this list, the file tools never write or delete git metadata (`isGitMetadata`:
any path with a `.git` component), though they may read it.

## Provider architecture

```
providers/registry.go   → map[string]func(apiKey string) engine.Provider (stateless, no key store)
providers/<name>.go     → init() calls providers.Register("name", newWithKey)
providers/client.go     → Shared *Client: PostJSON, PostSSE, circuit breaker
providers/http.go       → Retry/backoff helpers (429/5xx, Retry-After)
```

Each provider registers itself in `init()` and implements the full `Provider`
interface (`Name()`, `Chat()`, `Capabilities()`). Registration lives in
`anthropic.go`, `openai.go` (OpenAI plus the OpenRouter and Ollama shims), and
`opencode.go`. The registry holds no keys: `app.DiscoverConfig` resolves the
active provider's API key into `cfg.APIKey`, and `app.BuildProvider(name, key)`
passes it to the constructor. The shared `*Client` handles retry with backoff
on 429 and 5xx responses, SSE parsing, a circuit breaker, a streaming timeout,
and cleanup tied to the context, with no race windows and no timer leaks.

## Totem lifecycle

```
registry.Invoke(ctx, eng, totem, args)   ← app.Run, the MCP server, bootstrap.Main
  ├─ required inputs        → from InputSchema (default: a required task)
  ├─ Init(ctx, eng)         → optional Initializer; error → abort
  ├─ Run(ctx, eng, args)    → main logic (uses eng.Run/Chat internally)
  └─ Cleanup()              → optional Cleaner; always runs, even on Init/Run error
```

A totem keeps its own settings under
`totems.<name>` in the project config and reads them with `Config.TotemConfig`;
the engine never interprets them.

---

🔮 ouija docs — [Index](../index.md) · [Adding a totem](../how-to/add-totem.md)
