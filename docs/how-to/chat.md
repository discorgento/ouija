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

- **Token streaming** — the reply flows in about 20 times a second, even in a
  long conversation
- **Live tools** — while the turn runs, one line counts its tool calls
  (`◇ 4 tools · 3 ✓ · 1 awaiting your approval`); `Ctrl+T` lists them as they
  start and finish
- **Tool progress** — shows which tool is currently executing (e.g.
  `◎ read_file src/Foo.php`), the seconds spent and the tokens streamed so far,
  in a persistent status line between the viewport and the input — always
  visible, never scrolls away
- **Context gauge** — the header shows how full the model's context window is
  (`ctx ▰▰▱▱▱▱ 32%`) when the window is known
- **Budget bar** — visual progress bar in the footer for the most restrictive
  budget ceiling (USD or tokens), shown only when one is set
- **Queue while generating** — keep typing while the model responds; messages
  queue up and auto-send when the model finishes
- **Cancel** — `Esc` while generating cancels the current response; `Esc` while
  idle clears the queue
- **Edit last message** — press `Up` with empty input to reload the last message

## After each turn

- **Tool calls** — one line under the reply's header sums the turn's tools up
  (`◇ list_dir .  ✓`, or `◇ 3 tools read_file ×2, bash  ✗ 1 failed`); `Ctrl+T`
  lists each call — target, `✓`/`✗`, duration, why a failed one failed — and
  the span tree. The header shows the reply's time, tokens and cost
- **Diffs** — each file a turn changed is shown framed, with its `+N −M`, line
  numbers and the removed and added lines on red and green
- **Code** — code blocks in replies are syntax-highlighted; `Alt+Y` copies the
  last one
- **Cost** — each reply's header shows how long it took and what it cost
- **Errors** — a failed turn shows in a red card that names the failure (key
  refused, budget spent, network…) and the next step
- **Notification** — when a reply took over 10 seconds and the terminal is in
  the background, ouija asks for a desktop notification (OSC 9 / OSC 777:
  iTerm2, kitty, WezTerm, foot, GNOME terminals). The tab title shows what ouija
  is doing

`Ctrl+F` searches the conversation and highlights the matches; `/retry` asks
the last message again; `/changes` lists the files changed this session;
`/undo` takes ouija's latest file change back (repeat to go further — `bash`
commands are not covered). `@path` in a message attaches that file: typing `@`
opens a picker of the project's files. `/task <description>` runs the `task`
totem right here — scout, passes and reconcile drawn live — and shows the plan.
Paths in tool lines, diffs and approvals are links (OSC 8): a click opens the
file. Each conversation is saved; `ouija chat -c` (or `/sessions`) resumes one.
`/theme` picks the colors (ouija, catppuccin, dracula, mono) and applies them
at once — the chat restarts with the conversation restored. A diff shows old
and new side by side on a terminal 140 columns wide or more. On
exit, a card sums the session up: time, messages, tokens, cost and the files
ouija changed.

Scrolled up while a reply arrives, the status bar counts the new lines
(`↓ 3 new · End`). Mouse text selection works in select mode (`Ctrl+S`), or with
`Shift`+drag in most terminals.

## Approving tool calls

By default, read-only tools (`read_file`, `list_dir`, `grep`, `glob`) run
without a prompt; write and exec tools (`write_file`, `edit_file`,
`delete_file`, `bash`) require approval (`ask`). The prompt says what the call
asks in words ("ouija wants to edit `engine/budget.go`"), framed in the color of
its risk (green reads, amber writes, red shell and anything outside the
project), with the command — whole, wrapped, never cut — or the diff it would run or
apply, and the other arguments. Up to five options, navigable with arrow keys:

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

`/model` and `/provider` open an interactive picker with context-window sizes —
the models come from each provider's own API (cached for a day in
`~/.cache/ouija/models/`), with a built-in list as the offline fallback
(typing the trailing space opens the inline suggestion list). `/clear` wipes
history when the context has grown too large.

## Exiting

Press `Ctrl+C` or `Ctrl+D` at any time, or type `exit` / `quit` and press Enter.
After the TUI exits, the session span tree is rendered to stderr, followed by a
one-line summary (messages · tokens · steps, plus cost when pricing is set).

---

🔮 ouija docs — [Index](../index.md) · [Run a totem](run-totems.md)
