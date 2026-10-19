🔮 ouija docs · [Index](../index.md)

---

# Environment variables

> Every environment variable ouija reads. Audience: reference

API keys are **not** environment variables — they live in the config files with
`0600` permissions (see [Configure](../how-to/configure.md)). The variables
below only tune behaviour.

| Variable | Read by | Purpose |
|---|---|---|
| `OUIJA_LOG_LEVEL` | engine | Log verbosity: `debug`, `info` (default), `warn`, `error` |
| `OUIJA_THEME` | chat, CLI | Color theme for this run (`ouija`, `catppuccin`, `dracula`, `mono`), over the config's `theme` |
| `XDG_CACHE_HOME` | chat TUI | Where conversations are saved: `$XDG_CACHE_HOME/ouija/sessions/` (default `~/.cache`) |
| `XDG_CONFIG_HOME` | config discovery | Where the global config lives: `$XDG_CONFIG_HOME/ouija/config.json` (default `~/.config`) |
| `EDITOR` | chat TUI | Editor used by the **Edit** approval option, default `vi` |
| `OPENROUTER_BASE_URL` | openrouter provider | Override the OpenRouter endpoint |
| `OPENROUTER_REFERER` / `OPENROUTER_TITLE` | openrouter provider | Attribution headers OpenRouter uses for rankings |
| `OLLAMA_HOST` | ollama provider | Ollama server address (default `http://localhost:11434`) |

## Examples

```sh
OUIJA_LOG_LEVEL=debug ouija task "fix sorting bug"   # verbose engine logs on stderr
EDITOR=nvim ouija chat                                # Edit approval opens nvim
OLLAMA_HOST=http://192.168.1.10:11434 ouija chat     # remote Ollama
```

## Notes

- The TUI silences engine logging by discarding it; `OUIJA_LOG_LEVEL` matters
  for totems and `ouija mcp`.
- Subprocesses started by the `bash` tool get a **sanitized** environment:
  credential-like variables (`AWS_*`, `*API_KEY*`, `*SECRET*`, `*TOKEN*`, …) are
  stripped before exec. See [Security](../explanation/security.md).
- `PATH` matters for MCP hosts: hosts spawn the server by name, and a GUI host
  launched from the desktop may not inherit your shell's `PATH` — see
  [MCP integration](../how-to/integrate-mcp.md).

---

🔮 ouija docs — [Index](../index.md) · [Configure](../how-to/configure.md)
