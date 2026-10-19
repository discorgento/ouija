🔮 ouija docs · [Index](../index.md)

---

# CLI reference

> Full reference for the `ouija` command (including `ouija mcp`) and the
> standalone `ouija-mcp` binary. Audience: reference

## ouija

### `ouija help`

Prints the totem list and usage summary.

### `ouija version`

```
ouija 1.0.0
```

`ouija -v` / `ouija --version` are aliases.

### `ouija setup`

Interactive global config wizard. Prompts for:

1. API keys per provider (Ollama skipped)
2. Default provider and model

Writes to `~/.config/ouija/config.json` (`0600`).

### `ouija init`

Interactive per-project config wizard. Reads global defaults and lets you
override provider, model, and API key. Requires an existing global config —
run `ouija setup` first.

Writes to `.ouija/config.json` in the current directory.

### `ouija config`

Alias for `ouija setup`.

### `ouija generate totem`

Interactive scaffold for a new totem. Prompts for name and description, writes
`totems/<name>/<name>.go`, then runs `go generate ./totems/...` and
`go install ./cmd/ouija`.

With `--external [dir]` it scaffolds a standalone Go module instead — `go.mod`,
the totem, a `cmd/<name>/main.go` host on `bootstrap.Setup`, `README.md` and
`.gitignore` — then runs `go mod tidy` and builds the binary. For agents that
live in your own repo, not in ouija's.

```sh
ouija generate totem --external [dir]
```

### `ouija learn [<fact>]`

With a fact, saves it to `docs/knowledge.md`; without arguments, prints the
current knowledge. Facts are injected into every agent prompt.

### `ouija doctor`

Diagnoses the local setup: binary location, global and project config files,
resolved provider/model, API key presence, registered totems, and harness
registration status.

```sh
ouija doctor
```

Output is a checklist with ✓ (ok), ! (warning), ✗ (problem). Exit code is 0
when there are no problems; 1 when at least one problem is found (e.g. missing
API key for a non-keyless provider, invalid config, no totems registered).

### `ouija chat [--config <file>] [--trace-file <file>]`

Interactive Bubble Tea TUI. Project-aware chat with file tools, write/exec
tools (subject to approval), token streaming, and conversation history.

| Flag | Default | Description |
|---|---|---|
| `--config` | `""` | Explicit config path |
| `--trace-file` | `""` | Write a Perfetto/Chrome Trace JSON file |

After the TUI exits, the session span tree is always rendered to stderr, followed
by a one-line summary (messages · tokens · steps, plus cost when pricing is set).
`--trace-file` additionally writes the Perfetto JSON file.

#### Slash commands (inside the TUI)

| Command | Description |
|---|---|
| `/clear` | Clear chat history |
| `/save [file]` | Export conversation to Markdown |
| `/model [name]` | Show or switch model (with interactive picker) |
| `/provider [name]` | Show or switch provider (with interactive picker) |
| `/permit [tool] [allow\|ask\|deny]` | Show or set permission level |
| `/debug` | Toggle debug logging |
| `/help` | Show all commands and keyboard shortcuts |

#### Keyboard shortcuts

| Shortcut | Action |
|---|---|
| `Esc` | Cancel generation / clear pending queue |
| `Ctrl+Y` | Copy last assistant response to clipboard |
| `Up` (empty input) | Load last user message for editing |
| `Alt+Up` / `Alt+Down` | Cycle through input history |
| `Shift+Enter` / `Alt+Enter` | Insert a newline |
| `Tab` | Accept the highlighted slash-command/autocomplete entry |
| `PgUp` / `PgDn` / `Home` / `End` | Scroll the transcript |
| `←` / `→` then `Enter` | In the approval prompt: pick Yes / No / Session / Edit / Cancel |
| `Ctrl+C` / `Ctrl+D` | Exit |

#### Tool categories

| Risk | Tools |
|---|---|
| Read-only | `read_file`, `grep`, `glob`, `list_dir`, `submit_result` |
| Write | `write_file`, `edit_file`, `delete_file` |
| Exec | `bash` |

Default permission: **allow** for the read-only tools (`read_file`, `grep`,
`glob`, `list_dir`) and **ask** for the write/exec tools (`write_file`,
`edit_file`, `delete_file`, `bash`). Configurable per-tool via `/permit` or the
project config.

### `ouija <totem> [task] [--config <file>] [--trace-file <file>] [--json]`

Run a registered totem. The task is optional at the CLI level: `sticker` runs
without one, while `task` and `doc` reject an empty task with an error.

Registered totems: `task` (analysis → plan), `doc` (code-verified client
document), `sticker` (laptop sticker from project metrics). `ouija help` lists
them all.

| Flag | Default | Description |
|---|---|---|
| `--config` | `""` | Explicit config path |
| `--trace-file` | `""` | Write a Perfetto/Chrome Trace JSON file |
| `--json` | `false` | Print raw JSON result |

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success |
| `1` | Config error, unknown totem, or run failure |
| `2` | Invalid flag, or an unknown `ouija mcp` subcommand |

### Stderr output

- Config path: `[ouija] config: /path/to/.ouija/config.json`
- Error: `[ouija] error: <message>`
- Stats: `\n[ouija] <provider> · <model> · <tokens> tokens · <steps> steps` — the ` · $<cost>` suffix appears only when cost pricing is tracked

## ouija mcp

Serves MCP over stdio (JSON-RPC 2.0), or wires the server into the agent
harnesses found on this machine.

```sh
ouija mcp            # serve MCP over stdio (what a harness launches)
ouija mcp install    # register ouija with OpenCode / Claude Code / Cursor
ouija mcp status     # show where ouija is connected
```

The standalone `ouija-mcp` binary runs the same server, for host configs that
launch it by name. `ouija-mcp --version` prints the version and exits without
starting the server.

| Feature | Detail |
|---|---|
| Protocol | JSON-RPC 2.0 over stdin/stdout |
| Tool prefix | `ouija_<totem>` (e.g. `ouija_task`, `ouija_doc`, `ouija_sticker`) |
| Transport | stdio |
| Budget | Engine budget applies per call |

`ouija mcp install` only touches harnesses it detects (OpenCode, Claude Code,
Cursor), writes a `.bak` before editing any config file, and never overwrites an
existing entry. Both `install` and `status` are safe to re-run.

### Tools exposed

Each registered totem becomes an MCP tool named `ouija_<name>` with a single
`task` string argument. Config discovery follows the same rules as the CLI.

### Resources exposed

The server exposes a `ouija://project/{path}` resource for reading any file
within the project root. Use it to give editor agents direct access to project
files:

| URI pattern | Example | Returns |
|---|---|---|
| `ouija://project/{path}` | `ouija://project/src/main.go` | File contents |
| `ouija://project/` | `ouija://project/` | Project root listing |

The resource is sandboxed — paths are resolved via `engine.SafeResolve`, which
blocks both lexical escapes (`../../etc/passwd`) and symlinks pointing out of the
project root, and refuses protected paths (`.env`, `.ssh/*`, `/etc/*`) the same
way the built-in tools do.

### MCP server configuration

```json
{
  "mcp": {
    "ouija": {
      "type": "local",
      "command": ["ouija", "mcp"],
      "enabled": true
    }
  }
}
```

---

🔮 ouija docs — [Index](../index.md) · [Config schema](config-schema.md)
