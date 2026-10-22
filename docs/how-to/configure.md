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
`ouija.json`. It uses the first file it finds. A discovered project config
applies its trust-only keys only once you trust it (see below), and a
`sandbox` in the global config is a floor no project config lowers.

## Global setup: `ouija setup`

```sh
ouija setup
```

The interactive wizard has three steps:

1. **Provider**: who runs the model. Each option says what it is; Ollama runs
   on your computer, for free.
2. **API key**: only for the provider you picked, with the page to get one.
   Ollama needs no key, so the wizard skips this step. If a key is already
   saved, leave the field blank to keep it.
3. **Model**: your default model, listed from the provider's own API with the
   key you just typed. Suggested models come first, marked ★, and `/` searches
   a long list such as OpenRouter's.

Keys you saved for other providers stay as they are; run the wizard again to
switch providers or add another key.

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
  "model": "openrouter/free"
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
applies its `permission`, `tools`, `apiKey`, `systemPrompt`, `rules`,
`modelSuggestions` and `projectRoot` keys, a `budget` above the defaults, a
`provider` or `model` other than the ones you chose in `ouija setup`, and
raised totem `passes`, `streamTimeout` or `contextWindow`, only after you
review and trust it. The
[trust-only keys](../reference/config-schema.md#project-config-ouijaconfigjson)
list them all.

```sh
ouija trust          # prints what the config grants, then asks [y/N]
ouija trust --yes    # trusts without asking, for scripts
ouija trust --revoke
```

`ouija trust` shows every key in full, and any hidden or control character in
it as a visible `\u{XXXX}` escape. Any change to the file revokes the trust;
`ouija doctor` lists the keys an untrusted file has ignored.

Discovery stops at your home directory when you work inside it, and on Linux
and macOS it skips a config file another user owns. A config that sets the
same key twice is refused.

## Config validation

ouija validates the config every time it loads it:

- **`Config.Validate()`** checks the provider, the model, `maxNudges` and the
  budget limits, and joins all the errors with `errors.Join`.
- **`ValidateConfig()`** also checks that the provider is registered.

So a bad config fails at startup, not in the middle of a run. One exception:
ouija does not check a `totems.<name>` section up front. A malformed one fails
when that totem reads it.

## Bash sandbox

The OS confines every bash command, by default, on Linux (Landlock ABI v9+,
on x86-64 or arm64) and on macOS (Seatbelt). A confined command writes only
inside the project and private temp and cache dirs, reads no credentials in
your home, and opens no TCP connection. On Linux it also runs in user and PID
namespaces of its own where the kernel allows them, so nothing it starts
outlives it. `ouija doctor` shows whether the sandbox is on (`bash sandbox`)
and, on Linux, whether background jobs are contained (`bash jobs`).

When a build needs more, open it under `tools.bash.sandbox` in the project
config:

```json
{
  "tools": {
    "bash": {
      "sandbox": {
        "enabled": true,
        "read": ["~/sdk"],
        "write": ["/dev/shm"],
        "network": { "connect": [443] }
      }
    }
  }
}
```

The project section applies only once you trust the file. To turn the sandbox
off, set `"sandbox": {"enabled": false}` in the global config, or
`tools.bash.sandbox.enabled` to `false` in a trusted project config (this has
no effect when the global config sets a `sandbox`).

When a confined command fails, the model may retry it with
`"unsandboxed": true`. You then get a prompt for that exact command, every
time. `"allowUnsandboxed": false` in the sandbox section turns that escape off.
See [`tools.bash.sandbox`](../reference/config-schema.md#toolsbashsandbox) for
every key.

## Environment variables

`OUIJA_LOG_LEVEL` sets how much ouija logs: `debug`, `info` (default),
`warn` or `error`.

The providers and the TUI read a few others, such as `EDITOR` (for the
chat's Edit approval option), `OUIJA_THEME`, `OPENROUTER_BASE_URL` and
`OLLAMA_HOST`; [Environment variables](../reference/environment.md) lists
them all. ouija never reads API keys from environment variables; they live in
config files with `0600` permissions.

---

🔮 ouija docs — [Index](../index.md) · [Install](install.md)
