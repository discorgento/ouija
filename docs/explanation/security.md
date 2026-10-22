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
detects an escape even when the final target does not exist yet. It covers a
dangling symlink too: ouija follows it to the missing target that a write would
create.

A check by path still leaves a TOCTOU (time-of-check to time-of-use) window: a
symlink swapped in after the check would be followed by the open. The file
tools (`read_file`, `grep`, `list_dir`, `write_file`, `edit_file`,
`delete_file`, `@` mentions), the knowledge file, the totems' outputs, MCP
resources and the chat's **Edit** in `$EDITOR` close it. They open,
create and remove through an `os.Root` on the project, which refuses any link
that leads out of it. Once the file is open, ouija checks that it is still the
file its path names and that the file is not protected, so a link swapped to
`.env` inside the project fails too. A write truncates only after that check.
Only regular files and directories are opened, and the open never waits, so a
FIFO made in the project (`mkfifo`) cannot hang a tool.
A path with a control character (newline, tab, escape) or an invisible or
bidi one is refused before any prompt: no file needs one, and a newline once
pushed the real target out of the prompt's view. `read_file` and `grep` read a
line in bounded memory: `read_file` keeps 20 KB of a line and marks the cut,
`grep` searches a line's first MB, so a single-line file of gigabytes cannot
exhaust memory. `edit_file` refuses a file over 16 MB (edit it with bash). A
`glob` pattern holds at most 8 `**`, and matching it takes polynomial time.
Code outside the engine uses the same functions (`engine.SafeOpenFile()`,
`engine.SafeWriteFile()`) instead of re-implementing them. The one write that
still goes by path is the `doc` PDF, which Chrome writes itself to the path
`engine.SafeResolve()` approved.

ouija clamps an auto-discovered `.ouija.json` to its own directory, so a planted
config cannot grant reads above that directory. An explicit `--config` is
trusted, because you chose it.

### Project trust

