🔮 ouija docs · [Index](../index.md)

---

# Security model

> What ouija refuses to do, in which order, and why. Audience: explanation

ouija defends in **several layers**: a filesystem sandbox, protected paths that
are always denied, a permission config with interactive approval, a bash
denylist and allowlist, doom-loop detection, budget ceilings, and a sanitized
environment for subprocesses.

Every layer **fails closed**: when a check cannot reach a clear answer, the
answer is no. Each layer below is a hard rule first and a preference second.

## Layer 1: Filesystem sandbox

Every file tool stays inside the project root. `SafeJoin()` checks the path
lexically and rejects `../` traversal. ouija then resolves symlinks one path
component at a time, rather than calling `EvalSymlinks` on the full path, so it
detects an escape even when the final target does not exist yet. This closes
the TOCTOU (time-of-check to time-of-use) window, and it covers a dangling
symlink too: ouija follows it to the missing target that a write would create.
The `delete_file` tool resolves symlinks before it calls `os.RemoveAll`. The
`doc` output dir and MCP resources use the same mechanism: the MCP server calls
`engine.SafeResolve()` instead of re-implementing the join, so it inherits both
the symlink and the protected-path checks.

ouija clamps an auto-discovered `.ouija.json` to its own directory, so a planted
config cannot grant reads above that directory. An explicit `--config` is
trusted, because you chose it.

### Project trust

ouija finds a project config (`.ouija/config.json`, `.ouija.json`,
`ouija.json`) by walking up from the working directory, so the file may come
from a repository someone else wrote. Until you run `ouija trust` in the
project, ouija ignores the file's security-sensitive keys and prints a warning.
These keys are `permission`, `tools` (the bash allowlist), `apiKey`, a
non-default `systemPrompt`, and a `projectRoot` other than `.` (which moves the
sandbox). Capitalization does not matter: `"Permission"` counts as
`"permission"`.

ouija records trust as the file's SHA-256 hash in the global config, itself a
protected path, so any change to the file invalidates the trust.
`ouija trust --revoke` forgets it. Files that ouija writes itself (`ouija init`,
chat saves) are trusted automatically, and ouija never rewrites an untrusted
file that sets those keys.

Without this check, cloning a repository that commits
`{"permission":{"default":"allow"}}` would give a prompt-injected model
unattended `bash`, and an `apiKey` override would send your prompts and code to
an account the repository's author controls.

## Layer 2: Protected paths (always denied)

Some paths are **always blocked**, whatever the permission level: `/etc/*`,
`/var/lib/*`, `.env*`, `.netrc`, `.gitconfig`, `.ssh/*`, `.gnupg/*`, `.aws/*`,
`.ouija/*`, `.ouija.json`, `ouija.json`, ouija's global config
(`~/.config/ouija/*`, that is `os.UserConfigDir()/ouija`, which holds every API
key and the trusted-project list), `.config/gcloud/*`, and similar sensitive
locations. Directories match exactly: `/etc` matches `/etc` but not
`/etcetera`.

ouija enforces this policy at every entry point, not only on a tool's `path`
argument:

- `resolve()` rejects a protected path inside the root. It checks both the name
  and where the path physically lands, so a symlink in the project
  (`notes.txt → .env`, `cfg → .ouija`) cannot read or write what its name hides.
- The `grep` walk skips protected files and never follows symlinks.
- A bash command that merely *names* a protected path (`cat .env`,
  `~/.ssh/id_rsa`) is refused with a message that explains why. ouija judges
  each word by its value after the shell removes quotes (`cat .e""nv` is
  `cat .env`) and by where it physically lands from the project root, so it
  also refuses a symlink (`cat notes.txt` with `notes.txt → .env`).

The bash scan accepts occasional false positives, such as a commit message that
contains the literal text `.env`: it fails closed, never open. An explicit
sandbox-escape approval still outranks the policy, because it is your consent
for one path, shown to you in full. On macOS and Windows, whose filesystems
ignore case, every spelling of a protected name (`.ENV`, `.SSH/`) is protected
too.

