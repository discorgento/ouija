🔮 ouija docs

---

# ouija docs

Documentation for [ouija](https://github.com/discorgento/ouija) — a multi-totem
AI harness that is generic across projects and across LLMs.

New to ouija? Start with the [README](../README.md) for the pitch, or go
straight to [Getting started](tutorial/getting-started.md) and run your first
task in 10 minutes.

Docs are organized by **what the reader is trying to do** (Diátaxis): learn,
accomplish, look up, understand. Every page follows
[`_style-guide.md`](_style-guide.md).

## Tutorial — "teach me"

| Doc | What it covers |
|---|---|
| [Getting started](tutorial/getting-started.md) | Install → configure → first task → chat, step by step |

## How-to — "how do I do X"

| Doc | What it covers |
|---|---|
| [Install](how-to/install.md) | Installing ouija from source, requirements, `PATH` |
| [Configure](how-to/configure.md) | `ouija setup`, `ouija init`, config layers, API keys |
| [Run a totem](how-to/run-totems.md) | `task`, `doc`, `sticker`, `learn`, per-run stats |
| [Use the chat TUI](how-to/chat.md) | Approvals, permissions, queueing, sessions |
| [MCP integration](how-to/integrate-mcp.md) | Using ouija from OpenCode, Claude Code, Cursor |
| [Add a totem](how-to/add-totem.md) | Create a new totem — in the ouija repo or your own |
| [Add a provider](how-to/add-provider.md) | Implementing a new LLM backend provider |
| [Contribute](how-to/contribute.md) | Workflow, code conventions, commits, tests |

## Reference — "what is this"

| Doc | What it covers |
|---|---|
| [CLI](reference/cli.md) | All commands, flags, slash commands, shortcuts, exit codes |
| [Config schema](reference/config-schema.md) | JSON structure for global and project config |
| [Environment variables](reference/environment.md) | Every variable ouija reads |
| [Trace schema](reference/trace-schema.md) | Perfetto JSON trace format for `--trace-file` |

## Explanation — "why / how does it work"

| Doc | What it covers |
|---|---|
| [Architecture](explanation/architecture.md) | Layers, data flow, engine pipeline, gating |
| [Engine](explanation/engine.md) | ReAct loop, streaming, budget, providers, context limits |
| [Security](explanation/security.md) | The five layers, denylist, sandbox, accepted risks |

---

**Also at the repo root:** [README](../README.md) ·
[CONTRIBUTING](../CONTRIBUTING.md) · [SECURITY](../SECURITY.md) ·
[CHANGELOG](../CHANGELOG.md)

🔮 ouija docs
