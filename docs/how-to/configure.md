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

To find the project config, ouija walks up from the current directory and
looks for **`.ouija/config.json`** first, then `.ouija.json`, then
`ouija.json`. It uses the first file it finds.

## Global setup: `ouija setup`

```sh
ouija setup
```

The interactive wizard asks two groups of questions:

1. **API keys** — one input per provider. Ollama runs locally, so it needs
   no key and the wizard skips it.
2. **Defaults** — your default provider and model.

It saves your answers to `~/.config/ouija/config.json`:

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

`init` starts from your global defaults and lets you override the provider,
the model and, optionally, the API key (for example, a separate key for this
project). As in `ouija setup`, it lists the models from the provider's own
API.

It saves the result to `.ouija/config.json`. `init` writes the **whole**
project config, not just the two keys: it starts from the built-in defaults,
lays the global defaults over them, then applies your choices. The file
therefore also carries `maxNudges`, `budget` (`maxTokens`/`maxSteps`/`maxUSD`),
`rules`, `projectRoot`, `systemPrompt`, `costPer1MInput` and
`costPer1MOutput`. The part you edit can be as small as:

```json
{
  "provider": "openrouter",
  "model": "qwen/qwen3-coder:free"
}
```

`init` writes the `apiKey` field only when it differs from the global key.
The key belongs to the project's `provider`. If you switch provider in the
chat (`/provider`), ouija uses the new provider's global key, and saving that
switch removes the old provider's `apiKey` from the file. `init` leaves out
the optional sections (`permission`, `tools`, `modelSuggestions`,
`streamTimeout`) unless you set them.

`init` also marks the file it wrote as trusted. A project config you did not
write, such as one a teammate committed or one in a repository you cloned,
applies its `permission`, `tools`, `apiKey`, `systemPrompt` and `projectRoot`
keys only after you review and trust it:

```sh
ouija trust          # prints what the config grants, then trusts it
ouija trust --revoke
```

Any change to the file revokes the trust; `ouija doctor` reports when that
happens.

## Config validation

ouija validates the config every time it loads it:

- **`Config.Validate()`** checks every field (provider, model, maxNudges,
  budget limits, `totems.<name>` sections) and joins all the errors with
  `errors.Join`.
- **`ValidateConfig()`** also checks that the provider is registered.

So a bad config fails at startup, not in the middle of a run.

## Environment variables

`OUIJA_LOG_LEVEL` sets how much ouija logs: `debug`, `info` (default),
`warn` or `error`.

The providers and the TUI read a few others: `EDITOR` (for the chat's Edit
approval option), `OPENROUTER_BASE_URL`, `OPENROUTER_REFERER`,
`OPENROUTER_TITLE` and `OLLAMA_HOST`. ouija never reads API keys from
environment variables; they live in config files with `0600` permissions.

---

🔮 ouija docs — [Index](../index.md) · [Install](install.md)
