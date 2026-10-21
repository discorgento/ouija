🔮 ouija docs · [Index](../index.md)

---

# Config schema

> JSON structure for global and project configuration files. Audience: reference

## Global config: `~/.config/ouija/config.json`

The file lives in the OS's user config directory: `~/.config/ouija/config.json` on
Linux (or `$XDG_CONFIG_HOME/ouija/`), `~/Library/Application Support/ouija/`
on macOS, `%AppData%\ouija\` on Windows. `ouija setup` and `ouija doctor`
print the path in use.

```json
{
  "apiKeys": {
    "anthropic": "sk-ant-...",
    "openai": "sk-proj-...",
    "openrouter": "",
    "ollama": ""
  },
  "defaults": {
    "provider": "anthropic",
    "model": "claude-opus-5-5"
  }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `apiKeys` | `map[string]string` | No | API keys per provider. An empty string means not configured |
| `defaults.provider` | `string` | Yes (if defaults block) | Default provider name |
| `defaults.model` | `string` | Yes (if defaults block) | Default model ID |
| `theme` | `string` | No | UI color theme: `ouija` (default), `catppuccin`, `dracula`, `mono`. Set by `/theme` in the chat; `OUIJA_THEME` overrides it |
| `sandbox` | `object` | No | The bash sandbox for **every** project, same keys as [`tools.bash.sandbox`](#toolsbashsandbox). A project cannot turn it off; a trusted one may add paths and ports |

`sandbox` here is the way to confine bash in repositories you have not
trusted: a project's own `tools.bash.sandbox` only applies after `ouija trust`,
and a cloned repository could otherwise simply leave it off. With
`"sandbox": {"enabled": true}` in the global config, every project's bash runs
confined; a trusted project's sandbox section adds its `read`, `write` and
`network` entries on top, and `enabled: false` there changes nothing.

For backward compatibility, ouija still reads the legacy flat
`provider`/`model` fields at the top level.

File permissions: `0600` (ouija tightens looser permissions on load).

## Project config: `.ouija/config.json`

```json
{
  "provider": "openrouter",
  "model": "qwen/qwen3-coder:free",
  "apiKey": "sk-alt-...",
  "permission": {
    "default": "ask",
    "tools": {
      "read_file": "allow",
      "glob": "allow"
    }
  },
  "modelSuggestions": {
    "anthropic": ["claude-sonnet-5-5"],
    "openai": ["gpt-4o"]
  }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `provider` | `string` | No | Override default provider |
| `model` | `string` | No | Override default model |
| `apiKey` | `string` | No | Override the API key (set it only when it differs from the global one) |
| `permission` | `PermissionRule` | No | Permission defaults (see the [`permission` field](#permission-field)) |
| `modelSuggestions` | `map[string][]string` | No | Suggested models per provider, for autocomplete in the TUI model picker |

File permissions: `0600`.

## Engine config: `Config` struct

At runtime ouija uses the merged `engine.Config` struct: the built-in
defaults, overlaid by the global config, then by the project config.

```json
{
  "provider": "anthropic",
  "model": "claude-opus-5-5",
  "maxNudges": 3,
  "totems": {
    "task": { "passes": 3, "dimensions": ["correctness", "best-practices", "security", "performance"] },
    "doc": { "passes": 3 }
  },
  "rules": ["Prefer clarity and correctness", "Cite file/line when asserting something"],
  "projectRoot": ".",
  "budget": {
    "maxTokens": 2000000,
    "maxSteps": 500,
    "maxUSD": null
  },
  "costPer1MInput": 0,
  "costPer1MOutput": 0,
  "streamTimeout": 120,
  "modelSuggestions": {"anthropic": ["claude-sonnet-5-5"], "openai": ["gpt-4o"]},
  "tools": {
    "bash": {
      "allowlist": ["npm run build", "composer install"]
    }
  },
  "permission": {
    "default": "ask",
    "tools": {
      "read_file": "allow",
      "glob": "allow",
      "list_dir": "allow",
      "grep": "allow"
    }
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `provider` | `string` | — (required) | Active provider. No built-in default: set it here or with `ouija setup` (global) |
| `model` | `string` | — (required) | Active model. No built-in default — which model to run, and pay for, is your choice |
| `apiKey` | `string` | `""` | Project-level API key override |
| `systemPrompt` | `string` | built-in prompt ("You are an AI coding assistant with access to the project files…") | Overrides the default chat system prompt |
| `maxNudges` | `int` | `3` | Max retries when the model fails to return structured output |
| `totems.<name>` | `object` | — | A totem's own settings (a totem is an agent workflow such as `task`), read with `Config.TotemConfig`; the engine never interprets them |
| `totems.task.passes` | `int` | `3` | Independent analysis passes per dimension |
| `totems.task.dimensions` | `[]string` | `[...]` | Analysis dimensions for each pass |
| `totems.doc.passes` | `int` | `3` | Independent fact-gathering passes per aspect |
| `totems.doc.client` | `string` | `""` | Cover: who the document is for (`--set client=…` overrides) |
| `totems.doc.author` | `string` | `""` | Cover "prepared by" and the signature (`--set author=…`) |
| `totems.doc.contact` | `string` | `""` | Signature contact line (`--set contact=…`) |
| `totems.doc.brand` | `string` | `""` | Logo text, brand tag and cover footer (`--set brand=…`) |
| `totems.doc.locale` | `string` | `"en"` | The document's language tag (`--set locale=…`) |
| `rules` | `[]string` | `[...]` | Project rules injected into prompts |
| `projectRoot` | `string` | `"."` | Sandbox root for the file tools |
| `budget.maxTokens` | `int` | `2000000` | Hard token ceiling |
| `budget.maxSteps` | `int` | `500` | Hard step ceiling |
| `budget.maxUSD` | `float64` | `null` | Hard cost ceiling (null = unlimited) |
| `costPer1MInput` | `float64` | `0` | Input price per 1M tokens (set it for maxUSD to take effect) |
| `costPer1MOutput` | `float64` | `0` | Output price per 1M tokens (set it for maxUSD to take effect) |
| `streamTimeout` | `int` | `120` | Longest silence, in seconds, allowed from the provider during a streamed call (an idle timeout, not a deadline: an answer that keeps streaming is never cut) |
| `contextWindow` | `int` | from the model | The model's context size in tokens. ouija cuts each tool result to about a quarter of it and shrinks older history to fit. By default the value comes from the model catalog (4096 for Ollama, its default `num_ctx`; raise it here if your Ollama runs a larger one) |
| `tools.bash.allowlist` | `[]string` | `null` | Pre-approved bash commands that run without a prompt; the guard rail when the `bash` level is `allow` (see below) |
| `tools.bash.sandbox` | `object` | off | OS-level confinement of every bash command (Linux Landlock); see below |
| `modelSuggestions` | `map[string][]string` | `null` | Suggested models per provider for the TUI picker autocomplete |
| `permission` | `PermissionRule` | `null` | Permission defaults (see below) |

Configs written before `totems` existed keep working: ouija reads a top-level
`passes` / `dimensions` as `totems.task` (and `passes` as `totems.doc`) unless
those sections exist, and the next save writes the new layout.

### `permission` field

Controls the interactive approval gate for tools; set it in the project
config. When it is null, the chat TUI runs the read-only tools (`read_file`,
`list_dir`, `grep`, `glob`) without a prompt and asks before `write_file`,
`edit_file`, `delete_file` and `bash`.

```json
{
  "permission": {
    "default": "ask",
    "tools": {
      "read_file": "allow",
      "bash": "deny"
    }
  }
}
```

| Field | Type | Default | Description |
|---|---|---|---|
| `default` | `string` | `"ask"` | Default level: `"deny"`, `"ask"`, or `"allow"` |
| `tools` | `map[string]string` | `{}` | Per-tool overrides (tool name → level) |

Levels:

| Level | Behaviour |
|---|---|
| `"deny"` | The tool is blocked at once and the model gets an error |
| `"ask"` | Needs interactive approval (a prompt in the TUI) |
| `"allow"` | Runs without confirmation |

Protected paths are always denied, whatever the configured level. ouija checks
both the tool's `path` argument and any path a bash command names. The
protected paths are the system dirs `/etc/*` and `/var/lib/*`; the files `.env*`, `.gitconfig`, `.netrc`,
`.npmrc`, `.dockercfg`, `.dockerconfigjson`; and the dirs `.ssh`, `.gnupg`,
`.aws`, `.docker`, `.kube`, `.azure`, `.ouija`, `.config/gcloud`, `.config/kube`.

### `tools.bash.sandbox`

```json
{
  "tools": {
    "bash": {
      "sandbox": {
        "enabled": true,
        "read": ["~/go/pkg/mod"],
        "write": ["~/.cache/go-build"],
        "network": { "connect": [443] }
      }
    }
  }
}
```

The kernel confines every bash command (Linux, Landlock ABI v9+, which also
confines Unix sockets and signals outside the sandbox). A command writes only
inside the project and the temp dir, and reads only the system directories
(`/usr`, `/etc`, `/lib`, …), the project and the paths listed in `read` /
`write` (`~/` is your home; relative paths are relative to the project). The
rest of `$HOME` (`~/.ssh`, `~/.aws`, ouija's own config) stays unreachable,
whatever the command or its subprocesses do, even through a path built at run
time.

The sandbox is off by default because build tools keep caches under `$HOME`;
list those caches (e.g. `~/.cache/go-build`, `~/go/pkg/mod`; `/dev/shm` for
Python multiprocessing or browser-based tests). ouija refuses a protected
location in `read`/`write`, or one that contains a credential directory such
as `~` or `~/.config`, and it refuses to run with such a project root.
`~/.gitconfig` is protected, so git inside the sandbox runs without your
global identity: put it in `~/.config/git/config` and list that, or set it in
the repository. The project itself (its `.env` and `.git` included) stays
readable and writable; see
[what the sandbox does not cover](../explanation/security.md#os-sandbox-opt-in).
Where the OS cannot enforce the sandbox (macOS, Windows, an older kernel), bash
refuses to run rather than run unconfined.

TCP is closed by default: a confined command can neither open a connection
(not even to `localhost`) nor listen on a port. `network` opens what a project
needs:

| Key | Type | Effect |
|---|---|---|
| `connect` | `[]int` | TCP ports the command may connect to, e.g. `[443]` to download dependencies over HTTPS |
| `bind` | `[]int` | TCP ports the command may listen on, e.g. a dev server's port |
| `all` | `bool` | no network restriction |

Landlock filters TCP by port, not by host: `connect: [443]` allows HTTPS to any
server. It does not filter UDP, so DNS lookups still go out.

### `tools.bash.allowlist`

Pre-approves bash commands so they run without a prompt. Each entry is a base
command or an exact command prefix.

```json
{
  "tools": {
    "bash": {
      "allowlist": ["npm run build", "composer install"]
    }
  }
}
```

Semantics:

- A matching command runs with **no approval prompt** (at `ask` level).
- At `allow` level, a **non-empty** list is the **guard rail**: ouija refuses
  any command outside it, because `allow` means unattended and nothing else
  would check the command.
- An **empty list means "no restriction configured"**: at `ask` level every
  command still prompts; at `allow` level every command runs.
- A multi-word entry names an exact action, which only bare switches
  (`--prod`, `-v`) may extend: never operands, valued flags (`--out=x`) or
  anything after `--`.
- A **Session** approval of a command in the TUI adds it here and persists the
  file. A plain **Yes** runs that one command and is not persisted.

The full check order is:

1. **Denylist** (hardcoded + semantic analysis) — hard rule, before any prompt
2. **Protected-path reference** in the command — also before any prompt
3. **Permission gate** (deny/ask/allow); at `ask`, an allowlist match skips the prompt
4. **Allowlist enforcement** (only at `allow` level, where it is the guard rail)
5. **Execution**

#### Denylist

Catastrophic commands are **always blocked**, before any other check:

- `rm -rf /`, `rm -rf /*`, `rm -rf ~`, `rm -rf $HOME` — including variants with
  reordered flags (`rm -fr /`), variants reached through a wrapper
  (`sudo rm -fr /`, `bash -c 'rm -rf /'`, `eval '…'`), and `--recursive --force`
- `dd of=/dev/...` (raw device writes)
- `mkfs`/`mkfs.*`, `fdisk`, `parted`, `wipefs`, `blkdiscard` on `/dev/*`
- Fork bombs (`:(){ :|:& };:`, whitespace-insensitive)
- `chmod -R … /`, `chown -R … /` (recursive permission changes on the root)

Static patterns match whole tokens (**token-level matching**) to avoid false
positives: `rm -rf /tmp` is allowed, `rm -rf /` is blocked. Semantic analysis
covers the bypasses above, so approving a denylisted command can never
authorise it.

> **Accepted false positives:** conservative matching can also refuse a command
> that only *mentions* a dangerous operand, e.g. `git commit -m
> "rm -rf /"` or `grep mkfs scripts/…`. The model gets a clear error and can
> rephrase; the denylist never fails open.

#### Prefix matching

A multi-word pattern like `"npm run build"` is an **exact action**: it matches
`npm run build` and `npm run build --prod` (extra flags) but not
`npm run build ./dist` (a new operand, so a different action) or `npm run dev`.
A bare `"git"` matches any `git ...` command.

## Merge priority

```
project config > global config > built-in defaults
```

A field missing from a higher-priority layer is inherited from the layer
below. Validation runs on the merged result.

---

🔮 ouija docs — [Index](../index.md) · [Trace schema](trace-schema.md)
