🔮 ouija docs · [Index](../index.md)

---

# Security model

> What ouija refuses to do, in which order, and why. Audience: explanation

ouija has **multiple layers** of defense — filesystem sandbox, permission
config, interactive approval, bash denylist and allowlist, budget ceilings,
environment sanitization, doom-loop detection, and protected paths that are
always denied.

The organizing principle is **fail closed**: when a check is inconclusive, the
answer is no. Every layer below is a hard rule *before* it is a preference.

## Layer 1: Filesystem sandbox

Every file tool is confined to the project root via `SafeJoin()` — a lexical
check that rejects `../` traversal. Symlinks are resolved component by component
(not via `EvalSymlinks` on the full path) to detect escapes even when the final
target doesn't exist yet (closing the TOCTOU window). The `delete_file` tool
resolves symlinks before `os.RemoveAll`. The `doc` output dir and MCP resources
use the same mechanism — the MCP server calls `engine.SafeResolve()` rather than
re-implementing the join, so it inherits both the symlink and protected-path
checks.

An auto-discovered `.ouija.json` is clamped to its own directory, so a planted
config can't grant reads above it. An explicit `--config` is trusted (your
choice).

## Layer 2: Protected paths (always denied)

Certain paths are **always blocked** regardless of the permission level:
`/etc/*`, `/var/lib/*`, `.env*`, `.netrc`, `.gitconfig`, `.ssh/*`, `.gnupg/*`,
`.aws/*`, `.ouija/*`, `.config/gcloud/*`, and similar sensitive locations.
(Path matching uses exact directory match — `/etc` matches `/etc` but not
`/etcetera`.)

The policy is enforced at every entry point, not only on the `path` argument:
`resolve()` rejects a protected path inside the root — checked both on the name
and on where the path physically lands, so a symlink in the project
(`notes.txt → .env`, `cfg → .ouija`) cannot read or write what the name hides —
the `grep` walk skips protected files and never follows symlinks, and a bash
command that merely *names* a protected path
(`cat .env`, `~/.ssh/id_rsa`) is refused with an explanatory message. The bash
scan accepts occasional false positives (e.g. a commit message that literally
contains `.env`) — it fails closed, never open. An explicit sandbox-escape
approval still outranks the policy: that is a per-path user consent shown in
full.

## Layer 3: Permission config

Every tool call is gated by a `PermissionConfig` with three levels:

| Level | Behaviour |
|---|---|
| `deny` | Tool is blocked — model gets an error |
| `ask` | Requires interactive approval (TUI) or prompts for confirmation |
| `allow` | Runs automatically |

In the chat TUI the default comes from `NewDefaultPermissionConfig`: read-only
tools (`read_file`, `grep`, `glob`, `list_dir`) run without approval; write/exec
tools (`write_file`, `edit_file`, `delete_file`, `bash`) ask. Editable at runtime
via `/permit <tool> <allow|ask|deny>` or set statically in the project config.
Once a tool is rejected during a generation, all subsequent calls to that same
tool are auto-rejected — the model is told to try a different approach.

## Layer 4: Bash allowlist and denylist

Two independent mechanisms:

- **Denylist** (always on, before any prompt): a curated list of catastrophic
  commands plus semantic analysis — flag-order variants (`rm -fr /`), execution
  wrappers (`sudo rm -fr /`, `bash -c 'rm -fr /'`, `eval`), raw device writes
  (`dd of=/dev/sda`, `fdisk /dev/sdb`), recursive `chmod`/`chown` on `/`, and
  fork bombs. It is a hard rule: a user "yes" cannot override it.
- **Allowlist** (opt-in): base commands the user has pre-approved, e.g. `git`,
  `npm`. A matching command runs with no prompt. With `bash` set to `allow`
  (unattended runs) the allowlist is the guard rail — a command outside it is
  refused. An empty allowlist means "nothing pre-approved yet", not "everything
  approved": at `ask` level every command still prompts. A multi-word pattern
  matching is an exact action: `npm run build` also allows `npm run build --prod`
  but not `npm run build ./dist`.

Configured statically in `.ouija/config.json`, or learned interactively: a bash
command approved with **Session** is added to the allowlist and persisted to
disk. A plain **Yes** runs the command once and is *not* persisted. Thread-safe
via `sync.RWMutex`.

## Layer 5: Doom-loop detection

Encapsulated in `DoomDetector`. If the model calls the same tool with the
identical input **3+ times in a row**, it's blocked. Fresh agent runs and user
approvals reset the counter.

## The order of checks

One path decides everything — `ToolRunner.Run`. Static bash gates run *before*
any prompt, then permission level, then allowlist, then execution. The full
order, with the code path for each step, is in
[Architecture → Tool gating](architecture.md#tool-gating-permissions).

## Approval workflow (chat TUI)

When a tool requires approval (`ask` level), the TUI shows:

- Tool icon + name + call number (e.g. `[#2]`)
- All input arguments
- Five options navigable with arrow keys: **Yes** (`y`), **No** (`n`),
  **Session** (`s`, approves for the rest of the session), **Edit** (`e`, opens
  the proposed content in `$EDITOR`), **Cancel** (`esc`). For `delete_file` the
  destructive button is **Delete** (`d`) instead of Yes, so a stray `y` can't
  delete a file.

Approvals are scoped to the tool's **target**, not just the tool name: choosing
**Session** on `write_file` for `src/a.php` does not pre-approve `src/b.php`,
and **Session** on a bash command does not pre-approve a different command. For
bash, **Session** also adds the command segments to the allowlist and persists
them to the config file; **Yes** never persists. Rejecting a tool blocks that
tool for the rest of the generation.

## Cost and egress controls

Cost is bounded by the token/step/USD budget, with sanity checks (rejects
negative token counts). Secrets come from config file (`0600`) and are never
logged or traced. The bash tool sanitizes environment: `sanitizeEnv()` strips
credential-like env vars (`AWS_*`, `*API_KEY*`, `*SECRET*`, `*TOKEN*`, etc.)
before passing to subprocesses. Subprocesses have `WaitDelay = 3s` as a safety
net against runaway children. PDF rendering runs headless Chrome with argv only,
controlled paths, and a throwaway `--user-data-dir`.

## Tool categories

| Risk | Tools |
|---|---|
| Read-only | `read_file`, `grep`, `glob`, `list_dir`, `submit_result` |
| Write | `write_file`, `edit_file`, `delete_file` |
| Exec | `bash` |

The permission system applies across all categories.

## Accepted risks

Prompt injection can still skew *analysis content* — the model might produce a
misleading report based on malicious code comments. The layered defenses contain
the blast radius (no exfiltration, no modification without approval). An explicit
`--config` and the MCP host are trust boundaries you own.

## Reporting a vulnerability

See [SECURITY.md](../../SECURITY.md) — private reporting via GitHub Security
Advisories. Security-sensitive code changes have extra rules in
[Contribute](../how-to/contribute.md#security-sensitive-code).

---

🔮 ouija docs — [Index](../index.md) · [Architecture](architecture.md)
