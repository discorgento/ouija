🔮 ouija docs · [Index](../index.md)

---

# Run a totem

> Running `task`, `review`, `doc`, `sticker` and `learn` from the terminal. Audience: user

A totem is one of ouija's built-in workflows, and each totem is a command:
`ouija <totem> "<task>"`. The [CLI reference](../reference/cli.md) lists every
flag and exit code; this page shows how to get each job done.

First [install](install.md) and [configure](configure.md) ouija: run
`ouija setup` once, then `ouija init` inside the project.

## Global flags

Every totem accepts the same flags, placed after the totem name. (`chat`
accepts `--config` and `--trace-file` of these.)

| Flag | Description |
|---|---|
| `--set key=value` | A named input the totem declares (repeatable); typed by its input schema |
| `--config <file>` | Override config auto-discovery |
| `--trace-file <path>` | Write a Perfetto/Chrome Trace JSON trace |
| `--json` | Print raw JSON result (for pipes/scripts) |

## `ouija task` — verified implementation plans

`task` is the everyday command. Run it inside a project. A
`.ouija/config.json` is optional: without one, the `ouija setup` defaults
apply.

```sh
ouija task "PROJ-1823 — block order placement when the ZIP has no shipping coverage"
ouija task "fix sorting bug" --trace-file run.json
ouija task "refactor checkout" --json
```

A scout first surveys the codebase. Several independent passes then analyze
it, each along one dimension such as correctness or security. Finally, a
reconcile step merges their findings into one plan, prints it with a
checklist, and saves it to `docs/task-plan.md`.

Run `task` from inside the module you are analyzing; it needs no extra flags,
because ouija looks for the config by walking up from the current directory.

## `ouija review` — review the branch before the PR

```sh
ouija review                          # everything the branch changed since main
ouija review "concurrency"            # with a focus
ouija review --set base=develop       # against another branch or commit
```

`review` reads the branch's diff, asks the model to find problems in it, has
a skeptic check each finding, and reports what holds. Go code reads the diff
with `git diff --merge-base <base>`, which covers commits and uncommitted
changes, so the model never has to guess it. Each finding then goes to an
independent verify pass that rereads the code. The report lists only the
confirmed findings, plus the dropped ones with the skeptic's reason. Without
`base`, `review` compares against `main`, or `master` if `main` does not exist.

`review` reports bugs, unhandled errors, races, leaks, security holes and
broken contracts. It leaves out style and naming on purpose. The repository's
own git config cannot make it run a command, because its git calls turn off
external diffs, `textconv` filters, `core.fsmonitor` and every clean/smudge
filter driver a config defines, and skip the work trees of submodules
(`--ignore-submodules=dirty`), where another config would apply.

## `ouija doc` — code-verified client documents

```sh
cd my-project && ouija doc "configuration manual"
```

`doc` writes `<slug>.html`, `<slug>.pdf` and `<slug>.md` into `docs/` under
the project root, which is the current directory by default. It produces the
PDF only when it finds Chrome, Chromium or Edge, on `PATH` or where macOS and
Windows install them; otherwise it writes the HTML and tells you how to print
it. The render runs offline: only the template's Google Fonts are fetched (see
[egress controls](../explanation/security.md#cost-and-egress-controls)).

## `ouija sticker` — project sticker

```sh
cd my-project && ouija sticker
```

`sticker` writes `<slug>.sticker.svg` into `docs/sticker/`. It takes the
project name and tech stack from whatever manifest or README the project has
(`package.json`, `composer.json`, `go.mod`, …), so any folder layout works. On
Magento projects, Go code reads the platform, PHP and theme versions and the
custom-module count straight from `composer.json` and `app/code`; the model
does not generate them. Other projects' stickers omit those fields.

## `ouija learn` — project knowledge

```sh
ouija learn "This project uses React with TypeScript"
ouija learn                     # show current knowledge
```

`learn` saves each fact to `docs/knowledge.md`, relative to the current
directory. ouija adds the saved facts to the `task` scout's prompt and to the
chat's system prompt.

## Live progress

In a terminal, ouija draws a totem's run live on stderr. Each model run (the
scout, each pass, the reconcile) gets one line with a spinner. When the run
ends, the spinner becomes `✓` or `✗` with its time and, for a failure, the
reason. A header above shows the elapsed time, tokens and cost so far, and
logs print above the board. When stderr is piped or redirected, ouija skips
the board and prints only the logs and the stats below.

```
  ouija task  ·  1m12s  ·  48.2k tok  ·  $0.21
  ✓ scout             8.1s
  ✓ p1:correctness   21.4s
  ⣾ p1:risks         18.0s
  ✗ p2:risks          3.1s  budget exceeded
```

## Per-run stats

When a totem finishes, ouija prints its stats to stderr:

```
[ouija] anthropic · claude-opus-5-5 · 184320 tokens · 14 steps · $1.05
```

The token count is a plain integer. The ` · $…` suffix appears only when the
cost is known. To keep just the stats line:

```sh
ouija task "..." 2>&1 | tail -1
```

## Next

- To work interactively, see [Chat TUI](chat.md).
- To wire ouija into an editor, see [MCP integration](integrate-mcp.md).
- For every command and flag, see the [CLI reference](../reference/cli.md).

---

🔮 ouija docs — [Index](../index.md) · [Chat TUI](chat.md)