ouija finds a project config (`.ouija/config.json`, `.ouija.json`,
`ouija.json`) by walking up from the working directory, so the file may come
from a repository someone else wrote. Until you run `ouija trust` in the
project, ouija ignores the file's security-sensitive keys and prints a warning.
These keys are `permission`, `tools` (the bash allowlist and sandbox),
`apiKey`, a non-default `systemPrompt`, non-default `rules` (they go into the
system message too), `modelSuggestions` (the `/model` picker would list the
file's models first), and a `projectRoot` other than `.` (which moves the
sandbox). A `provider` or `model` other than the ones you chose in
`ouija setup` counts too: it decides which company gets your code and your
key, and what each token costs, so an untrusted file keeps yours. So does a
`budget` above the defaults: an untrusted file may lower the token and step
budget but never raise it. The same holds for what only costs money or time:
a totem's `passes`, `streamTimeout` and `contextWindow` may only go down, and
the token price may not go below zero (a negative price kept the spend at
zero, so `budget.maxUSD` never tripped). A totem's `dimensions` go into the
analysis prompts like `rules`, so an untrusted file's are dropped.
[Trust-only keys](../reference/config-schema.md#project-config-ouijaconfigjson)
lists the limits. Capitalization does not matter: `"Permission"`
counts as `"permission"`.

`ouija trust` prints each key's effect in full, including the paths and
network a sandbox section opens and the whole `systemPrompt`. Terminal
escape sequences and invisible Unicode (bidi controls, zero-width characters,
the tag characters U+E0000–E007F, which spell ASCII a model reads but a
terminal does not show) appear as visible `\u{XXXX}` escapes, with a warning,
so the file cannot hide part of what it grants. It then asks before it
records the trust, and it records the contents it showed: a file changed while
the question was open stays untrusted. Without a terminal it needs `--yes`.

Discovery stops at your home directory when the working directory is inside
it, and on Linux and macOS it skips a file another user owns: a `.ouija.json`
planted in `/tmp` or `/home` would otherwise apply to every project below it.

ouija refuses a discovered config whose real file lies outside the directory
it was found in: a repository could otherwise ship `.ouija.json` as a symlink
to a project you trusted (borrowing its trust) or to the global config (which a
chat save would then overwrite). ouija never rewrites the global config as a
project config.

ouija records trust as the file's SHA-256 hash in the global config, itself a
protected path, so any change to the file invalidates the trust.
`ouija trust --revoke` forgets it. Files that ouija writes itself (`ouija init`,
chat saves) are trusted automatically, and ouija never rewrites an untrusted
file that sets those keys.

The bash sandbox is the exception that goes the other way. It is on by
default where the OS supports it, and enabled in the global config
(`"sandbox": {"enabled": true}`) it applies to every project, trusted or not,
and no project config can turn it off. A trusted project can only add the
paths and ports it needs. `ouija doctor` shows whether it is on.

Without this check, cloning a repository that commits
`{"permission":{"default":"allow"}}` would give a prompt-injected model
unattended `bash`, and an `apiKey` override would send your prompts and code to
an account the repository's author controls.

## Layer 2: Protected paths (always denied)

Some paths are **always blocked**, whatever the permission level: `/etc/*`,
`/var/lib/*`, `/proc/*`, `/sys/*`, `.env*`, `.netrc`, `.gitconfig`,
`.git-credentials`, `.pgpass`, `.pypirc`, `.my.cnf`, SSH keys by name
(`id_rsa`, `id_ed25519`, …) anywhere, `.npmrc`, `.vault-token`, `*.tfstate`,
secrets a project keeps in its own tree (`app/etc/env.php`, `wp-config.php`,
`config/database.yml`, `auth.json` at any level, `credentials.json`,
`*-sa.json`, `service-account.json`, `secrets.yml`, `credentials.yml.enc`,
`terraform.tfvars`, `*.auto.tfvars`, `.htpasswd`, `.yarnrc`, `.bundle/config`,
`.dev.vars`, `local.settings.json`, `kubeconfig`), keys and keystores by
extension (`*.pem`, `*.key`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore`), every
git config and hook directory (`.git/config`, `.git/config.worktree`,
`.git/hooks/*`, and the same under `.git/modules/*` and `.git/worktrees/*`),
`.ssh/*`, `.gnupg/*`, `.aws/*`, `.kube/*`, `.docker/*`,
`.password-store/*`, `.config/gh/*`, `.ouija/*`, `.ouija.json`, `ouija.json`,
token files inside tool dirs (`.cargo/credentials.toml`, `.m2/settings.xml`,
`~/.config/composer/auth.json`, `~/.config/pip/pip.conf`, …), browser
profiles, ouija's global config
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
for one path, shown to you in full. Every spelling of a protected name
(`.ENV`, `.SSH/`) is protected too, on every OS: macOS and Windows ignore case
by default, and Linux does in a casefold ext4 directory or on vfat and exFAT.
On a case-sensitive filesystem this refuses a file that only shares the name,
by design. The list also refuses some files that hold no secret, such as a
public certificate in a `.pem`.

A hard link is the same file under another name. ouija compares a file with
more than one link against the credential files in your home directory, so
`ln ~/.ssh/id_rsa notes.txt` does not hand out the key. Inside the project
it cannot find a file's other names, so it judges a linked file by where it
is. The file tools never write one: a write would change every name, and
`.git/config` linked as `notes.txt` would take a git config. They read one
under `.git/objects` (`git clone --local` links objects), and one under
`node_modules`, where pnpm links every file from its store, unless it is one
of the project's protected files: ouija walks the project for them once per
call, only when it meets such a file. They refuse to read one anywhere else
(`ln .env notes.txt`). A checkout never makes hard links, so this costs
little.

The file tools also never write or delete git metadata: any path with a
component named `.git`, the directory or a worktree's `.git` file. Besides the
config and hooks, `.git/info/attributes` runs filters,
`.git/objects/info/alternates` pulls objects from elsewhere, and a planted
`.git` file points git at another repository. Reading stays allowed
(`.git/HEAD`).

Protected paths guard secrets, not every file that runs code later. A
`Makefile`, the `scripts` of a `package.json`, `.husky/*`,
`.pre-commit-config.yaml` or `.vscode/tasks.json` runs whatever it holds the
next time you run that tool, outside any ouija sandbox. Treat approving a write
to one of them as seriously as approving the bash command it contains.

The bash scan reads the command's words as text, so it cannot see a path that
the command builds while it runs (`cat "$(printf ~/.ss)h/id_rsa"`). Under `ask`
you see that command in full before it runs. The boundary that holds, whatever
the command computes, is the [OS sandbox](#os-sandbox).

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

### OS sandbox

The checks above read the command's *text*, and no reading of text can see a
path built at run time (`cat $(printf '.%s' env)`). The OS sandbox, on by
default where the OS supports it, has the kernel enforce the boundary instead.
On Linux each command runs in a re-executed copy of the ouija binary, which
confines itself with Landlock and then execs the shell; on macOS it runs under
a Seatbelt profile (`sandbox-exec`) whose paths are passed as parameters, never
spliced into the profile's text. The confined process can write only inside
the project, a private temp dir and a private build cache, and read only
system directories, the project, the parts of the home toolchains builds read
(`~/go/pkg/mod`, `~/.cargo/registry`, `~/.nvm/versions`, …, never the
credential files next to them) and the paths the config lists. Every process
it starts inherits the confinement, and none can lift it. On Windows, and on
any OS without Landlock ABI v9 or `sandbox-exec`, ouija skips the default
sandbox: bash runs unconfined and, at the default `ask` level, asks before
every command.

The design follows what the other coding agents do (OpenAI Codex, Claude
Code's sandbox-runtime, Cursor, Gemini CLI), and is stricter on reads: they
read the whole disk and deny a list of secrets, ouija reads only what it
lists. As in Cursor, build caches are private: the user's real caches are
read-only, because tools run later outside the sandbox trust them (go build
reuses a cached object, npx and pre-commit run cached code), and a writable
one would let a confined command plant code that escapes it. On macOS the
Seatbelt profile grants only the system services and sysctls Codex grants,
denies LaunchServices and Apple events (through them `open` and `osascript`
start programs outside the sandbox), the Docker socket, other terminals' ttys
(their keystrokes), the fcntls that write through a read-only descriptor, and
writes to what git runs on its own in the project: `.git/hooks`,
`.git/config`, `commondir`, `config.worktree`, `modules/` and `worktrees/`.
`.git` itself cannot be moved or created there (`mv .git g` would carry the
hooks past those rules), so on macOS `git init` of the project itself, and
commands that write its git config (`git remote add`, `git config`), need an
approved unsandboxed run. `open`, `osascript` and `osacompile` cannot run at
all.

When the sandbox blocks a command the user needs (a download, a file outside
the project), the model retries it with `"unsandboxed": true`, and the user
approves that exact command in a prompt that says it runs outside the
sandbox. The permission level and the allowlist never skip that prompt, and
without someone to answer it (MCP, CI) the call is refused.
`"allowUnsandboxed": false` removes the escape hatch.

The read grants leave out `/proc` (other processes' environments) and `/run`
(ssh-agent and docker sockets) on purpose. The sandbox also confines
connections to Unix sockets outside the granted paths (an ssh-agent or tmux
socket under `/tmp`), connections to abstract Unix sockets (an X11 display),
and signals to processes outside the sandbox. Those confinements need Landlock
ABI v9; on an older kernel the sockets would stay reachable. So if a config
enables the sandbox and it cannot be enforced (Windows, or Landlock older than
ABI v9), bash refuses to run; the default sandbox, which no config asked for,
falls back to running bash unconfined there. ouija says so: the chat shows a
banner when it starts ("bash sandbox unavailable (…): commands run
unconfined"), a one-shot run or the MCP server prints the same line on stderr
before the first unconfined command, and `ouija doctor` warns. ouija also
refuses a grant that would cover a credential directory: the home directory,
`~/.config`, `/`, or a project root at one of those locations.

TCP is closed by default too: a confined command can neither connect (not even
to `localhost`) nor listen, so it cannot send the project's code anywhere
over TCP. A project opens the ports it needs (`network.connect`, e.g. `443`
for downloads, or `network.bind` for a dev server). The filter is by port, not
by host, so an open port is open to every server. Since Landlock ABI v10 UDP
is filtered as well: a closed sandbox sends no DNS query either (no data out
through DNS), and opening any TCP port opens DNS (UDP 53) with it, no other
UDP port. On a kernel with ABI v9, UDP is not filtered, so a DNS query can
still carry data out.

Landlock filters TCP and UDP and no other protocol. MPTCP, which the kernel
enables by default, reaches any server past Landlock's TCP rules, and so
would SCTP, ICMP (many distributions let every user open ping sockets,
`net.ipv4.ping_group_range`) and VSOCK. So on Linux the sandbox helper also
installs a seccomp filter before it execs the shell, and `socket()` makes only
TCP and UDP over IPv4 and IPv6, Unix sockets and the local netlink and
`AF_ALG` sockets:

- MPTCP, SCTP and other protocols fail with `EPROTONOSUPPORT`, so a program
  that tries MPTCP first (Go, curl) falls back to plain TCP, which Landlock
  filters;
- other address families fail with `EAFNOSUPPORT`;
- ICMP, raw and packet sockets fail with `EPERM`;
- `io_uring_setup` fails with `ENOSYS`, since io_uring makes sockets without
  calling `socket()`;
- with no port open, UDP sockets fail with `EPERM` too, so a kernel with
  Landlock ABI v9, which does not filter UDP, sends no DNS query either;
- netlink's `sock_diag`, which lists every socket on the machine (`ss`),
  fails with `EPERM`;
- `ptrace`, `process_vm_readv`, `process_vm_writev` and `pidfd_getfd` fail
  with `EPERM`: where no PID namespace separates them, a confined command
  could otherwise read ouija's memory, API keys included (debuggers such as
  `gdb` and `strace` do not work in the sandbox).

The filter knows x86-64 and arm64. There 32-bit programs cannot call
`socketcall` at all (its arguments are out of a filter's reach). On any other
architecture the sandbox counts as unavailable, as on a kernel without
Landlock. `network.all` lifts the socket rules with the rest; the rules
against reading other processes stay.
See [`tools.bash.sandbox`](../reference/config-schema.md#toolsbashsandbox).

Every command, confined or not, runs in a process group of its own (Unix).
When the shell exits, is cancelled or times out, ouija kills the whole group
before it puts back the git state below, so a background job
(`(sleep 5; …) &`) cannot act after that check. A job that holds the output
open ends the command 3 seconds after the shell exits. A process that leaves
the group (`setsid`, a daemon that double-forks) escapes that kill, so on
Linux a confined command also runs in user and PID namespaces of its own: the
shell is PID 1 there, and when it exits the kernel kills every process left
in the namespace before ouija checks the git state. The user namespace maps
your user to itself only, so files keep their owner and the shell holds no
capabilities. Where unprivileged user namespaces are forbidden (Ubuntu's
AppArmor policy, many containers, `kernel.unprivileged_userns_clone=0`), the
process group is the only bound, and a process that calls `setsid` can still
write in the project, confined by Landlock, after the command returned. On
Windows background jobs are not killed.

A boundary scoped to the project does **not** cover the following, by design:

- **Network filtering is by TCP port, not by domain.** The other agents send
  traffic through a proxy with a domain allowlist; ouija does not have one yet,
  so `network.connect: [443]` opens HTTPS to every server.
- **The project itself is readable and writable**, its `.env` files included.
  The protected-path checks above still refuse commands that name them, but
  code that builds the path at run time can read them. Keep real secrets out of
  the repository, or out of the sandbox's reach.
- **On Linux, `.git` is writable** while the command runs: Landlock cannot
  carve a read-only subtree out of a writable one (on macOS, Seatbelt denies
  those writes up front). So after every confined command ouija puts back what
  git runs, or reads commands from, on its own: a hook the command added or
  changed is removed or restored (a `hooks` directory swapped for a link
  included), and if it added a config entry that runs a command
  (`core.fsmonitor`, `core.hooksPath`, `core.editor`, a `!` alias, a filter, a
  credential helper, an `include`, `extensions.worktreeConfig`, …) that config
  goes back to how it was. A config ouija cannot parse the way git does goes
  back whole. This covers `.git/config`, `config.worktree`, `commondir`,
  `info/attributes`, the hooks and config of every submodule under
  `.git/modules/` and of every linked worktree under `.git/worktrees/`, and
  `.git` itself: a git directory swapped for a `gitdir:` pointer or a link
  loses the pointer.

  A `core.hooksPath` the repository already set (husky's `.husky/_`, for
  example) is watched too, for the hook names git runs from it (and, for
  husky, the helpers they source and the `.husky/<hook>` scripts they run),
  never for the other files there. So is the config of a hook manager whose
  hook is installed (`.pre-commit-config.yaml` for pre-commit, `lefthook.yml`
  for lefthook): a change to it is undone, a legitimate one included, so make
  those edits with the file tools, which ask first.

  Repositories nested in the project (a package installed from source under
  `vendor/`, a submodule's work tree) are found and checked the same way, to a
  depth of 8 directories. ouija does not walk `node_modules`, but checks the
  packages right under it (`node_modules/<pkg>`, `node_modules/@scope/<pkg>`),
  where a package installed from git keeps its repository. A config file a
  repository's config already includes from the work tree (`include.path`,
  `includeIf`) is watched like the config itself. A repository the command
  creates keeps everything but its hooks and the config entries that run a
  command. In a project with more than 50,000 directories the search stops
  early, and the result says so. The result names what was put back. It is
  narrower than macOS's up-front denial: a git directory moved away is not
  brought back. Other commands, unconfined or approved outside the
  sandbox, still get the **git guard** notice: ouija fingerprints the same
  files around every bash command and the chat shows a banner when one
  changes. The file tools refuse `.git/hooks` and `.git/config` on every OS.
- **Other project files that tools run are not watched.** The project is
  writable, so a confined command can change what a build or an editor runs
  later, unconfined: `.yarnrc.yml` (`yarnPath`), a project `.npmrc`,
  `package.json` scripts, a `Makefile`, `.vscode/tasks.json`, `.venv/bin`.
  Review changes to such files (`git diff`) before you run them outside
  ouija.

## Layer 5: Doom-loop detection

A doom loop is a model stuck repeating one call. `DoomDetector` blocks the
model when it calls the same tool with identical input **3+ times in a row**. A
fresh agent run or a user approval resets the counter.

## The order of checks

One function, `ToolRunner.Run`, decides every tool call. It runs the
doom-loop check first, then refuses a bash command with a NUL byte or with a
character the approval prompt would hide (a control character, a bidi
override, a zero-width character), then runs the other static bash gates (denylist, protected paths) *before* any prompt.
Next comes the prompt for a run outside the sandbox (`"unsandboxed": true`),
then the permission level, then the allowlist, then execution.
[Architecture → Tool gating](architecture.md#tool-gating-permissions) lists the
full order, with the code path for each step.

## Approval workflow (chat TUI)

When a tool requires approval (`ask` level), the TUI shows:

- the tool's icon, name and call number (e.g. `[#2]`)
- for a file tool, the file it changes, as the project-relative path it lands
  on: cleaned (`docs/x/../../Makefile` is `Makefile`) and with symlinks
  followed, shown with both names through a link
  (`notes.txt → Makefile (symlink)`). It wraps, never cut.
- all input arguments
- five options, chosen with the arrow keys: **Yes** (`y`), **No** (`n`),
  **Session** (`s`, approves for the rest of the session), **Edit** (`e`, opens
  the proposed content in `$EDITOR`), **Cancel** (`esc`). For `delete_file` the
  destructive button reads **Delete** (`d`) instead of **Yes**, so a stray `y`
  cannot delete a file.

Approvals apply to the tool's **target**, not only to its name: choosing
**Session** on `write_file` for `src/a.php` does not pre-approve `src/b.php`,
and **Session** on a bash command does not pre-approve a different command. A
file's target is where it physically lands: once `notes.txt` becomes a link to
the `Makefile`, writing it asks again, and a call whose link changed while the
prompt waited is refused. For
bash, **Session** also adds one allowlist pattern per simple command (none for
a command with substitutions or variables) and saves them to the config file;
**Yes** never saves. Rejecting a tool blocks it for the rest of the generation.

Text from outside ouija (a model's reply, a file, a config, a provider's
error) reaches the terminal only after ouija drops what a terminal acts on or
hides: escape sequences and other control characters (one could set the
clipboard or rewrite lines already on screen), bidi controls, zero-width and
tag characters. The chat, the approval prompt, the progress board,
`ouija doctor` and `ouija learn` all print through that filter;
`ouija trust` shows those characters as `\u{XXXX}` instead, so a reviewer
sees them.

## Cost and egress controls

A token, step and USD budget bounds cost, and its sanity checks reject negative
token counts. Secrets come from the config file (`0600`), and ouija never logs
or traces them. The bash tool sanitizes the environment before it passes it to
subprocesses. `sanitizeEnv()` strips variables by name (`AWS_*`, `OUIJA_*`,
provider prefixes such as `OPENROUTER_*`, `*API_KEY*`, `*_KEY`, `*SECRET*`,
`*TOKEN*`, `*AUTH*`, `*PRIVATE*`, `*_DSN`, `*_PWD`, `DATABASE_URL`, etc.).
Whatever the name, it also strips a value that holds a known token format
(`sk-…`, `ghp_…`, `AKIA…`, a JWT, a PEM private key) or a URL or DSN with a
password. `PATH` is always kept. It is still a denylist: a secret in an
unusual format under an unusual name still reaches bash. The sandbox, with
its network closed, keeps such a secret from leaving the machine, but bash
still sees it, so keep
secrets out of the environment you start ouija from. Subprocesses get
`WaitDelay = 3s` as a safety net against runaway children.

The provider client follows a redirect only to the same scheme and host. Go
drops `Authorization` on a redirect to another host but keeps the other
headers, so Anthropic's `x-api-key` (and any extra header a provider sends)
would have reached whatever host the redirect named; a refused redirect is
reported as an HTTP error instead. Responses are bounded too: 32 MiB for a
whole body, 8 MiB for one streamed line, 32 MiB for one streamed event and
64 MiB for the text and tool input of one streamed reply, and a model listing
follows at most 50 pages, so a broken or hostile endpoint cannot fill the
memory or keep ouija paging forever.

The files that hold prompts and file contents are owner-only. `/debug` in the
chat logs to `chat-debug.log` in ouija's directory under the user cache
directory (next to the sessions, mode `0700`), as a `0600` file it refuses to
open through a link. A Perfetto trace file is `0600` too, also when it
overwrites an older, wider one.

Egress controls stop data from leaving the machine through ouija's outputs. PDF
rendering runs headless Chrome with argv only, controlled paths, and a
throwaway `--user-data-dir`. The model-written HTML gets a
Content-Security-Policy as the first element of its `<head>`, which allows
inline styles, Google Fonts and `data:` images only: no scripts, frames, remote
images or requests, no `<base>` and no form targets. A policy cannot stop
navigation or resource hints, so before the HTML is written ouija removes every
`<meta http-equiv>` (a `refresh` to an attacker's URL is the classic) and every
`<link>` whose `rel` fetches on its own (`preconnect`, `dns-prefetch`,
`prefetch`, `prerender`, `preload`, the favicons and the like), in any
spelling, and turns off DNS prefetching. The HTML declares UTF-8 ahead of the
policy, and the model's own encoding declarations, invalid UTF-8 and control
characters (an escape sequence that another encoding would decode into a
hidden tag) are removed. The render itself is cut off the network as well: in Chrome no
host name resolves, and every connection, to a name or to a bare IP address,
goes to a proxy that refuses it. The two Google Fonts hosts are the one
exception, so the PDF keeps the template's fonts: a request to them can reach
Google, never an attacker's server. Content injected through the repository therefore cannot
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
reserve that setting for trusted projects. A single-word allowlist pattern
(`git`, `npm`) trusts everything that program can do.

The OS sandbox leaves gaps it documents above: where user namespaces are
forbidden, a process that calls `setsid` outlives its command (confined, but
still able to write in the project later); the project's own files that builds and editors run are writable; and on
Linux the git restore does not reach repositories nested deeper than 8
directories.
Where the default sandbox cannot be enforced, bash runs unconfined, and ouija
says so when the chat starts.

## Reporting a vulnerability

See [SECURITY.md](../../SECURITY.md) to report privately through GitHub
Security Advisories. Changes to security-sensitive code follow extra rules in
[Contribute](../how-to/contribute.md#security-sensitive-code).

---

🔮 ouija docs — [Index](../index.md) · [Architecture](architecture.md)
