🔮 ouija docs · [Index](../index.md)

---

# Install

> Installing ouija. Audience: user

## Requirements

- **Go 1.26+** — uses `iter.Seq2`, `log/slog`, `slices`, `errors.Join`, `clear`, `range-over-int`
- At least one configured provider — an API key via `ouija setup`, or a local Ollama, which needs no key
- `make` (optional) — every target has a raw `go` equivalent
- **Windows:** the `bash` tool needs a POSIX shell on `PATH` — the `sh.exe`
  that [Git for Windows](https://gitforwindows.org) installs. Without one,
  every bash call is refused with that explanation; the other tools work.
  "Edit in $EDITOR" opens Notepad unless `EDITOR` is set.

Runtime dependencies are declared in `go.mod` and are fetched automatically —
there is nothing to install by hand (Charm stack for the TUI, `mvdan.cc/sh/v3`
for bash parsing, `golang.org/x/sync`; everything else is the standard library).
The full list with rationale is in [Conventions](contribute.md#dependencies).

## Pre-built archives (no Go needed)

Every [GitHub Release](https://github.com/discorgento/ouija/releases) is built
by CI and carries one archive per platform, named after the version —
`ouija_<version>_<os>_<arch>.tar.gz` for Linux and macOS (amd64, arm64),
`ouija_<version>_windows_amd64.zip` for Windows — plus a `SHA256SUMS` file.
(`v0.1.0` used `ouija-<os>-<arch>.tar.gz`.)

ouija is a terminal program: there is no installer window or app icon —
"installed" means `ouija` is on your `PATH`, and you run it from a terminal.

### Linux and macOS

```sh
sha256sum -c SHA256SUMS --ignore-missing      # macOS: shasum -a 256 -c --ignore-missing
tar -xzf ouija_0.2.0_linux_amd64.tar.gz       # ouija + ouija-mcp inside
```

Put both binaries on your `PATH` (see the last step below).

### Windows

In PowerShell — no administrator rights needed:

```powershell
irm https://raw.githubusercontent.com/discorgento/ouija/main/scripts/install.ps1 | iex
```

It downloads the latest release's zip, checks it against `SHA256SUMS`, puts
`ouija.exe` and `ouija-mcp.exe` in `%LOCALAPPDATA%\Programs\ouija` and adds
that folder to your user's `PATH`. Then open a **new** terminal (Windows
Terminal or PowerShell) and run `ouija setup`, then `ouija` in a project
folder. Saved as a file, the script takes `-Version 0.2.0`, `-InstallDir <dir>`
and `-NoPath`.

By hand: download the zip, extract it (Explorer opens it), and add the folder
holding `ouija.exe` to your `PATH` — *Settings → System → About → Advanced
system settings → Environment Variables*. The `bash` tool also needs
[Git for Windows](https://gitforwindows.org) (see the requirements above).

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
# ouija 1.2.3 — a release; a build from source before the first release says 0.0.0
```

---

🔮 ouija docs — [Index](../index.md) · [Configure](configure.md)
