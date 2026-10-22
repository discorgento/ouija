🔮 ouija docs · [Index](../index.md)

---

# Totems

> Why a totem is compiled Go code and not a prompt file — the design bet, what
> it buys you, and when it is the wrong tool.
> Audience: explanation

## The one-line version

A totem is an AI agent you write as ordinary Go code, with types, tests and
golden files. Underneath, every totem shares one engine that handles the
expensive, tedious parts: the cost ceiling, the filesystem sandbox, traces,
retrying a provider that fails, and exposing the totem as an MCP tool
(MCP, the Model Context Protocol, is how editors such as Claude Code or Cursor
call outside tools). The totem itself only decides the order of the work.

## Why not a markdown agent?

A harness is the program that runs a model in a loop and executes the tool
calls it asks for. Most harnesses let you define a custom agent as a markdown
file: a page of instructions, a model name, perhaps a list of allowed tools.
That approach works until you need behavior you can *verify*. We kept hitting
three failure modes:

1. **The model may skip a step.** Prose such as "Do A, then verify B, then
   reconcile" is a request, not a program. The model may skip the verification,
   especially when its context is long. In Go, "then" is the next line of code,
   so the reconcile step runs every time, whatever the model decides.
2. **You cannot test a prompt file.** No unit test can check that "the agent
   should look around the codebase before it writes". In a totem, the whole
   sequence — a scout that surveys the code, several independent passes that
   analyze it, and a reconcile step that merges their results — is a function.
   You call it in `go test` with a stubbed provider and compare the output to a
   golden file.
3. **A reviewer cannot tell what a prompt edit changed.** When someone rewords
   the instructions in a prompt file, the diff shows paragraphs that moved and
   sentences that changed, but not how the agent will now behave; the reviewer
   has to guess. When someone changes a totem, the diff shows the exact step in
   the Go control flow that changed.

In fairness, a markdown agent is faster to start, and a non-developer can edit
one. If you need an agent once and never again, a prompt file is the right
tool. Totems pay off when the agent becomes infrastructure: something you run
in CI, hand to a teammate, or depend on.

## What the engine gives every totem

Every totem inherits the harness. A totem author does *not* have to write any
of this:

| Capability | Where it lives | Why it matters |
|---|---|---|
| Cost ceiling (`maxUSD`, `maxTokens`, `maxSteps`) | `Tracker` | A runaway loop cannot drain your key |
| Filesystem sandbox (`SafeJoin`, symlink detection) | `Sandbox` | The model cannot write outside the project root |
| Permission tiers + approval | `ToolRunner` | Write and draft tools need permission; nothing is assumed |
| Bash allowlist + doom-loop detection | `ToolRunner` | Repeated identical calls get cut off |
| OS sandbox for bash (Landlock, Seatbelt) | `bash_sandbox*.go` | A command cannot reach your credentials or the network, even through a path built at run time |
| Retries + circuit breaker | `providers/` | A 429 or 5xx is retried with backoff; after 3 failures in a row the provider fails fast for 30s |
| Structured trace | `Tracer` | Every span can be exported to Perfetto for debugging |
| MCP export | `mcp/` | Any registered totem becomes a `ouija_<name>` tool |

Most agent frameworks track usage but leave enforcing a cost cap to you. Here
the engine enforces the cap on every run.

## The cost of the choice

Writing an agent in Go asks three things of you:

- **A compile step.** You iterate on a totem with `go build`, not "save and
  re-run". The scaffold (`ouija generate totem`) and a fast local build keep
  this to seconds.
- **You own the binary.** A totem in your repo is a normal Go package. It
  versions with your `go.mod`, tests with your CI, and ships however you ship
  code. You cannot yet install it from a marketplace; a registry is on the
  roadmap.
- **Go is the only language.** The engine is a Go library, so a totem in Python
  or TypeScript cannot link against it. The escape hatch is MCP: serve your own
  tools over stdio and let a harness call them.

## Where a totem lives

Both kinds implement the same interface; [Add a totem](../how-to/add-totem.md)
walks through each:

- **In the ouija repo** (`totems/<name>/`): contributed upstream, it ships in
  the `ouija` binary and appears in `ouija help` and MCP for everyone.
- **In your repo** (`github.com/you/agents`): it imports the public
  `bootstrap`, `engine` and `registry` packages and builds into your own
  binary, under your own name in MCP.

## When a totem is the wrong tool

- **One-shot automations.** A shell script with one LLM call is less code.
- **Plain questions about a codebase.** `ouija chat` already answers them; no
  totem needed.
- **Open-ended exploration**, where the model *should* choose its own path. A
  totem exists to take that choice away from the model.

---

🔮 ouija docs — [Index](../index.md) · [Add a totem](../how-to/add-totem.md)
