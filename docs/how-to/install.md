🔮 ouija docs · [Index](../index.md)

---

# Install

> Installing ouija. Audience: user

## Requirements

- **Go 1.26+** — uses `iter.Seq2`, `log/slog`, `slices`, `errors.Join`, `clear`, `range-over-int`
- At least one configured provider — an API key via `ouija setup`, or a local Ollama, which needs no key
- `make` (optional) — every target has a raw `go` equivalent

Runtime dependencies are declared in `go.mod` and are fetched automatically —
there is nothing to install by hand (Charm stack for the TUI, `mvdan.cc/sh/v3`
for bash parsing, `golang.org/x/sync`; everything else is the standard library).
The full list with rationale is in [Conventions](contribute.md#dependencies).

## Pre-built archives (no Go needed)

Every [GitHub Release](https://github.com/discorgento/ouija/releases) is built
by CI and carries `ouija-<os>-<arch>.tar.gz` archives (linux, macOS and Windows
— amd64/arm64) plus a `SHA256SUMS` file. Verify, then extract:

```sh
sha256sum -c SHA256SUMS --ignore-missing   # macOS: shasum -a 256 -c
tar -xzf ouija-linux-amd64.tar.gz          # ouija + ouija-mcp inside
```

Put both binaries on your `PATH` (see the last step below).

## Install the CLI

```sh
go install ./cmd/ouija
```

> `@latest` only works for modules published at a remote path
> (`go install example.com/mod/…@latest`). The module path is
> `github.com/discorgento/ouija`, so
> `go install github.com/discorgento/ouija/cmd/ouija@latest` works once the
> repository is published there; a relative `go install` with `@latest` fails
> with `directory not found` — install from a checkout. Inside a checkout,
> `make install` runs both commands for you.

The binary goes to `$(go env GOBIN)` — or `$(go env GOPATH)/bin` when `GOBIN` is
unset. Print the exact directory with:

```sh
d="$(go env GOBIN)"; [ -n "$d" ] || d="$(go env GOPATH)/bin"; echo "$d"
```

Same command on Linux and macOS. If it is not on your `PATH`, add it in your
shell's config file — which one depends on the shell, not on the OS (zsh:
`~/.zshrc`, bash: `~/.bashrc`, fish: `~/.config/fish/config.fish`).

## Install the MCP server (optional)

```sh
go install ./cmd/ouija-mcp
```

## Verify

```sh
ouija version
# ouija 1.0.0
```

---

🔮 ouija docs — [Index](../index.md) · [Configure](configure.md)
