🔮 ouija docs · [Index](../index.md)

---

# Install

> Installing ouija. Audience: user

## Requirements

- **Go 1.26+**. ouija uses `iter.Seq2`, `log/slog`, `slices`, `errors.Join`,
  `clear` and `range-over-int`.
- At least one configured provider (the service that runs the model): an API
  key entered with `ouija setup`, or a local Ollama, which needs no key.
- `make` (optional). Every target has a plain `go` equivalent.
- **Windows:** the `bash` tool needs a POSIX shell on `PATH`, such as the
  `sh.exe` that [Git for Windows](https://gitforwindows.org) installs. Without
  one, ouija refuses every bash call and says why; the other tools still work.
  "Edit in $EDITOR" opens Notepad unless `EDITOR` is set.

`go.mod` declares the runtime dependencies, and Go fetches them automatically,
so you install nothing by hand. They are the Charm stack for the TUI,
`mvdan.cc/sh/v3` for parsing bash and `golang.org/x/sync`; everything else is
the standard library. [Conventions](contribute.md#dependencies) gives the full
list and the reason for each.

## Pre-built archives (no Go needed)

CI builds every [GitHub Release](https://github.com/discorgento/ouija/releases).
Each release carries a `SHA256SUMS` file and one archive per platform, named
after the version:

- `ouija_<version>_<os>_<arch>.tar.gz` for Linux and macOS (amd64, arm64)
- `ouija_<version>_windows_amd64.zip` for Windows

(`v0.1.0` used `ouija-<os>-<arch>.tar.gz`.)

ouija is a terminal program, with no installer window or app icon. Once
`ouija` is on your `PATH`, it is installed, and you run it from a terminal.

### Linux and macOS

Run one command; it needs no root access:

```sh
curl -fsSL https://raw.githubusercontent.com/discorgento/ouija/main/scripts/install.sh | sh
```

The script picks the archive for your OS and CPU from the latest release,
checks it against `SHA256SUMS` and puts `ouija` and `ouija-mcp` in
`~/.local/bin`. If that folder is not on your `PATH`, the script prints the
line to add. To pass options (`--version 0.2.0`, `--dir <dir>`), put them
after `sh -s --`.

To install by hand, check and extract the archive:

```sh
sha256sum -c SHA256SUMS --ignore-missing      # macOS: shasum -a 256 -c --ignore-missing
tar -xzf ouija_0.2.0_linux_amd64.tar.gz       # ouija + ouija-mcp inside
```

Put both binaries on your `PATH` (see the last step below).

### Windows

Run this in PowerShell; it needs no administrator rights:

```powershell
irm https://raw.githubusercontent.com/discorgento/ouija/main/scripts/install.ps1 | iex
```

The script downloads the latest release's zip, checks it against
`SHA256SUMS`, puts `ouija.exe` and `ouija-mcp.exe` in
`%LOCALAPPDATA%\Programs\ouija` and adds that folder to your user's `PATH`.
Then open a **new** terminal (Windows Terminal or PowerShell), run
`ouija setup`, and run `ouija` in a project folder. If you save the script as
a file, it accepts `-Version 0.2.0`, `-InstallDir <dir>` and `-NoPath`.

To install by hand, download the zip and extract it (Explorer opens it). Then
add the folder that holds `ouija.exe` to your `PATH` under *Settings → System
→ About → Advanced system settings → Environment Variables*. The `bash` tool
also needs [Git for Windows](https://gitforwindows.org), as the requirements
above explain.

## Install the CLI

```sh
go install ./cmd/ouija
```

Run it from a checkout of the repository. Inside a checkout, `make install`
installs both binaries for you: this one and the
[MCP server](#install-the-mcp-server-optional).

> `@latest` works only for modules published at a remote path
> (`go install example.com/mod/…@latest`). The module path is
> `github.com/discorgento/ouija`, so
> `go install github.com/discorgento/ouija/cmd/ouija@latest` works once the
> repository is published there. A relative `go install` with `@latest` fails
> with `directory not found`.

Go puts the binary in `$(go env GOBIN)`, or in `$(go env GOPATH)/bin` when
`GOBIN` is unset. This prints the exact directory:

```sh
d="$(go env GOBIN)"; [ -n "$d" ] || d="$(go env GOPATH)/bin"; echo "$d"
```

The command is the same on Linux and macOS. If the directory is not on your
`PATH`, add it in your shell's config file. The file depends on the shell, not
on the OS: `~/.zshrc` for zsh, `~/.bashrc` for bash,
`~/.config/fish/config.fish` for fish.

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
