🔮 ouija docs · [Index](../index.md)

---

# MCP integration

> Using ouija via the Model Context Protocol in editors and agentic tools.
> Audience: user

## What is MCP?

The ouija MCP server (`ouija mcp`) exposes totems as MCP **tools** and project
files as MCP **resources** over stdio using JSON-RPC 2.0.

- **Tools**: named `ouija_<totem>` — e.g. `ouija_task`, `ouija_doc`, `ouija_sticker`
- **Resources**: URI scheme `ouija://project/{path}` — sandboxed file access
  within the project root
- **Transport**: JSON-RPC 2.0 over stdio, newline-delimited. The `initialize`
  handshake advertises MCP protocol version `2025-06-18` with `tools` and
  `resources` capabilities.
- **Logging**: stdout carries protocol messages only; diagnostics go to stderr.

## Setup

The MCP server is built into the `ouija` binary — there is nothing extra to
install if you already have `ouija` on your `PATH`:

```sh
ouija mcp install   # detect OpenCode / Claude Code / Cursor and register ouija
ouija mcp status    # show where ouija is connected
```

`install` touches only harnesses it finds, writes a `.bak` before editing any
config, and never overwrites an existing entry. Restart the harness afterwards.

A standalone `ouija-mcp` binary also exists for hosts configured to launch it
directly (`go install ./cmd/ouija-mcp`). It runs the same server.

> The directory holding the binary must be on the `PATH` **of the host process**,
> because hosts spawn the server by name. If it is not there, add it in your
> shell's config file — which one depends on the shell, not on the OS (zsh:
> `~/.zshrc`, bash: `~/.bashrc`, fish: `~/.config/fish/config.fish`).

> A GUI host launched from the desktop (Claude Desktop, Cursor, …) may not inherit
> your shell's `PATH`. If the server fails to start with "command not found", put
> the **absolute path** to the binary in the host config instead of `ouija`.

## Configuration

The server command is `ouija mcp`. `ouija mcp install` writes the entries below
for you; they are shown for manual setup or for hosts ouija does not know.

### OpenCode

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

### Claude Code

```sh
claude mcp add --scope user ouija -- ouija mcp
```

### Cursor

In Cursor settings, add a new MCP server with command `ouija` and argument `mcp`.

### Any MCP host

```json
{
  "servers": {
    "ouija": {
      "command": "ouija",
      "args": ["mcp"]
    }
  }
}
```

## How it works

1. The host calls `ouija_<name>` with a task string argument.
2. The server discovers config, builds the engine, runs the totem, and returns
   the result.
3. Budget ceilings apply (the host prompts for approval before each call).
4. The host sees the result as a tool response.

---

🔮 ouija docs — [Index](../index.md) · [Run a totem](run-totems.md)
