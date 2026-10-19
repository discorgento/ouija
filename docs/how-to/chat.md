🔮 ouija docs · [Index](../index.md)

---

# Use the chat TUI

> Working interactively with `ouija chat`: approvals, permissions, queueing.
> Audience: user

```sh
cd my-project && ouija chat
ouija chat --trace-file session.json
```

Full-screen terminal chat with tools, streaming tokens, conversation history,
tool progress display, budget progress bar, runtime permission prompts, and a
card-based layout with consistent backgrounds for every visual element.

Slash commands and keyboard shortcuts are listed once, in the
[CLI reference](../reference/cli.md#ouija-chat---config-file---trace-file-file) —
this page covers the workflows around them.

## What happens while the model works

- **Token streaming** — text renders character by character as the model responds
- **Tool progress** — shows which tool is currently executing (e.g.
  `read_file: src/Foo.php`) in a persistent status bar between the viewport and
  the input — always visible, never scrolls away
- **Budget bar** — visual progress bar in the footer for the most restrictive
  budget ceiling (tokens/steps/USD)
- **Queue while generating** — keep typing while the model responds; messages
  queue up and auto-send when the model finishes
- **Cancel** — `Esc` while generating cancels the current response; `Esc` while
  idle clears the queue
- **Edit last message** — press `Up` with empty input to reload the last message

## Approving tool calls

By default, read-only tools (`read_file`, `list_dir`, `grep`, `glob`) run
without a prompt; write and exec tools (`write_file`, `edit_file`,
`delete_file`, `bash`) require approval (`ask`). The prompt shows the tool icon,
name, call number, and all arguments. Five options, navigable with arrow keys:

- **Yes** (`y`) — approve this call once
- **No** (`n`) — reject; the model is told to try a different approach
- **Session** (`s`) — approve for the rest of the session, **scoped to this
  exact target** (the file path, or the exact bash command). For bash it also
  adds the command to the allowlist and persists it to the project config file
  (the discovered `.ouija/config.json`, `.ouija.json` or `ouija.json`, or the
  `--config` path)
- **Edit** (`e`) — open the proposed change in `$EDITOR` (falls back to `vi`) to
  adjust it before approving
- **Cancel** (`esc`) — cancels the generation

Some tools have a dedicated confirm key: `delete_file` asks for `d` (Delete)
instead of `y`. Hard rules cannot be approved away — a denylisted command or a
protected path (`.env`, `~/.ssh/*`, `/etc/*`) is refused *before* the prompt is
shown. Why, and which paths: [Security](../explanation/security.md).

Once a tool is rejected during a generation, all subsequent calls to that same
tool are auto-rejected. Use `/permit <tool> allow` to skip prompts for a
specific tool you trust.

## Trusting a command for good

A bash command approved with **Session** is written to
`tools.bash.allowlist` in the project config and survives restarts. A plain
**Yes** runs that one command and is not persisted. The allowlist also acts as
the guard rail for unattended runs (`permission.tools.bash: "allow"`) — see
[Config schema](../reference/config-schema.md#toolsbashallowlist).

## Change permission levels

```text
/permit                 → show every tool's level
/permit bash allow      → never prompt for bash
/permit write_file deny → block writes outright
```

The same levels can be set statically in `.ouija/config.json`
(`permission.default`, `permission.tools`) — see
[Configure](configure.md).

## Restart the model or the provider

`/model` and `/provider` open an interactive picker with context-window sizes
(typing the trailing space opens the inline suggestion list). `/clear` wipes
history when the context has grown too large.

## Exiting

Press `Ctrl+C` or `Ctrl+D` at any time, or type `exit` / `quit` and press Enter.
After the TUI exits, the session span tree is rendered to stderr, followed by a
one-line summary (messages · tokens · steps, plus cost when pricing is set).

---

🔮 ouija docs — [Index](../index.md) · [Run a totem](run-totems.md)
