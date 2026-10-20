🔮 ouija docs · [Index](../index.md)

---

# Getting started

> From zero to your first verified plan in about 10 minutes. Audience: user

Follow this guide from top to bottom. Each step tells you what to run and what
you should see. If you already have ouija installed, jump to
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
# ouija 1.2.3   # 0.0.0 for a build from source before the first release
```

Not on your `PATH` yet? [Install](../how-to/install.md) explains where the
binary landed and how to fix it for your shell.

## 2. Understand what you installed

Two binaries, one engine:

| Binary | What it is for |
|---|---|
| `ouija` | The CLI: totems (`task`, `review`, `doc`, `sticker`), `chat`, `setup`, `mcp`, … |
| `ouija-mcp` | The same MCP server, as a standalone binary for hosts that launch it by name |

You almost always use `ouija`.

## 3. Configure a provider

```sh
ouija setup
```

The wizard asks for API keys, one field per provider (leave a field blank to
skip that provider), and for your default provider and model. It writes
everything to the global config with `0600` permissions:
`~/.config/ouija/config.json` on Linux, `~/Library/Application Support/ouija/`
on macOS, and `%AppData%\ouija\` on Windows.

No API key? Pick **ollama**. It runs locally and for free, as long as ouija can
reach an Ollama server.

Check the result:

```sh
ouija doctor
```

`doctor` prints a checklist of the config files, the provider and model it
will use, whether the key is present, the registered totems (the agents ouija
ships), and whether your editor's harness knows about ouija. Each line is
marked ✓ for ok, ! for a warning, or ✗ for a problem.

## 4. Run your first task

Go to **your own project**, because ouija analyzes the directory it runs in:

```sh
cd /path/to/your/project
ouija init                     # per-project config: provider, model, overrides
ouija task "describe this codebase and propose a refactor plan"
```

`task` runs **scout → N passes × dimensions → reconcile**. A scout first
surveys the codebase. Several independent passes then analyze it, each along
one dimension such as correctness or security. Finally, a reconcile step merges
their findings into one plan, which `task` prints with a checklist. Notice two
things:

- The plan is saved to `docs/task-plan.md` in the project.
- The last stderr line is your usage: `[ouija] anthropic · claude-opus-5-5 ·
  184320 tokens · 14 steps · $1.05`.

For raw data instead of formatted output, add `--json`. To see where the time
and tokens went, add `--trace-file run.json` and open the file in
[ui.perfetto.dev](https://ui.perfetto.dev).

## 5. Open the chat

```sh
ouija chat
```

The chat is a full-screen terminal UI with the same tools and engine, but you
talk to it. Two behaviors are worth knowing on day one:

- **Read-only tools run immediately**; tools that write files or run commands
  ask for approval. Answer **Yes** to allow it once, **Session** to trust that
  exact file or command for the rest of the run, or **Edit** to change the
  proposed edit first.
- **You can type while the model works.** Your messages wait in a queue and go
  out when it finishes.

Type `/help` inside the chat for every command and shortcut. The full workflow
is in [Use the chat TUI](../how-to/chat.md).

## 6. (Optional) Plug ouija into your editor

```sh
ouija mcp install   # detect OpenCode / Claude Code / Cursor and register ouija
ouija mcp status    # show where ouija is connected
```

MCP, the Model Context Protocol, lets these editors call outside tools.
Restart your editor afterwards, and the totems appear as tools named
`ouija_task`, `ouija_doc`, … Details in
[MCP integration](../how-to/integrate-mcp.md).

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
