🔮 ouija docs · [Index](../index.md)

---

# Use the chat TUI

> Working interactively with `ouija chat`: approvals, permissions, queueing.
> Audience: user

```sh
cd my-project && ouija chat
ouija chat --trace-file session.json
```

`ouija chat` opens a full-screen chat in your terminal. The model can use
tools on your project, its reply streams in as it is written, and ouija asks
your permission before any risky tool call.

The [CLI reference](../reference/cli.md#ouija-chat---continue---config-file---trace-file-file)
lists every slash command and keyboard shortcut. This page covers the
workflows around them.

## What happens while the model works

- **Token streaming** — the reply updates about 20 times a second, even in a
  long conversation.
- **Live tools** — while the turn runs, one line counts its tool calls
  (`◇ 4 tools · 3 ✓ · 1 awaiting your approval`). `Ctrl+T` lists the calls as
  they start and finish.
- **Tool progress** — a status line between the conversation and the input
  shows the running tool (e.g. `◎ read_file src/Foo.php`), the seconds spent
  and the tokens streamed so far. It stays put and never scrolls away.
- **Context gauge** — when the model's context window size is known, the
  header shows how full it is (`ctx ▰▰▱▱▱▱ 32%`).
- **Budget bar** — when you set a budget ceiling (USD or tokens), the footer
  shows progress toward the strictest one.
- **Queue while generating** — keep typing while the model responds. Your
  messages wait in a queue and send themselves when the model finishes.
- **Cancel** — `Esc` cancels the current response while the model is
  generating, and clears the queue while it is idle.
- **Edit last message** — press `Up` in an empty input to bring back your last
  message.

## After each turn

- **Commands' output** — a failed command shows the reason and its last lines
  under the reply, even with the details closed. `Ctrl+T` shows the last lines
  of every command.
- **New files** — when you approve a `write_file`, ouija shows the content it
  will write, numbered and highlighted like the diff of an edit.
- **Tool calls** — one line under the reply's header sums up the turn's tools
  (`◇ list_dir .  ✓`, or `◇ 3 tools read_file ×2, bash  ✗ 1 failed`). `Ctrl+T`
  lists each call — its target, `✓`/`✗`, duration and, for a failed call, the
  reason — plus the span tree.
- **Diffs** — ouija frames each file the turn changed and shows its `+N −M`,
  line numbers, and removed and added lines on red and green. On a terminal
  140 columns wide or more, the old and new versions sit side by side.
- **Code** — code blocks in replies are syntax-highlighted. `Alt+Y` copies the
  last one.
- **Cost** — each reply's header shows how long the reply took, its tokens and
  what it cost.
- **Errors** — a failed turn shows a red card that names the failure (key
  refused, budget spent, network…) and the next step.
- **Notification** — when a reply takes over 10 seconds and the terminal is in
  the background, ouija asks for a desktop notification (OSC 9 / OSC 777:
  iTerm2, kitty, WezTerm, foot, GNOME terminals). The tab title shows what ouija
  is doing.

A few commands work on the conversation as a whole:

- `Ctrl+F` searches the conversation and highlights the matches.
- `/retry` sends the last message again.
- `/changes` lists the files changed this session.
- `/undo` reverts ouija's latest file change; repeat it to go further back. It
  does not undo `bash` commands.
- `@path` in a message attaches that file. Typing `@` opens a picker of the
  project's files.
- `/learn <fact>` teaches ouija a fact about the project. ouija saves it in
  `docs/knowledge.md` and uses it in every later chat and `/task`.
- `/theme` picks the colors (ouija, catppuccin, dracula, mono). The chat
  restarts at once with the new colors and the conversation restored, except
  on Windows, where the new colors apply at the next start.

Every totem runs here as a slash command: `/task <description>`, `/doc`,
`/sticker` and any totem you write. The chat draws the totem's passes live,
then shows its result. The welcome screen names the available totems, and
`/help` says what each does.

Paths in tool lines, diffs and approvals are links (OSC 8); click one to open
the file. ouija saves each conversation, and `ouija chat -c` (or `/sessions`)
resumes one. On exit, a card sums up the session: time, messages, tokens,
cost and the files ouija changed.

If you scroll up while a reply arrives, the status bar counts the new lines
(`↓ 3 new · End`). To select text with the mouse, switch to select mode
(`Ctrl+S`), or `Shift`+drag in most terminals.

## Approving tool calls

By default, read-only tools (`read_file`, `list_dir`, `grep`, `glob`) run
without a prompt. Tools that write files or run commands (`write_file`,
`edit_file`, `delete_file`, `bash`) need your approval: their level is `ask`.

The prompt states in words what the call wants to do ("ouija wants to edit
`engine/budget.go`"). Its frame takes the color of the risk: green for reads,
amber for writes, red for shell commands and anything outside the project.
Below it you see the full command, wrapped but never cut, or the diff the call
would apply, plus the other arguments. A file tool's prompt names the file the
call really lands on, with `..` resolved and symlinks followed: through a link
it shows both names (`notes.txt → Makefile (symlink)`). If that link changes
while the prompt waits, the approved call is refused and the model must call
again. Pick one of the options with the arrow keys (**Edit** appears only
for `edit_file`, and `delete_file` offers only **Delete**, **No** and
**Cancel**):

- **Yes** (`y`) — approve this call once.
- **No** (`n`) — reject it. ouija tells the model to try a different approach.
- **Session** (`s`) — approve for the rest of the session, **scoped to this
  exact target** (the file path, or the exact bash command). For bash, ouija
  also adds one allowlist pattern per simple command and saves it to the
  project config file: the discovered `.ouija/config.json`, `.ouija.json` or
  `ouija.json`, or the `--config` path. A command with `$(…)` or a variable
  adds no pattern, and neither does an approval to run outside the sandbox.
  ouija refuses to rewrite an untrusted config that sets trust-only keys; the
  approval then lasts for this session only.
- **Edit** (`e`, `edit_file` only) — open the proposed change in `$EDITOR`
  (or `vi`, `notepad` on Windows, if unset) and adjust it before approving.
- **Cancel** (`esc`) — cancel the generation.

`delete_file` asks for `d` (**Delete**) instead of `y`, and offers no
**Session** and no **Edit**. No approval overrides the hard rules: ouija refuses a
denylisted command or a protected path (`.env`, `~/.ssh/*`, `/etc/*`) *before*
it shows the prompt, and so it does a command or a path holding a control or
invisible character the prompt could not show.
[Security](../explanation/security.md) explains why and lists the paths.

After you reject a tool during a generation, ouija rejects every later call to
that tool until the generation ends. To stop the prompts for a tool you trust,
use `/permit <tool> allow`.

## When the sandbox blocks a command

On Linux and macOS, bash runs in the OS sandbox by default: a command writes
only inside the project and reaches no network (see
[Configure → Bash sandbox](configure.md#bash-sandbox)). When the sandbox blocks
one, its result says it ran in the sandbox, so the model does not mistake the
denial for a bug. Where the machine cannot enforce the default sandbox
(Windows, an older kernel), the chat says so in a banner when it starts, and
bash runs unconfined, asking before every command; `ouija doctor` shows why.

The model may then retry the same command with `"unsandboxed": true`. You get
a prompt marked **runs OUTSIDE the bash sandbox: full access to your files and
network**, every time, whatever `/permit` or the allowlist says. **Session**
there covers only that exact command, and it adds nothing to the allowlist. If
the command needs something regular, such as a port or a path, open it under
`tools.bash.sandbox` instead.

## Trusting a command for good

When you approve a bash command with **Session**, ouija writes it to
`tools.bash.allowlist` in the project config, so the approval survives
restarts. A plain **Yes** runs the command once and saves nothing. The
allowlist also limits what unattended runs (`permission.tools.bash: "allow"`)
may execute; see
[Config schema](../reference/config-schema.md#toolsbashallowlist).

## Change permission levels

```text
/permit                 → show every tool's level
/permit bash allow      → never prompt for bash
/permit write_file deny → block writes outright
```

To set the same levels permanently, use `permission.default` and
`permission.tools` in `.ouija/config.json`; see [Configure](configure.md).

## Restart the model or the provider

`/model` and `/provider` open an interactive picker that shows each model's
context-window size. ouija fetches the model list from each provider's API and
caches it for a day in `~/.cache/ouija/models/`; offline, it falls back to a
built-in list. Typing the command followed by a space opens the inline
suggestion list instead. When the context has grown too large, `/clear` wipes
the history.

## Debug logging

`/debug` sends the engine's debug logs to `chat-debug.log` in ouija's folder
of the user cache directory (`~/.cache/ouija/` on Linux), and prints the path.
The log holds prompts and file contents, so the file is `0600` in a folder
only you can enter. Run `/debug` again to stop.

## Exiting

Press `Ctrl+C` or `Ctrl+D` at any time, or type `exit` / `quit` and press Enter.
After the TUI exits, ouija prints to stderr a summary card (when the session
had messages): time, messages, tokens and steps, the model, cost when pricing
is set, and the files changed. Then it prints the session's span tree.

---

🔮 ouija docs — [Index](../index.md) · [Run a totem](run-totems.md)
