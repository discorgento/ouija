🔮 ouija docs · [Index](../index.md)

---

# Getting started

> From zero to your first verified plan in about 10 minutes. Audience: user

This is the guided path — do it top to bottom. Every step explains what to run
and what you should see. If you already have ouija installed, jump to
[Configure a provider](#3-configure-a-provider).

## 1. Install

```sh
git clone https://github.com/discorgento/ouija.git
cd ouija
make install        # builds ouija + ouija-mcp into $GOBIN
```

Verify:

```sh
ouija version
# ouija 1.0.0
```

Not on your `PATH` yet? [Install](../how-to/install.md) explains where the
binary landed and how to fix it for your shell.

## 2. Understand what you installed

Two binaries, one engine:

| Binary | What it is for |
|---|---|
| `ouija` | The CLI: totems (`task`, `doc`, `sticker`), `chat`, `setup`, `mcp`, … |
| `ouija-mcp` | The same MCP server, as a standalone binary for hosts that launch it by name |

You almost always use `ouija`.

## 3. Configure a provider

```sh
ouija setup
```

The wizard asks for API keys (one field per provider — leave blank to skip) and
your default provider/model. Everything is written to
`~/.config/ouija/config.json` with `0600` permissions.

No API key? Pick **ollama** — it runs locally and free, as long as an Ollama
server is reachable.

Check the result:

```sh
ouija doctor
```

`doctor` prints a checklist: config files, resolved provider/model, key
presence, registered totems, harness registration. ✓ ok, ! warning, ✗ problem.

## 4. Run your first task

Go to **your own project** — ouija analyzes the directory it runs in:

```sh
cd /path/to/your/project
ouija init                     # per-project config: provider, model, overrides
ouija task "describe this codebase and propose a refactor plan"
```

`task` runs **scout → N passes × dimensions → reconcile** and prints a plan with
a checklist. Two things to notice:

- The plan is saved to `docs/task-plan.md` in the project.
- The last stderr line is your usage: `[ouija] anthropic · claude-opus-5-5 ·
  184320 tokens · 14 steps · $1.05`.

Want the raw data instead of the pretty output? Add `--json`. Want to see where
the time and tokens went? Add `--trace-file run.json` and open it in
[ui.perfetto.dev](https://ui.perfetto.dev).

## 5. Open the chat

```sh
ouija chat
```

A full-screen TUI with the same tools and engine, but conversational. Two
behaviors worth knowing on day one:

- **Read-only tools run immediately**; write and exec tools ask for approval.
  Answer **Yes** once, **Session** to trust that exact file/command for the rest
  of the run, **Edit** to modify the proposed change first.
- **Type while the model works** — messages queue and send when it finishes.

Type `/help` inside the chat for every command and shortcut. The full workflow
is in [Use the chat TUI](../how-to/chat.md).

## 6. (Optional) Plug ouija into your editor

```sh
ouija mcp install   # detect OpenCode / Claude Code / Cursor and register ouija
ouija mcp status    # show where ouija is connected
```

Restart the harness afterwards; the totems appear as `ouija_task`,
`ouija_doc`, … Details in [MCP integration](../how-to/integrate-mcp.md).

## Where to go next

| If you want to… | Read |
|---|---|
| Know every command and flag | [CLI reference](../reference/cli.md) |
| Tune budgets, permissions, allowlists | [Configure](../how-to/configure.md) |
| Understand the pipeline you just ran | [Architecture](../explanation/architecture.md) |
| Know exactly what ouija refuses to do and why | [Security](../explanation/security.md) |
| **Write your own agent (totem)** | [Add a totem](../how-to/add-totem.md) |
| Understand why totems are code, not prompt files | [Totems](../explanation/totems.md) |

---

🔮 ouija docs — [Index](../index.md) · [Configure](../how-to/configure.md)
