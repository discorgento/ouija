🔮 ouija docs · [Index](../index.md)

---

# Totems

> Why a totem is compiled Go code and not a prompt file — the design bet, what
> it buys you, and when it is the wrong tool.
> Audience: explanation

## The one-line version

A totem is an agent you own: real Go code with types, tests and golden files.
The engine underneath is shared and does the expensive, boring parts — cost
ceiling, sandbox, traces, provider fallback, MCP export — so a totem stays
focused on *orchestration*.

## Why not a markdown agent?

Most harnesses define custom agents as markdown: a file of instructions, a
model name, maybe a tool allowlist. That is a real approach and it works until
you need behavior you can *verify*. The three failure modes we kept hitting:

1. **Sequencing is probabilistic.** "Do A, then verify B, then reconcile" written
   in prose is a request, not a control flow. The model may skip the
   verification step, especially under a long context. In Go, "then" is the
   next line — the reconcile pass runs whether the model feels like it or not.
2. **You cannot test a prompt file.** There is no unit test for "the agent
   should scout before it writes". With a totem, the scout → passes → reconcile
   pipeline is a function you can call in `go test`, with a stubbed provider
   and golden-file output.
3. **Reviews do not scale to prose.** A reviewer can reason about a diff of Go
   control flow. A diff that shuffles three paragraphs of instructions is
   guesswork about what changed behaviorally.

The flip side, stated honestly: markdown agents are faster to start with, and a
non-developer can edit one. If you need one agent, once, and never again — a
prompt file is the right tool. Totems pay off when the agent becomes
infrastructure: something you run in CI, hand to a teammate, or depend on.

## What the engine gives every totem

A totem gets the harness for free. This is the list a totem author does *not*
have to write:

| Capability | Where it lives | Why it matters |
|---|---|---|
| Cost ceiling (`maxUSD`, `maxTokens`, `maxSteps`) | `Tracker` | A runaway loop cannot drain your key |
| Filesystem sandbox (`SafeJoin`, symlink detection) | `Sandbox` | The model cannot write outside the project root |
| Permission tiers + approval | `ToolRunner` | Write/draft tools are gated, not assumed |
| Bash allowlist + doom-loop detection | `ToolRunner` | Repeated identical calls get cut off |
| Provider fallback | `providers/` | One provider down, the next is tried |
| Structured trace | `Tracer` | Every span exportable to Perfetto for debugging |
| MCP export | `mcp/` | Any registered totem becomes a `ouija_<name>` tool |

Notably, **cost caps are not a solved feature elsewhere**: most agent
frameworks track usage and leave enforcement to you. Here it is in the engine,
enforced per run.

## The cost of the choice

Being honest about what writing an agent in Go asks of you:

- **Compile step.** Iterating on a totem means `go build`, not "save and
  re-run". The scaffold (`ouija generate totem`) and the fast local build keep
  this to seconds.
- **You own the binary.** A totem in your repo is a normal Go package — it
  versions with your `go.mod`, tests with your CI, and ships however you ship
  code. It is not a plugin you install from a marketplace (a registry is on the
  roadmap).
- **Go is the only language.** The engine is a Go library. A totem in Python or
  TypeScript cannot link against it; the MCP server is the escape hatch (serve
  your own tools over stdio and let a harness call them).

## Where a totem lives

Same interface, two homes — see [Add a totem](../how-to/add-totem.md) for the
walkthrough:

- **In the ouija repo** (`totems/<name>/`) — contributed upstream, ships in the
  `ouija` binary, appears in `ouija help` and MCP for everyone.
- **In your repo** (`github.com/you/agents`) — imported via the public
  `bootstrap`, `engine`, `registry` packages, built into your own binary with
  your own name in MCP.

## When a totem is the wrong tool

- **One-shot automations.** A shell script with an LLM call is less code.
- **Pure Q&A over a codebase.** `ouija chat` already does this; no totem needed.
- **Non-deterministic exploration** where the model *should* choose its own
  path. A totem's value is that it does not let the model choose the path.

---

🔮 ouija docs — [Index](../index.md) · [Add a totem](../how-to/add-totem.md)
