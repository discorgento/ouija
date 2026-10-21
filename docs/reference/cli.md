🔮 ouija docs · [Index](../index.md)

---

# CLI reference

> Full reference for the `ouija` command (including `ouija mcp`) and the
> standalone `ouija-mcp` binary. Audience: reference

## ouija

### `ouija`

On a terminal, opens the chat (`ouija chat`), running `ouija setup` first if
no global config exists yet. When piped or scripted, it prints the help, like
`ouija help`.

### `ouija help`

Prints the totem list and usage summary.

### `ouija version`

```
ouija 1.2.3   # 0.0.0 for a build from source before the first release
```

`ouija -v` / `ouija --version` are aliases.

### `ouija setup`

Interactive wizard for the global config, in three steps:

1. The provider, each with a one-line description.
2. That provider's API key, with the page to get one. Ollama needs no key, so
   the wizard skips this step. With a key already saved, a blank field keeps it.
3. The default model, listed from the provider's API with the key just typed.
   Suggested models come first, marked ★; type `/` to search a long list.

Writes the global config (`0600`): `~/.config/ouija/config.json` on Linux,
`~/Library/Application Support/ouija/config.json` on macOS,
`%AppData%\ouija\config.json` on Windows.

### `ouija init`

Interactive wizard for the project config. Reads the global defaults and lets
you override the provider, model and API key. Needs an existing global config,
so run `ouija setup` first.

Writes `.ouija/config.json` in the current directory and trusts it (see
`ouija trust`).

### `ouija trust [--revoke] [<path>]`

Trusts the project config that discovery finds from the current directory (or
from `<path>`). It first prints the security-sensitive keys the file sets
(`permission`, the `tools` bash allowlist, `apiKey`, a non-default
`systemPrompt`, a `projectRoot` other than `.`). Until you trust the file,
ouija ignores those keys with a warning, and any change to the file needs
`ouija trust` again. `--revoke` forgets the trust. Files written by
`ouija init` and by the chat are trusted automatically. See
[Project trust](../explanation/security.md#project-trust).

### `ouija config`

Alias for `ouija setup`.

### `ouija generate totem`

Interactive scaffold for a new totem (an agent workflow you run with
`ouija <totem>`). Prompts for a name and description, writes
`totems/<name>/<name>.go`, then runs `go generate ./totems/` and
`go install ./cmd/ouija`.

With `--external [dir]`, it scaffolds a standalone Go module instead, for
agents that live in your own repository rather than in ouija's: `go.mod`, the
totem, a one-line `cmd/<name>/main.go` host on `bootstrap.Main`, `README.md`
and `.gitignore`. It then runs `go mod tidy` and builds the binary.

```sh
ouija generate totem --external [dir]
```

### `ouija learn [<fact>]`

With a fact, saves it to `docs/knowledge.md`; with no argument, prints the
current knowledge. ouija injects the facts into the chat's system prompt and
into the `task` totem's scout (the agent that first surveys the code).

### `ouija doctor`

Checks the local setup: binary location, global and project config files,
resolved provider/model, API key presence, registered totems, and harness
registration status.

```sh
ouija doctor
```

The output is a checklist with ✓ (ok), ! (warning) and ✗ (problem). The exit
code is 0 when there are no problems, and 1 when it finds at least one (e.g. a
missing API key for a provider that needs one, an invalid config, no totems
registered).

### `ouija chat [--config <file>] [--trace-file <file>]`

Interactive terminal UI (TUI, built with Bubble Tea): a chat that knows the
project, with file tools, write and exec tools (subject to approval), token
streaming and conversation history.

| Flag | Default | Description |
|---|---|---|
| `--config` | `""` | Explicit config path |
| `--trace-file` | `""` | Write a Perfetto/Chrome Trace JSON file |
| `--continue`, `-c` | `false` | Resume this project's latest conversation (`/sessions` picks an older one) |

ouija saves each conversation after every reply, with mode 0600, under the
user cache directory (`ouija/sessions/`, one directory per project, keeping the
latest 30). A conversation holds what the tools read, so ouija never writes it
into the project.

When the TUI exits, ouija always prints the session's span tree (the timed
steps of the session) to stderr, followed by a summary card: time, messages,
tokens, model, cost (when pricing is set) and the files changed.
`--trace-file` also writes the Perfetto JSON file.

#### Slash commands (inside the TUI)

