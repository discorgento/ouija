🔮 ouija docs · [Index](../index.md)

---

# Contribute

> Workflow and code conventions for developing ouija. Audience: developer

## Requirements

- **Go 1.26+**. The `go` directive in `go.mod` sets the exact version.
- **`make`** (optional). Every command below has a plain `go` equivalent.

## The one rule: `make check` must pass

```sh
make check    # gofmt -l . + go vet ./... + go test ./...
```

Without `make`:

```sh
test -z "$(gofmt -l .)" && go vet ./... && go test ./...
```

CI runs that gate and more on every push and pull request. Its workflow,
`.github/workflows/ci.yml`, checks gofmt, `go.mod` tidiness and stale
generated code, runs vet for linux/darwin/windows, staticcheck, actionlint,
`go test -race ./...`, the build and govulncheck. A pull request with a failing
check cannot merge. Each extra check is a `make` target you can run locally:

```sh
make tidy-check gen-check cross-vet lint actionlint vuln
```

While iterating, run a single package or a single test:

```sh
go test ./engine -run TestBashDenylist -v
make test-race          # before touching concurrency (chat, engine)
```

## Build and run

```sh
make build                    # → ./ouija and ./ouija-mcp (gitignored)
make run ARGS='task "..."'    # build + run a totem
make chat                     # build + open the TUI
make help                     # every target
```

## Where to make changes

| Area | Read first |
|---|---|
| Anything that gates tool execution | [Architecture](../explanation/architecture.md) — permission rules, protected paths, bash denylist |
| The security model | [Security](../explanation/security.md) |
| A new totem | [Adding a totem](add-totem.md) |
| A new provider | [Adding a provider](add-provider.md) |
| Engine internals | [Engine](../explanation/engine.md) |

Tests live next to the code they test: `foo_test.go` sits beside `foo.go`.
Where the logic branches, make them table-driven.

## Go style

- Follow `go fmt` and `go vet`. `make check` enforces both, so run it before
  committing.
- Keep `go vet ./...` free of warnings.
- Leave no unused imports or variables (the Go compiler enforces this).
- Use `gofumpt` if you have it; standard `gofmt` is fine too.

## Error handling

- **Wrap errors** with `fmt.Errorf("context: %w", err)` to preserve the chain.
- **Use `errors.Join`** (Go 1.20+) to collect several errors, as in
  validation or fan-out.
- **Don't panic**, except in `init()` for a misconfiguration nothing can
  recover from (e.g. registering the same provider twice).
- Write error messages in lowercase, with no trailing punctuation.
- Validate early and return at once, so a bad input fails at startup, not in
  the middle of a loop.

```go
// Good
if err := validate(cfg); err != nil {
    return nil, fmt.Errorf("config: %w", err)
}

// Good — multi-error
var errs []error
if x == "" { errs = append(errs, errors.New("x is required")) }
if y <= 0  { errs = append(errs, errors.New("y must be > 0")) }
return errors.Join(errs...)

// Bad
return fmt.Errorf("Config is invalid: %s", err)
```

## Logging

- Use **`log/slog`** (Go 1.21+) and log structured key-value pairs, not
  formatted strings.
- Use **Info** for notable events (agent start, completion, config path).
- Use **Warn** for issues ouija recovers from (response truncated, fallback
  activated).
- Use **Debug** for verbose details (tool calls, token counts). `OUIJA_LOG_LEVEL`
  decides whether they print.
- Never log API keys or other sensitive data; ouija reads the keys from
  `0600` files.
- In totems, use the engine's logger (`eng.Logger()`), not a global logger.

```go
// Good
eng.Logger().Info("agent start", "label", label)
eng.Logger().Warn("response truncated", "label", label, "stop_reason", "length")
eng.Logger().Debug("tool call", "name", call.Name, "input", call.Input)

// Bad
fmt.Fprintf(os.Stderr, "[ouija] starting agent %s\n", label)
```

## Naming

| Concept | Name | Convention |
|---|---|---|
| Workflow | **totem** | Package `totems/<name>/`, type `Totem` |
| Core loop | **engine** | Package `engine`, type `Engine` |
| LLM adapter | **provider** | Package `providers`, type `<Name>` (`Anthropic`, `OpenAI`, …) |
| Sandboxed action | **tool** | Package `engine`, registered in `tools.go` |
| Session log | **trace** | Perfetto/Chrome Trace JSON written by `Tracer` |
| Budget | **budget** | Package `engine`, type `tracker` (unexported) |
| Gate | **gate** | `gateState` shared between `Engine` and `ToolRunner` (permissions, approvals, tool callback) |

- File names: **snake_case.go** (Go standard).
- Directories: **kebab-case** or single word.
- Exported names are PascalCase (Go standard).

## Testing

- Use **table-driven tests** where they fit.
- Report errors through `*testing.T`, and use `t.Fatalf` when the test cannot
  go on.
- **Mock providers** with `mockProvider` (see `loop_test.go`).
- Create temporary directories for file operations with `t.TempDir()`.
- Put test files next to the source file (`foo_test.go`).
- Mark tests with `t.Parallel()` when they are safe to run in parallel.

```go
func TestEngineLoopToolThenSubmit(t *testing.T) {
    mp := &mockProvider{responses: []ChatResponse{
        {ToolCalls: []ToolCall{{ID: "1", Name: "read_file", Input: map[string]any{"path": "f.txt"}}}},
        {ToolCalls: []ToolCall{{ID: "2", Name: SubmitTool, Input: map[string]any{"ok": true}}}},
    }}
    eng := newTestEngine(mp, t.TempDir())
    res, err := eng.Run(context.Background(), RunRequest{Schema: map[string]any{"type": "object"}, UseTools: true})
    if err != nil { t.Fatalf("run: %v", err) }
    if res.Text != `{"ok":true}` { t.Fatalf("got %q", res.Text) }
}
```

