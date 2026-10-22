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
| `sandbox` | `object` | No | The bash sandbox for **every** project, same keys as [`tools.bash.sandbox`](#toolsbashsandbox). Set, a project cannot turn it off; a trusted one may add paths and ports. `{"enabled": false}` turns the default sandbox off |
| `trustedProjects` | `map[string]string` | No | Written by `ouija trust`: each trusted project config's absolute path → the SHA-256 of the contents you trusted. Do not edit it by hand |

The bash sandbox is **on by default** wherever the OS can enforce it. Without
a `sandbox` key anywhere, ouija confines bash with the defaults below; where
the OS cannot (Windows, a Linux kernel older than Landlock ABI v9), bash runs
unconfined and asks before each command, and `ouija doctor` says which.

`sandbox` here makes the sandbox a floor for every project, trusted or not: a
project's own `tools.bash.sandbox` only applies after `ouija trust`, and with
`"sandbox": {"enabled": true}` in the global config a trusted project's
section only adds its `read`, `write` and `network` entries on top
(`enabled: false` there changes nothing). Set explicitly, the sandbox is
required: where the OS cannot enforce it, bash refuses to run.

For backward compatibility, ouija still reads the legacy flat
`provider`/`model` fields at the top level.

File permissions: `0600`. On Linux and macOS ouija tightens looser
permissions on load; on Windows it skips the check.

## Project config: `.ouija/config.json`

```json
{
  "provider": "openrouter",
  "model": "openrouter/free",
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

A discovered project config applies its **trust-only keys** only once you
trust the file with `ouija trust` (see
[project trust](../explanation/security.md#project-trust)). Until then ouija
ignores them and warns. The trust-only keys are:

- `permission`
- `tools` (the bash allowlist and the sandbox section)
- `apiKey`
- a `systemPrompt` or `rules` other than the built-in ones
- a `projectRoot` other than `"."`
- a `budget` above the defaults (an untrusted file may only lower it)
- a `provider` or `model` other than your `ouija setup` defaults

A config passed with `--config` is trusted, like any CLI flag, and so is a
file ouija writes itself (`ouija init`, a chat save).

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
| `projectRoot` | `string` | `"."` | Root for the file tools and the bash sandbox, relative to the config file's directory (the one holding `.ouija/`). Kept inside that directory (symlinks resolved); applied only from a trusted file |
| `budget.maxTokens` | `int` | `2000000` | Hard token ceiling |
| `budget.maxSteps` | `int` | `500` | Hard step ceiling |
| `budget.maxUSD` | `float64` | `null` | Hard cost ceiling (null = unlimited) |
| `costPer1MInput` | `float64` | `0` | Input price per 1M tokens (set it for maxUSD to take effect) |
| `costPer1MOutput` | `float64` | `0` | Output price per 1M tokens (set it for maxUSD to take effect) |
| `streamTimeout` | `int` | `120` | Longest silence, in seconds, allowed from the provider during a streamed call (an idle timeout, not a deadline: an answer that keeps streaming is never cut) |
| `contextWindow` | `int` | from the model | The model's context size in tokens. ouija cuts each tool result to about a quarter of it and shrinks older history to fit. By default the value comes from the model catalog (4096 for Ollama, its default `num_ctx`; raise it here if your Ollama runs a larger one) |
| `tools.bash.allowlist` | `[]string` | `null` | Pre-approved bash commands that run without a prompt; the guard rail when the `bash` level is `allow` (see below) |
| `tools.bash.sandbox` | `object` | on where supported | OS-level confinement of every bash command (Linux Landlock, macOS Seatbelt); see below |
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
protected paths are:

- the system dirs `/etc/*`, `/var/lib/*`, `/proc` and `/sys`, and on macOS
  `/private/etc/*` and `/private/var/lib/*`;
- the files `.env*`, `.gitconfig`, `.netrc`, `.npmrc`, `.dockercfg`,
  `.dockerconfigjson`, `.git-credentials`, `.pgpass`, `.pypirc`, `.my.cnf`,
  `.vault-token`, `id_rsa`, `id_dsa`, `id_ecdsa`, `id_ed25519`, `*.tfstate`,
  `*.tfstate.backup`, and ouija's own `.ouija.json` and `ouija.json`;
- the dirs `.ssh`, `.gnupg`, `.aws`, `.docker`, `.kube`, `.azure`, `.ouija`,
  `.password-store`, `.config/gcloud`, `.config/kube`, `.config/gh`, and
  ouija's global config dir;
- a repository's `.git/config` and `.git/hooks`;
- the token files inside tool dirs: `.cargo/credentials`,
  `.cargo/credentials.toml`, `.m2/settings.xml`, `.m2/settings-security.xml`,
  `.gradle/gradle.properties`, `huggingface/token`, `uv/credentials`,
  `.docker/config.json`, `git/credentials`,
  `.terraform.d/credentials.tfrc.json`, `rclone/rclone.conf`,
  `share/keyrings` and `etc/npmrc`.

### `tools.bash.sandbox`

```json
{
  "tools": {
    "bash": {
      "sandbox": {
        "enabled": true,
        "read": ["~/sdk"],
        "write": ["/dev/shm"],
        "network": { "connect": [443] }
      }
    }
  }
}
```

The OS confines every bash command: on Linux with Landlock (ABI v9+, which
also confines Unix sockets and signals outside the sandbox), on macOS with
Seatbelt (`sandbox-exec`). A command writes only inside the project, a private
temp dir (`TMPDIR`, one per ouija process) and a private build cache for the
project (`GOCACHE`, `npm_config_cache`, `PIP_CACHE_DIR`, `XDG_CACHE_HOME`, …
point into `ouija/sandbox/` in the user cache dir: `~/.cache/ouija/sandbox/`
on Linux, `~/Library/Caches/ouija/sandbox/` on macOS). Your real caches stay
read-only to it:
the tools you run later outside the sandbox trust them, so a confined command
that could write there could plant code that escapes. It reads only the system
directories (`/usr`, `/etc`, `/lib`, …), the project, the parts of the
toolchains in your home that builds read (`~/go/pkg/mod`, `~/.cargo/registry`,
`~/.rustup`, `~/.m2/repository`, `~/.local/bin`, …; for a version manager,
each installed version's `bin` and `lib`, such as
`~/.nvm/versions/node/*/{bin,lib}` or `~/.pyenv/versions/*/{bin,lib}`, not the
whole version dir, whose `etc` holds an `npmrc`; never the credentials next to
them such as `~/.cargo/credentials.toml` or `~/.m2/settings.xml`), git's
global ignore files
and the paths listed in `read` / `write` (`~/` is your home; relative paths are
relative to the project). The rest of `$HOME` (`~/.ssh`, `~/.aws`, ouija's own
config, your documents) stays unreachable, whatever the command or its
subprocesses do, even through a path built at run time.

List anything else a build needs (`/dev/shm` for Python multiprocessing or
browser-based tests, a toolchain in an unusual place). ouija refuses a
protected location in `read`/`write`, or one that contains a credential
directory such as `~` or `~/.config`, and it refuses to run with such a
project root. git in the sandbox cannot read `~/.gitconfig` (it may hold a
token), so ouija runs it with `GIT_CONFIG_GLOBAL=/dev/null` and passes on only
your `user.name` and `user.email`. The project itself (its `.env` and `.git`
included) stays readable and writable; see
[what the sandbox does not cover](../explanation/security.md#os-sandbox).
Because `.git` stays writable, ouija checks it after every confined command:
it puts back the hooks in `.git/hooks` as they were, and `.git/config` when
the command added an entry that runs a command (`core.fsmonitor`,
`core.hooksPath`, a `!` alias, a filter, …). Ordinary changes stay. The
command's result names what was put back.
When a confined command fails, its result says it ran in the sandbox, so a
denial is not mistaken for a bug. The model may then retry the same command
with `"unsandboxed": true`: that runs it outside the sandbox only after you
approve that exact command in a prompt marked **runs OUTSIDE the bash
sandbox**, whatever the permission level or the allowlist says, and never
where no one can approve (MCP, CI). `"allowUnsandboxed": false` turns that
escape hatch off; in the global config no project can turn it back on.

TCP is closed by default: a confined command can neither open a connection
(not even to `localhost`) nor listen on a port. `network` opens what a project
needs:

| Key | Type | Effect |
|---|---|---|
| `connect` | `[]int` | TCP ports the command may connect to, e.g. `[443]` to download dependencies over HTTPS |
| `bind` | `[]int` | TCP ports the command may listen on, e.g. a dev server's port |
| `all` | `bool` | no network restriction |

The filter is by port, not by host: `connect: [443]` allows HTTPS to any
server. Listing any port also lets names resolve (DNS, UDP 53); with no port
listed, Linux with Landlock ABI v10+ blocks DNS too (older kernels do not
filter UDP).

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
- A **Session** approval of a command in the TUI adds one pattern per simple
  command here and saves the file. A command with `$(…)` or a variable adds
  none, and neither does an approval to run outside the sandbox. ouija
  refuses to rewrite an untrusted config that sets trust-only keys, so the
  pattern then lasts only for the session. A plain **Yes** runs that one
  command and saves nothing.

The full check order is:

1. **Doom-loop check** — the same tool with identical input three times in a row is refused
2. **NUL byte** in the command — refused (`bash: the command contains a NUL byte`)
3. **Denylist** (hardcoded + semantic analysis) — hard rule, before any prompt
4. **Protected-path reference** in the command — also before any prompt
5. **`"unsandboxed": true`** — the permission level and the allowlist still
   apply; refused when `allowUnsandboxed` is `false` or there is no approval
   UI; otherwise always a prompt
6. **Permission gate** (deny/ask/allow); at `ask`, an allowlist match skips the prompt
7. **Allowlist enforcement** (only at `allow` level, where it is the guard rail)
8. **Execution**

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
below. An untrusted project file loses its trust-only keys before the merge.
A global `sandbox` is applied last, as a floor no project lowers. Validation
runs on the merged result.

---

🔮 ouija docs — [Index](../index.md) · [Trace schema](trace-schema.md)