The bash scan reads the command's words as text, so it cannot see a path that
the command builds while it runs (`cat "$(printf ~/.ss)h/id_rsa"`). Under `ask`
you see that command in full before it runs. The boundary that holds, whatever
the command computes, is the [OS sandbox](#os-sandbox-opt-in).

## Layer 3: Permission config

A `PermissionConfig` gates every tool call with one of three levels:

| Level | Behaviour |
|---|---|
| `deny` | The tool is blocked and the model gets an error |
| `ask` | Needs your approval, in the TUI or through a confirmation prompt |
| `allow` | Runs automatically |

In the chat TUI, `NewDefaultPermissionConfig` sets the defaults: read-only
tools (`read_file`, `grep`, `glob`, `list_dir`) run without approval, and the
tools that write or execute (`write_file`, `edit_file`, `delete_file`, `bash`)
ask. Change a level at runtime with `/permit <tool> <allow|ask|deny>`, or set
it in the project config. Once you reject a tool during a generation (the
model's whole response to one message), ouija rejects every later call to that
tool automatically and tells the model to try a different approach.

## Layer 4: Bash allowlist and denylist

Two independent mechanisms check bash commands:

- **Denylist** (always on, checked before any prompt): a curated list of
  catastrophic commands, plus analysis that catches their variants: reordered
  flags (`rm -fr /`), execution wrappers (`sudo rm -fr /`, `bash -c 'rm -fr /'`,
  `eval`), raw device writes (`dd of=/dev/sda`, `fdisk /dev/sdb`), recursive
  `chmod`/`chown` on `/`, and fork bombs. It is a hard rule: even your "yes"
  cannot override it.
- **Allowlist** (opt-in): base commands you have pre-approved, such as `git` or
  `npm`. A matching command runs without a prompt. When `bash` is set to
  `allow` (unattended runs), the allowlist is the guard rail: ouija refuses any
  command outside it. An empty allowlist means "nothing pre-approved yet", not
  "everything approved": at `ask` level every command still prompts. A
  multi-word pattern names an exact action, which only bare switches may
  extend: `npm run build` also allows `npm run build --prod -v`, but not
  `npm run build ./dist`, `--out=x`, or anything after `--`. So a pattern
  learned from one harmless `find . -name '*.go'` never pre-approves
  `find . -name '*.go' -exec …`.

  ouija parses the command as shell, and **every** simple command in it must
  match on its own. With `git` allowed, `git log | head` also needs `head`, and
  `git status; curl … | sh` is not pre-approved. ouija never pre-approves a
  command whose effect the text alone does not determine: `$(…)` or backticks,
  `<(…)`, `$VAR`, environment prefixes (`GIT_SSH_COMMAND=… git fetch`),
  `export`, functions, or output redirection to anything but `/dev/null`. Such
  a command prompts under `ask` and is refused under `allow`. A single-word
  pattern trusts everything that program can do (`git -c core.pager=…`,
  `find -exec`), so prefer multi-word patterns for unattended runs.

You set the allowlist statically in `.ouija/config.json` (applied only when the
project is trusted; see [Project trust](#project-trust)), or ouija learns it as
you approve commands. Approving a bash command with **Session** adds one
pattern per simple command and saves them to disk; a command that is not fully
static (`echo $(whoami)`) adds nothing. A plain **Yes** runs the command once
and saves nothing. A `sync.RWMutex` makes the allowlist safe for concurrent
use.

### OS sandbox (opt-in)

The checks above read the command's *text*, and no reading of text can see a
path built at run time (`cat $(printf '.%s' env)`). With
`tools.bash.sandbox.enabled`, the kernel enforces the boundary instead. Each
command runs in a re-executed copy of the ouija binary, which confines itself
with Linux Landlock and then execs the shell. The confined process can write
only inside the project and the temp dir, and read only system directories,
the project and the paths the config lists. Every process it starts inherits
the confinement, and none can lift it.

The read grants leave out `/proc` (other processes' environments) and `/run`
(ssh-agent and docker sockets) on purpose. The sandbox also confines
connections to Unix sockets outside the granted paths (an ssh-agent or tmux
socket under `/tmp`), connections to abstract Unix sockets (an X11 display),
and signals to processes outside the sandbox. Those confinements need Landlock
ABI v9; on an older kernel the sockets would stay reachable. So if the sandbox
is enabled but cannot be enforced (not Linux, or Landlock older than ABI v9),
bash refuses to run. ouija also refuses a grant that would cover a credential
directory: the home directory, `~/.config`, `/`, or a project root at one of
those locations. Network access is not restricted. See
[`tools.bash.sandbox`](../reference/config-schema.md#toolsbashsandbox).

A boundary scoped to the project does **not** cover the following, by design:

- **The project itself is readable and writable**, its `.env` files included.
  The protected-path checks above still refuse commands that name them, but
  code that builds the path at run time can read them. Keep real secrets out of
  the repository, or out of the sandbox's reach.
- **`.git` is writable**, so a command can plant a hook or a `core.fsmonitor`
  setting that runs, unconfined, the next time *you* run git there. (Landlock
  cannot carve a read-only subtree out of a writable one.) ouija fingerprints
  `.git/hooks` and `.git/config` around every bash command, sandboxed or not.
  When one changes, the tool result says so and the chat shows a **git guard**
  banner: review the change before you run git in that project.

## Layer 5: Doom-loop detection

A doom loop is a model stuck repeating one call. `DoomDetector` blocks the
model when it calls the same tool with identical input **3+ times in a row**. A
fresh agent run or a user approval resets the counter.

## The order of checks

One function, `ToolRunner.Run`, decides every tool call. It runs the static
bash gates *before* any prompt, then the permission level, then the allowlist,
then execution.
[Architecture → Tool gating](architecture.md#tool-gating-permissions) lists the
full order, with the code path for each step.

## Approval workflow (chat TUI)

When a tool requires approval (`ask` level), the TUI shows:

- the tool's icon, name and call number (e.g. `[#2]`)
- all input arguments
- five options, chosen with the arrow keys: **Yes** (`y`), **No** (`n`),
  **Session** (`s`, approves for the rest of the session), **Edit** (`e`, opens
  the proposed content in `$EDITOR`), **Cancel** (`esc`). For `delete_file` the
  destructive button reads **Delete** (`d`) instead of **Yes**, so a stray `y`
  cannot delete a file.

Approvals apply to the tool's **target**, not only to its name: choosing
**Session** on `write_file` for `src/a.php` does not pre-approve `src/b.php`,
and **Session** on a bash command does not pre-approve a different command. For
bash, **Session** also adds one allowlist pattern per simple command (none for
a command with substitutions or variables) and saves them to the config file;
**Yes** never saves. Rejecting a tool blocks it for the rest of the generation.

## Cost and egress controls

A token, step and USD budget bounds cost, and its sanity checks reject negative
token counts. Secrets come from the config file (`0600`), and ouija never logs
or traces them. The bash tool sanitizes the environment: `sanitizeEnv()` strips
credential-like variables (`AWS_*`, `*API_KEY*`, `*SECRET*`, `*TOKEN*`, etc.)
before it passes the environment to subprocesses. Subprocesses get
`WaitDelay = 3s` as a safety net against runaway children.

Egress controls stop data from leaving the machine through ouija's outputs. PDF
rendering runs headless Chrome with argv only, controlled paths, and a
throwaway `--user-data-dir`. The model-written HTML gets a
Content-Security-Policy as the first element of its `<head>`, which allows
inline styles, Google Fonts and `data:` images only: no scripts, frames, remote
images or requests. Content injected through the repository therefore cannot
use the render, or a later look at the HTML, to send data out. The totems
(ouija's agent workflows) write to fixed outputs (`docs/task-plan.md`,
`docs/<slug>.*`, `docs/sticker/`). Those outputs and `docs/knowledge.md` go
through `engine.SafeResolve()`, so a symlink planted in the repository cannot redirect
them outside the project or onto a protected file.

## Tool categories

| Risk | Tools |
|---|---|
| Read-only | `read_file`, `grep`, `glob`, `list_dir`, `submit_result` |
| Write | `write_file`, `edit_file`, `delete_file` |
| Exec | `bash` |

The permission system applies to all three categories.

## Accepted risks

Prompt injection, meaning text in the repository that tries to steer the model,
can still skew *analysis content*: malicious code comments might lead the model
to write a misleading report. The layered defenses limit the damage to that: no
exfiltration, and no modification without approval. You own three trust
boundaries: an explicit `--config`, a project you ran `ouija trust` on, and the
MCP host.

The bash checks read the command's text, so a path built at run time
(`cat $(printf '.%s' env)`, or a variable set earlier in the same command)
escapes them. That is why the allowlist never pre-approves such commands. Under
`allow` with an empty allowlist, only the static gates stand in the way, so
reserve that setting for trusted projects.

## Reporting a vulnerability

See [SECURITY.md](../../SECURITY.md) to report privately through GitHub
Security Advisories. Changes to security-sensitive code follow extra rules in
[Contribute](../how-to/contribute.md#security-sensitive-code).

---

🔮 ouija docs — [Index](../index.md) · [Architecture](architecture.md)
