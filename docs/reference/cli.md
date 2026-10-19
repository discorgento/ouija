🔮 ouija docs · [Index](../index.md)

---

# CLI reference

> Full reference for the `ouija` command (including `ouija mcp`) and the
> standalone `ouija-mcp` binary. Audience: reference

## ouija

### `ouija`

On a terminal, opens the chat (`ouija chat`); the first time — no global
config yet — it runs `ouija setup` first. Piped or scripted, it prints the
help, as `ouija help` does.

### `ouija help`

Prints the totem list and usage summary.

### `ouija version`

```
ouija 1.2.3   # 0.0.0 for a build from source before the first release
```

`ouija -v` / `ouija --version` are aliases.

### `ouija setup`

Interactive global config wizard. Prompts for:

1. API keys per provider (Ollama skipped)
2. Default provider and model

Writes to the global config (`0600`): `~/.config/ouija/config.json` on Linux,
`~/Library/Application Support/ouija/config.json` on macOS,
`%AppData%\ouija\config.json` on Windows.

### `ouija init`

Interactive per-project config wizard. Reads global defaults and lets you
override provider, model, and API key. Requires an existing global config —
run `ouija setup` first.

Writes to `.ouija/config.json` in the current directory, and trusts it (see
`ouija trust`).

### `ouija trust [--revoke] [<path>]`

Trusts the project config discovery finds from the current directory (or
`<path>`), after printing the security-sensitive keys it sets (`permission`,
`tools` bash allowlist, `apiKey`, a non-default `systemPrompt`, a `projectRoot`
other than `.`). Until then
those keys are ignored with a warning; any change to the file needs
`ouija trust` again. `--revoke` forgets the trust. Files written by
`ouija init` and by the chat are trusted automatically. See
[Project trust](../explanation/security.md#project-trust).

### `ouija config`

Alias for `ouija setup`.

### `ouija generate totem`

Interactive scaffold for a new totem. Prompts for name and description, writes
`totems/<name>/<name>.go`, then runs `go generate ./totems/` and
`go install ./cmd/ouija`.

With `--external [dir]` it scaffolds a standalone Go module instead — `go.mod`,
the totem, a one-line `cmd/<name>/main.go` host on `bootstrap.Main`, `README.md` and
`.gitignore` — then runs `go mod tidy` and builds the binary. For agents that
live in your own repo, not in ouija's.

```sh
ouija generate totem --external [dir]
```

### `ouija learn [<fact>]`

With a fact, saves it to `docs/knowledge.md`; without arguments, prints the
current knowledge. Facts are injected into the chat's system prompt and the
`task` totem's scout.

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
| `--continue`, `-c` | `false` | Resume this project's latest conversation (`/sessions` picks an older one) |

Each conversation is saved after every reply, 0600, under the user cache
directory (`ouija/sessions/`, one directory per project, the latest 30 kept):
it holds what the tools read, so it is never written into the project.

After the TUI exits, the session span tree is always rendered to stderr, followed
by a card summing the session up (time, messages, tokens, model, cost when
pricing is set, the files changed).
`--trace-file` additionally writes the Perfetto JSON file.

#### Slash commands (inside the TUI)

| Command | Description |
|---|---|
| `/clear` | Clear chat history |
| `/retry` | Ask the last message again; the new reply replaces the last one (files already changed stay changed) |
| `/changes` | List the files ouija changed in this session, with their `+N −M` |
| `/<totem> [what]` | Run a totem inside the chat — `/task <what>`, `/doc <what>`, `/sticker`, and any totem you register — its runs drawn live, then its result (`task` also saves the plan to `docs/task-plan.md`) |
| `/learn [fact]` | Teach ouija a fact about the project (saved to `docs/knowledge.md` at the project root, read by every later chat and `/task`); alone, list what it knows |
| `/sessions` | Resume an earlier conversation of this project |
| `/theme [name]` | Pick the color theme (`ouija`, `catppuccin`, `dracula`, `mono`) from a list with each one's colors. It applies at once: the chat restarts with the conversation restored (on Windows, at the next start) |
| `/undo` | Take back ouija's latest file change — `write_file`, `edit_file`, `delete_file` on a file, an edit from the editor; not `bash` commands nor a deleted directory. Repeat to go further back |
| `/save [file]` | Export conversation to Markdown |
| `/model [name]` | Show or switch model (with interactive picker) |
| `/provider [name] [model]` | Show or switch provider and model; Enter on `/provider` or `/provider <name>` opens a picker, so you always choose the model |
| `/permit [tool] [allow\|ask\|deny]` | Show or set permission level |
| `/debug` | Toggle debug logging |
| `/help` | Show all commands and keyboard shortcuts |

#### Keyboard shortcuts

| Shortcut | Action |
|---|---|
| `Esc` | Cancel generation / clear pending queue |
| `Ctrl+Y` | Copy last assistant response to clipboard |
| `Ctrl+T` | Show or hide each turn's tool calls one per line, with the span tree |
| `Alt+Y` | Copy the last code block to clipboard |
| `Ctrl+F` | Search the conversation: matches highlighted, `Enter`/`↓` next, `↑` previous, `Esc` close |
| `Ctrl+R` | Search the messages you sent; `Enter` puts one back in the input box |
| `@` | Attach a project file to the message: a picker of the project's files, filtered as you type |
| `Ctrl+S` | Select mode: release the mouse to select text (again to scroll with the wheel) |
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

### `ouija <totem> [task] [--set <key>=<value>]… [--config <file>] [--trace-file <file>] [--json]`

Run a registered totem. Whether the task is required comes from the totem's
input schema: `sticker` runs without one, while `task` and `doc` reject a
missing task before running. `--set key=value` (repeatable) passes a named input
the totem declares, typed by its input schema: a `string` input keeps the text
as typed (`--set ticket=1234`), others are read as JSON (`3`, `true`,
`["a","b"]`) when they parse, else as a string.

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
| `0` | Success (also `<command> --help`, which only prints the usage) |
| `1` | Config error or run failure |
| `2` | Usage error: unknown command or totem (with a suggestion for a typo), invalid flag, unexpected argument |

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