| Command | Description |
|---|---|
| `/clear` | Clear chat history |
| `/retry` | Ask the last message again; the new reply replaces the last one (files already changed stay changed) |
| `/changes` | List the files ouija changed in this session, with their `+N −M` |
| `/<totem> [what]` | Run a totem inside the chat: `/task <what>`, `/doc <what>`, `/sticker`, or any totem you register. The chat draws its runs live, then shows the result (`task` also saves the plan to `docs/task-plan.md`) |
| `/learn [fact]` | Teach ouija a fact about the project (saved to `docs/knowledge.md` at the project root, read by every later chat and `/task`); alone, list what it knows |
| `/sessions` | Resume an earlier conversation of this project |
| `/theme [name]` | Pick the color theme (`ouija`, `catppuccin`, `dracula`, `mono`) from a list with each one's colors. It applies at once: the chat restarts and restores the conversation (on Windows, it applies at the next start) |
| `/undo` | Undo ouija's latest file change: a `write_file`, `edit_file` or `delete_file` on a file, or an edit made in the editor. It cannot undo `bash` commands or a deleted directory. Repeat to go further back |
| `/save [file]` | Export the conversation to Markdown |
| `/model [name]` | Show or switch the model (with an interactive picker) |
| `/provider [name] [model]` | Show or switch the provider and model; Enter on `/provider` or `/provider <name>` opens a picker, so you always choose the model |
| `/permit [tool] [allow\|ask\|deny]` | Show or set a tool's permission level |
| `/debug` | Toggle debug logging |
| `/help` | Show all commands and keyboard shortcuts |

#### Keyboard shortcuts

| Shortcut | Action |
|---|---|
| `Esc` | Cancel the generation / clear the queue of pending messages |
| `Ctrl+Y` | Copy the last assistant response to the clipboard |
| `Ctrl+T` | Show or hide each turn's tool calls one per line, with the span tree |
| `Alt+Y` | Copy the last code block to the clipboard |
| `Ctrl+F` | Search the conversation: matches highlighted, `Enter`/`↓` next, `↑` previous, `Esc` close |
| `Ctrl+R` | Search the messages you sent; `Enter` puts one back in the input box |
| `@` | Attach a project file to the message: a picker of the project's files, filtered as you type |
| `Ctrl+S` | Select mode: release the mouse so you can select text (press again to scroll with the wheel) |
| `Up` (empty input) | Load your last message for editing |
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
`edit_file`, `delete_file`, `bash`). Configure it per tool with `/permit` or in
the project config.

### `ouija <totem> [task] [--set <key>=<value>]… [--config <file>] [--trace-file <file>] [--json]`

Runs a registered totem. The totem's input schema decides whether the task
is required: `sticker` and `review` run without one, while `task` and `doc`
reject a missing task before running. `--set key=value` (repeatable) passes a
named input that the totem declares, typed by its input schema. A `string`
input keeps the text as typed (`--set ticket=1234`); other inputs are read as
JSON (`3`, `true`, `["a","b"]`) when they parse, and as a string otherwise.

Registered totems: `task` (analyses the code, then writes a plan), `review`
(turns the branch's changes into verified findings), `doc` (a client document
verified against the code), `sticker` (a laptop sticker from project metrics).
`ouija help` lists them all.

| Flag | Default | Description |
|---|---|---|
| `--config` | `""` | Explicit config path |
| `--trace-file` | `""` | Write a Perfetto/Chrome Trace JSON file |
| `--json` | `false` | Print the raw JSON result |

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success (also for `<command> --help`, which only prints the usage) |
| `1` | Config error or run failure |
| `2` | Usage error: unknown command or totem (with a suggestion for a typo), invalid flag, unexpected argument |

### Stderr output

- Config path: `[ouija] config: /path/to/.ouija/config.json`
- Error: `[ouija] error: <message>`
- Stats: `\n[ouija] <provider> · <model> · <tokens> tokens · <steps> steps` — the ` · $<cost>` suffix appears only when cost pricing is set

## ouija mcp

Serves MCP (Model Context Protocol) over stdio as JSON-RPC 2.0, or registers
the server with the agent harnesses (OpenCode, Claude Code, Cursor) it finds
on this machine.

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
| Tool prefix | `ouija_<totem>` (e.g. `ouija_task`, `ouija_review`, `ouija_doc`, `ouija_sticker`) |
| Transport | stdio |
| Budget | The engine budget applies to each call |

`ouija mcp install` touches only the harnesses it detects (OpenCode, Claude
Code, Cursor), writes a `.bak` before it edits any config file, and never
overwrites an existing entry. You can safely re-run both `install` and
`status`.

### Tools exposed

Each registered totem becomes an MCP tool named `ouija_<name>`, with a single
`task` string argument. Config discovery follows the same rules as the CLI.

### Resources exposed

The server exposes a `ouija://project/{path}` resource that reads any file
within the project root, giving editor agents direct access to project files:

| URI pattern | Example | Returns |
|---|---|---|
| `ouija://project/{path}` | `ouija://project/src/main.go` | File contents |
| `ouija://project/` | `ouija://project/` | Project root listing |

The resource is sandboxed: `engine.SafeResolve` resolves its paths, blocking
both lexical escapes (`../../etc/passwd`) and symlinks that point out of the
project root, and it refuses protected paths (`.env`, `.ssh/*`, `/etc/*`) the
same way the built-in tools do.

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
