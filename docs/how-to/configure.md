🔮 ouija docs · [Index](../index.md)

---

# Setup & config

> Configuring ouija — global defaults, per-project overrides, and API keys.
> Audience: user

## Configuration layers

ouija uses two layers:

| Scope | File | Purpose |
|---|---|---|
| **Global** | `~/.config/ouija/config.json` on Linux (or `$XDG_CONFIG_HOME/ouija/`); `~/Library/Application Support/ouija/` on macOS; `%AppData%\ouija\` on Windows | Default provider, model, API keys (`0600`) |
| **Project** | `.ouija/config.json` | Per-project overrides |

Priority (highest wins): **project config > global config > built-in defaults**

Config discovery walks up from the current directory and
looks for **`.ouija/config.json`** first, then `.ouija.json`, then `ouija.json` —
the first one found wins.

## Global setup: `ouija setup`

```sh
ouija setup
```

Interactive wizard in two groups:

1. **API keys** — one input per provider (Ollama skipped — it's local)
2. **Defaults** — pick your default provider and model

Saved to `~/.config/ouija/config.json`:

```json
{
  "apiKeys": {
    "anthropic": "sk-ant-...",
    "openai": "sk-proj-..."
  },
  "defaults": {
    "provider": "anthropic",
    "model": "claude-opus-5-5"
  }
}
```

## Per-project config: `ouija init`

Inside a project directory:

```sh
ouija init
```

Pre-fills from global defaults and lets you override provider, model (listed
from the provider's own API, like in `ouija setup`), and optionally
the API key (e.g. a client-specific key).

Saved to `.ouija/config.json`. `init` writes the **whole** project config, not
just the two keys: it starts from the built-in defaults, overlays the global
defaults, then applies your choices, so the file also carries `maxNudges`,
`budget` (`maxTokens`/`maxSteps`/`maxUSD`), `rules`,
`projectRoot`, `systemPrompt`, `costPer1MInput` and `costPer1MOutput`. What you
edit can be as small as:

```json
{
  "provider": "openrouter",
  "model": "qwen/qwen3-coder:free"
}
```

The `apiKey` field is only written when it differs from the global one. It
belongs to the project's `provider`: switching provider in the chat
(`/provider`) uses the new provider's global key, and saving that switch drops
the old provider's `apiKey` from the file.
Optional sections (`permission`, `tools`, `modelSuggestions`, `streamTimeout`)
are omitted when unset.

`init` also trusts the file it wrote. A project config you did not write —
committed by a teammate, or in a repository you cloned — applies its
`permission`, `tools`, `apiKey`, `systemPrompt` and `projectRoot` keys only
after you review and trust it:

```sh
ouija trust          # prints what the config grants, then trusts it
ouija trust --revoke
```

Any change to the file lapses the trust (`ouija doctor` shows it).

## Config validation

Config is validated automatically on load:

- **`Config.Validate()`** — checks all fields with `errors.Join` (provider, model,
  maxNudges, budget limits, `totems.<name>` sections)
- **`ValidateConfig()`** — also checks the provider is registered

Errors are reported at startup, not mid-run.

## Environment variables

`OUIJA_LOG_LEVEL` — set log verbosity: `debug`, `info` (default), `warn`, `error`.

A few others are read by the providers and the TUI: `EDITOR` (the chat's Edit
approval option), `OPENROUTER_BASE_URL`, `OPENROUTER_REFERER`,
`OPENROUTER_TITLE` and `OLLAMA_HOST`. API keys are never read from environment
variables — they live in config files with `0600` permissions.

---

🔮 ouija docs — [Index](../index.md) · [Install](install.md)
