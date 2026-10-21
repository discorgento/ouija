🔮 ouija docs · [Index](../index.md)

---

# MCP integration

> Using ouija via the Model Context Protocol in editors and agentic tools.
> Audience: user

## What is MCP?

The Model Context Protocol (MCP) lets an editor or agent, the *host*, call
tools that another program provides. The ouija MCP server (`ouija mcp`) offers
ouija's totems to the host as MCP **tools** and the project's files as MCP
**resources**.

- **Tools** are named `ouija_<totem>`, e.g. `ouija_task`, `ouija_review`,
  `ouija_doc`, `ouija_sticker`.
- **Resources** use the URI scheme `ouija://project/{path}` and give sandboxed
  access to files within the project root.
- **Transport** is newline-delimited JSON-RPC 2.0 over stdio. The `initialize`
  handshake advertises MCP protocol version `2025-06-18` with the `tools` and
  `resources` capabilities.
- **Logging**: stdout carries only protocol messages; diagnostics go to
  stderr.

## Setup

The MCP server is built into the `ouija` binary, so if `ouija` is already on
your `PATH`, you have nothing extra to install:

```sh
ouija mcp install   # detect OpenCode / Claude Code / Cursor and register ouija
ouija mcp status    # show where ouija is connected
```

`install` changes only the hosts (harnesses) it finds, backs up each config
to a `.bak` file before editing it, and never overwrites an existing entry.
Restart the host afterwards.

For hosts configured to launch it directly, a standalone `ouija-mcp` binary
runs the same server (`go install ./cmd/ouija-mcp`).

> The directory holding the binary must be on the `PATH` **of the host process**,
> because hosts start the server by name. If it is not there, add it in your
> shell's config file. The file depends on the shell, not on the OS:
> `~/.zshrc` for zsh, `~/.bashrc` for bash, `~/.config/fish/config.fish` for
> fish.

> A GUI host launched from the desktop (Claude Desktop, Cursor, …) may not inherit
> your shell's `PATH`. If the server fails to start with "command not found", put
> the **absolute path** to the binary in the host config instead of `ouija`.

## Configuration

The server command is `ouija mcp`. `ouija mcp install` writes the entries
below for you; use them to set a host up by hand, or for a host ouija does not
know.

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
2. The server finds the config, builds the engine, runs the totem and returns
   the result.
3. The budget ceilings apply, and the host asks for your approval before each
   call. Each call gets a fresh engine, so each has its own budget.
   A totem that only reads (such as `review`) is listed with the
   `readOnlyHint` annotation, so a host may run it without asking; the ones
   that write files carry no annotation and keep the host's confirmation.
4. The host receives the result as a tool response.

Calls run concurrently, so a long `ouija_task` does not block `ping`,
`tools/list` or another call. When the host sends `notifications/cancelled`,
the server stops that call and its spending at once and sends no response for
it. When the host closes the connection, the server cancels every call still
running.

---

🔮 ouija docs — [Index](../index.md) · [Run a totem](run-totems.md)
