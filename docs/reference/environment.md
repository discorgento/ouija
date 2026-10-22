🔮 ouija docs · [Index](../index.md)

---

# Environment variables

> Every environment variable ouija reads. Audience: reference

API keys are **not** environment variables: they live in the config files, with
`0600` permissions (see [Configure](../how-to/configure.md)). The variables
below only tune behaviour.

| Variable | Read by | Purpose |
|---|---|---|
| `OUIJA_LOG_LEVEL` | engine | Log verbosity: `debug`, `info` (default), `warn`, `error` |
| `OUIJA_NO_ANIMATION` | chat, CLI | Any value turns off the animations (the intro, the flowing colors, the shimmer) for a still screen |
| `OUIJA_THEME` | chat, CLI | Color theme for this run (`ouija`, `catppuccin`, `dracula`, `mono`); overrides the config's `theme` |
| `OUIJA_SANDBOX_POLICY` | bash sandbox | Internal: ouija sets it for the sandboxed child process it starts, which removes it before the shell starts. Do not set it |
| `XDG_CACHE_HOME` | chat TUI, model lists, bash sandbox | The user cache dir: conversations (`ouija/sessions/`), the `/debug` log (`ouija/chat-debug.log`, mode `0600`), the model list cache (`ouija/models/`) and the bash sandbox's private build cache (`ouija/sandbox/`). Default `~/.cache`. Linux and other Unix only: macOS always uses `~/Library/Caches` and Windows `%LocalAppData%`, ignoring the variable |
| `XDG_CONFIG_HOME` | config discovery, `ouija mcp install` | Where the global config lives: `$XDG_CONFIG_HOME/ouija/config.json` (default `~/.config`). A relative value is ignored: ouija then runs with no global config and trusts nothing. Linux and other Unix only: macOS always uses `~/Library/Application Support` and Windows `%AppData%`, ignoring the variable. The exception is `ouija mcp install`, which uses it on every OS to find OpenCode's config (default `~/.config/opencode/`) |
| `EDITOR` | chat TUI | Editor that the **Edit** approval option opens (default `vi`; `notepad` on Windows) |
| `GIT_CONFIG_GLOBAL` / `GIT_CONFIG_COUNT` | bash sandbox, `review` totem | When set, the sandbox keeps your choice. Otherwise it points git at `/dev/null` for the global config and passes on your `user.name` and `user.email`. The `review` totem numbers the `GIT_CONFIG_*` entries that turn off the repository's filters after yours |
| `HOME` | config discovery, sandbox, MCP server | Your home directory: discovery stops there, the sandbox grants and protected paths are computed from it, and `ouija mcp` refuses to serve from it or above it |
| `TERM` | totems | `TERM=dumb` turns off the live progress board |
| `ProgramFiles` / `ProgramFiles(x86)` / `LocalAppData` | `doc` totem (Windows) | Where it looks for Chrome or Edge when neither is on `PATH` |
| `OPENROUTER_BASE_URL` | openrouter provider | Overrides the OpenRouter endpoint |
| `OPENROUTER_REFERER` / `OPENROUTER_TITLE` | openrouter provider | Attribution headers OpenRouter uses for rankings |
| `OLLAMA_HOST` | ollama provider | Ollama server address (default `http://localhost:11434`) |

## Examples

```sh
OUIJA_LOG_LEVEL=debug ouija task "fix sorting bug"   # verbose engine logs on stderr
EDITOR=nvim ouija chat                                # Edit approval opens nvim
OLLAMA_HOST=http://192.168.1.10:11434 ouija chat     # remote Ollama
```

## Notes

- The TUI discards engine logs, so `OUIJA_LOG_LEVEL` matters for totems and
  `ouija mcp`.
- The `bash` tool starts subprocesses with a **sanitized** environment. It
  strips variables by name (`AWS_*`, `OUIJA_*`, the provider prefixes such as
  `OPENAI_*` and `GITHUB_*`, `*API_KEY*`, `*_KEY`, `*SECRET*`, `*TOKEN*`,
  `*AUTH*`, `*_DSN`, `*_PWD`, `DATABASE_URL`, …). Whatever the name, it also
  strips a value that holds a known token format (`sk-…`, `ghp_…`, `AKIA…`, a
  JWT, a PEM private key) or a URL or DSN with a password. `PATH` is always
  kept. See [Security](../explanation/security.md).
- A sandboxed command also gets `TMPDIR`, `GOCACHE`, `npm_config_cache`,
  `PIP_CACHE_DIR`, `XDG_CACHE_HOME` and similar set to private dirs (see
  [`tools.bash.sandbox`](config-schema.md#toolsbashsandbox)).
- `PATH` matters for MCP hosts, which start the server by name: a GUI host
  launched from the desktop may not inherit your shell's `PATH`. See
  [MCP integration](../how-to/integrate-mcp.md).

---

🔮 ouija docs — [Index](../index.md) · [Configure](../how-to/configure.md)