## Module organization

| Directory | Import path | Exported? |
|---|---|---|
| `engine/`, `registry/`, `schema/`, `bootstrap/` | `github.com/discorgento/ouija/...` | Public: what an external totem imports |
| `totems/*` | `github.com/discorgento/ouija/totems/...` | Public registration of the built-in totems |
| `cmd/*` | `github.com/discorgento/ouija/cmd/...` | Entry points |
| `internal/*` | `github.com/discorgento/ouija/internal/...` | Internal only |

The engine and the packages a totem needs are public, so a totem can live in
another module ([Add a totem](add-totem.md)). Everything else, from the chat to
the CLI commands, lives in `internal/` and cannot be imported from outside.

## Dependencies

- Runtime: Bubble Tea (`charm.land/bubbletea/v2`, `bubbles/v2`, `lipgloss/v2`,
  `glamour/v2`) and `huh/v2` for the interactive chat TUI and commands,
  `alecthomas/chroma/v2` for syntax highlighting in the chat,
  `atotto/clipboard` for copying a reply or a code block,
  `charmbracelet/x/ansi` for terminal styling, `charmbracelet/x/term` for
  terminal detection and `charmbracelet/colorprofile` for the terminal's color
  support. The engine uses `golang.org/x/sync` (errgroup), `mvdan.cc/sh/v3`
  (shell parsing for the bash tool) and `landlock-lsm/go-landlock` (the Linux
  bash sandbox).
- Everything else comes from the Go standard library.
- Vendor nothing; `go.mod` manages every dependency.

## Security-sensitive code

Permission gating (`engine/toolrunner.go`, `engine/permission.go`), the
filesystem sandbox and the link checks (`engine/sandbox.go`,
`engine/protect_links.go`, `engine/protect_*.go`), the file tools
(`engine/tools_files.go`, `engine/tools_write.go`), the bash OS sandbox
(`engine/bash_sandbox*.go`, its seccomp filter included) and the git restore
after it (`engine/git_restore.go`, `engine/git_guard.go`), the bash denylist
(`engine/tools_bash.go`), project trust (`internal/app/trust.go`), config
handling, the provider client's redirect and size limits
(`engine/providers/client.go`), the MCP server (`internal/mcp/`) and the
terminal filter (`internal/termsafe`) all **fail closed**: when in
doubt, they deny. A change that makes any of them more permissive must come
with tests that prove the previous behaviour was wrong. Without such tests,
treat it as a security change and open an issue before writing code.

## Commit messages

Follow [Conventional Commits](https://www.conventionalcommits.org/):
`type(scope): description`, in lowercase and the imperative mood:

```
fix(engine): reject recursive rm on protected paths
feat(chat): add /export slash command
docs(reference): document exit codes
```

Types used here: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`,
`ci`, `chore`. The scope is the package or area (`engine`, `chat`, `mcp`,
`task`, `docs`, `release`). Leave out ticket numbers; this is an open-source
project.

Conventional Commits are more than style: they drive the release. `feat`,
`fix` and breaking changes go into the changelog and bump the version;
`chore`, `ci` and `test` do not.

## Releases

There is **no version to edit anywhere**: the git tag is the version.
A release happens in four steps:

1. Push to `main` with Conventional Commits.
2. [`release-please`](https://github.com/googleapis/release-please) opens a
   *release PR* with the bump, the new `CHANGELOG.md` section and the fallback
   in `internal/version/version.go`.
   Because a bot pushed that PR, GitHub holds its CI runs for approval, and
   `main` merges only after CI's checks pass. The release workflow approves
   the runs; if one still waits, its page has an **Approve and run** button.
3. Merging that PR creates the tag `vX.Y.Z` and the GitHub Release.
4. The same workflow runs the gate (gofmt, vet, tests) again on the tag, in a
   job of its own with a read-only token, and only once it passes
   cross-compiles with `make dist`. It then smoke-tests the archives (their checksums, and the
   binary's `version` against the tag), attests their provenance and attaches
   them to the release.

The workflow lives in `.github/workflows/release.yml`, and its config in
`release-please-config.json` and `.release-please-manifest.json`.

The manifest started at `0.0.0`, which release-please treats as *no release
yet*; a first release would have taken its default, `1.0.0`. A one-off
`"release-as"` set `v0.1.0` instead, and was removed after that release
shipped, because every later release PR would otherwise ask for the same
version again. Now `bump-minor-pre-major` keeps ouija below 1.0 while its API
settles: a breaking change bumps the minor version (`0.1.0` → `0.2.0`), and a
fix bumps the patch. Moving to `1.0.0` will be a deliberate decision, made
with `release-as` the same way.

## Documentation

The docs are plain Markdown under `docs/`, versioned and reviewed like code.
They are organized by what the reader wants: tutorial, how-to, reference or
explanation (see the [Index](../index.md)). `docs/_style-guide.md` holds the
formatting rules and the skeleton for a new page.

The checks in `internal/doccheck` make `go test ./...` fail when a move
breaks a relative link or anchor, and when the code gains a config key, a CLI
command or an `OUIJA_*` variable that the reference pages do not mention:
document it in `docs/reference/` in the same change. The same package checks absolute
`http(s)://` links, including the README badges, over the network in a
separate CI job: `DOCCHECK_EXTERNAL=1 go test ./internal/doccheck -run
TestExternalLinksReachable`.

## License

By contributing you agree that your work is released under the
[MIT License](../../LICENSE) — © 2026 discorgento.

---

🔮 ouija docs — [Index](../index.md) · [Architecture](../explanation/architecture.md)
