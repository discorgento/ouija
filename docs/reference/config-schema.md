🔮 ouija docs · [Index](../index.md)

---

# Config schema

> JSON structure for global and project configuration files. Audience: reference

## Global config: `~/.config/ouija/config.json`

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
    "model": "claude-opus-4-8"
  }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `apiKeys` | `map[string]string` | No | API keys per provider. Empty = not configured |
| `defaults.provider` | `string` | Yes (if defaults block) | Default provider name |
| `defaults.model` | `string` | Yes (if defaults block) | Default model ID |

Legacy flat `provider`/`model` fields at the top level are still read for
backward compatibility.

File permissions: `0600` (auto-fixed on load if loose).

## Project config: `.ouija/config.json`

```json
{
  "provider": "openrouter",
  "model": "opencode/big-pickle",
  "apiKey": "sk-alt-...",
  "permission": {
    "default": "ask",
    "tools": {
      "read_file": "allow",
      "glob": "allow"
    }
  },
  "modelSuggestions": {
    "anthropic": ["claude-sonnet-4-8"],
    "openai": ["gpt-4o"]
  }
}
```

| Field | Type | Required | Description |
|---|---|---|---|
| `provider` | `string` | No | Override default provider |
| `model` | `string` | No | Override default model |
| `apiKey` | `string` | No | Override API key (only when different from global) |
| `permission` | `PermissionRule` | No | Permission defaults (see engine config section) |
| `modelSuggestions` | `map[string][]string` | No | Suggested models per provider for the TUI picker autocomplete |

File permissions: `0600`.

## Engine config: `Config` struct

The merged config used at runtime is the `engine.Config` struct. It's the
combination of built-in defaults + global config + project config.

```json
{
  "provider": "anthropic",
  "model": "claude-opus-4-8",
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
  "modelSuggestions": {"anthropic": ["claude-sonnet-4-8"], "openai": ["gpt-4o"]},
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
| `provider` | `string` | `"anthropic"` | Active provider |
| `model` | `string` | `"claude-opus-4-8"` | Active model |
| `apiKey` | `string` | `""` | Project-level API key override |
| `systemPrompt` | `string` | built-in prompt ("You are an AI coding assistant with access to the project files…") | Overrides the default chat system prompt |
| `maxNudges` | `int` | `3` | Max retries when model fails to return structured output |
| `totems.<name>` | `object` | — | A totem's own settings, read with `Config.TotemConfig`; the engine never interprets them |
| `totems.task.passes` | `int` | `3` | Independent analysis passes per dimension |
| `totems.task.dimensions` | `[]string` | `[...]` | Analysis dimensions of each pass |
| `totems.doc.passes` | `int` | `3` | Independent fact-gathering passes per aspect |

Configs written before `totems` keep working: a top-level `passes` /
`dimensions` is read as `totems.task` (and `passes` as `totems.doc`), unless
those sections exist, and the next save writes the new layout.
| `rules` | `[]string` | `[...]` | Project rules injected into prompts |
| `projectRoot` | `string` | `"."` | Sandbox root for file tools |
| `budget.maxTokens` | `int` | `2000000` | Hard token ceiling |
| `budget.maxSteps` | `int` | `500` | Hard step ceiling |
| `budget.maxUSD` | `float64` | `null` | Hard cost ceiling (null = unlimited) |
| `costPer1MInput` | `float64` | `0` | Input price per 1M tokens (set it to make maxUSD effective) |
| `costPer1MOutput` | `float64` | `0` | Output price per 1M tokens (set it to make maxUSD effective) |
| `streamTimeout` | `int` | `120` | Streaming response timeout in seconds |
| `tools.bash.allowlist` | `[]string` | `null` | Pre-approved bash commands (no prompt); guard rail when `bash` level is `allow` (see below) |
| `modelSuggestions` | `map[string][]string` | `null` | Suggested models per provider for the TUI picker autocomplete |
| `permission` | `PermissionRule` | `null` | Permission defaults (see below) |

### `permission` field

Controls the interactive approval gate for tools. Can be set in the project
config. When left null, the chat TUI allows the read-only tools (`read_file`,
`list_dir`, `grep`, `glob`) without a prompt and asks for `write_file`,
`edit_file`, `delete_file`, and `bash`.

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
| `"deny"` | Tool is blocked immediately — model gets an error |
| `"ask"` | Requires interactive approval (prompt in TUI) |
| `"allow"` | Runs without confirmation |

Protected paths are always denied regardless of the configured level — enforced
both on the tool's `path` argument and on any path a bash command names: the
system dirs `/etc/*` and `/var/lib/*`; the files `.env*`, `.gitconfig`, `.netrc`,
`.npmrc`, `.dockercfg`, `.dockerconfigjson`; and the dirs `.ssh`, `.gnupg`,
`.aws`, `.docker`, `.kube`, `.azure`, `.ouija`, `.config/gcloud`, `.config/kube`.

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
- At `allow` level a **non-empty** list is the **guard rail**: a command outside
  it is refused (`allow` means unattended, so nothing else would check it).
- An **empty list means "no restriction configured"**: at `ask` level every
  command still prompts; at `allow` level every command runs.
- A **Session** approval of a command in the TUI adds it here and persists the
  file. A plain **Yes** runs that one command and is not persisted.

The full check order is:

1. **Denylist** (hardcoded + semantic analysis) — hard rule, before any prompt
2. **Protected-path reference** in the command — also before any prompt
3. **Permission gate** (deny/ask/allow); at `ask`, an allowlist match skips the prompt
4. **Allowlist enforcement** (only at `allow` level, where it is the guard rail)
5. **Execution**

#### Denylist

Catastrophic commands are **always blocked** before any other check:

- `rm -rf /`, `rm -rf /*`, `rm -rf ~`, `rm -rf $HOME` — including variants with
  the flags in another order (`rm -fr /`), reached through a wrapper
  (`sudo rm -fr /`, `bash -c 'rm -rf /'`, `eval '…'`) or via `--recursive --force`
- `dd of=/dev/...` (raw device writes)
- `mkfs`/`mkfs.*`, `fdisk`, `parted`, `wipefs`, `blkdiscard` on `/dev/*`
- Fork bombs (`:(){ :|:& };:`, whitespace-insensitive)
- `chmod -R … /`, `chown -R … /` (recursive permission changes on the root)

Static patterns use **token-level matching** to avoid false positives (e.g.
`rm -rf /tmp` is allowed, `rm -rf /` is blocked). Semantic analysis covers the
bypasses above, so a denylisted command can never be authorised by approving it.

> **Accepted false positives:** conservative matching means a command that only
> *mentions* a dangerous operand can also be refused — e.g. `git commit -m
> "rm -rf /"` or `grep mkfs scripts/…`. The model gets a clear error and can
> rephrase; the denylist never fails open.

#### Prefix matching

A multi-word pattern like `"npm run build"` is an **exact action**: it matches
`npm run build` and `npm run build --prod` (further flags) but not
`npm run build ./dist` (a new operand — a different action) nor `npm run dev`.
A bare `"git"` matches any `git ...` command.

## Merge priority

```
project config > global config > built-in defaults
```

Fields not present in a higher-priority layer are inherited from the layer
below. Validation runs on the merged result.

---

🔮 ouija docs — [Index](../index.md) · [Trace schema](trace-schema.md)
