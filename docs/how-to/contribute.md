🔮 ouija docs · [Index](../index.md)

---

# Contribute

> Workflow and code conventions for developing ouija. Audience: developer

## Requirements

- **Go 1.26+** — the `go` directive in `go.mod` is the source of truth
- **`make`** — optional; every command below has a raw `go` equivalent

## The one rule: `make check` must pass

```sh
make check    # gofmt -l . + go vet ./... + go test ./...
```

Without `make`:

```sh
test -z "$(gofmt -l .)" && go vet ./... && go test ./...
```

CI runs the same gate on every push and pull request (`.github/workflows/ci.yml`:
gofmt, vet, `go test -race ./...`, build) — a red PR is a PR that cannot merge.

A single package, or a single test, while iterating:

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

Tests live next to the code they test (`foo.go` → `foo_test.go`), table-driven
where the logic branches.

## Go style

- Follow `go fmt` and `go vet` — enforced by `make check` (run it before
  committing).
- Zero lint warnings on `go vet ./...`.
- No unused imports or variables (Go compiler enforces this).
- Use `gofumpt` if available, but standard `gofmt` is fine.

## Error handling

- **Wrap errors** with `fmt.Errorf("context: %w", err)` to preserve the chain.
- **`errors.Join`** (Go 1.20+) for multi-error collection (validation, fan-out).
- **Don't panic** except in `init()` for truly unrecoverable misconfiguration
  (e.g. duplicate provider registration).
- Error messages: lowercase, no trailing punctuation.
- Return early on validation — fail at startup, not mid-loop.

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

- Use **`log/slog`** (Go 1.21+) — structured key-value pairs, not formatted strings.
- **Info** for notable events (agent start, completion, config path).
- **Warn** for recoverable issues (response truncated, fallback activated).
- **Debug** for verbose details (tool calls, token counts — controlled by `OUIJA_LOG_LEVEL`).
- Never log API keys or sensitive data (they come from `0600` files).
- Use the engine's logger (`eng.Logger()`) in totems, not a global logger.

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

- Use **table-driven tests** where appropriate.
- Use `*testing.T` for errors, `t.Fatalf` for fatal.
- **Mock providers** via `mockProvider` (see `loop_test.go`).
- Temp dirs via `t.TempDir()` for file operations.
- Test files go next to the source file (`foo_test.go`).
- Parallel tests via `t.Parallel()` when safe.

```go
func TestEngineLoopToolThenSubmit(t *testing.T) {
    mp := &mockProvider{responses: []ChatResponse{
        {ToolCalls: []ToolCall{{ID: "1", Name: "read_file", Input: map[string]any{"path": "f.txt"}}}},
        {ToolCalls: []ToolCall{{ID: "2", Name: submitTool, Input: map[string]any{"ok": true}}}},
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
| `internal/*` | `github.com/discorgento/ouija/internal/...` | Internal only |
| `cmd/*` | `github.com/discorgento/ouija/cmd/...` | Entry points |
| `totems/*` | `github.com/discorgento/ouija/totems/...` | Public registration |

All domain logic is in `internal/`. Only `cmd/` and `totems/` are technically
importable by external consumers, but the public API is the CLI, not the Go
packages.

## Dependencies

- Runtime: Bubble Tea (`charm.land/bubbletea/v2`, `bubbles/v2`, `lipgloss/v2`,
  `glamour/v2`) and `huh/v2` for the interactive chat TUI and commands,
  `atotto/clipboard` for paste, `charmbracelet/x/ansi` for terminal styling, and
  `golang.org/x/sync` (errgroup) plus `mvdan.cc/sh/v3` (shell parsing for the
  bash tool) in the engine.
- Everything else comes from the Go standard library.
- Vendor nothing — all deps are managed via `go.mod`.

## Security-sensitive code

Permission gating, the sandbox (`engine/sandbox.go`), the bash denylist
(`engine/tools_bash.go`) and config handling all **fail closed**: when
unsure, they deny. A change that makes any of them more permissive must come with
tests proving the previous behaviour was wrong — otherwise treat it as a security
change and open an issue before writing code.

## Commit messages

[Conventional Commits](https://www.conventionalcommits.org/) —
`type(scope): description`, lowercase, imperative:

```
fix(engine): reject recursive rm on protected paths
feat(chat): add /export slash command
docs(reference): document exit codes
```

Types used here: `feat`, `fix`, `docs`, `refactor`, `perf`, `test`, `build`,
`ci`, `chore`. The scope is the package or area (`engine`, `chat`, `mcp`,
`task`, `docs`, `release`). No ticket numbers — this is an open-source project.

Conventional Commits are not just style: they are what drives the release.
`feat` and `fix` (and breaking changes) end up in the changelog and bump the
version; `chore`/`ci`/`test` do not.

## Releases

There is **no version to edit anywhere** — the git tag is the version:

1. Push to `main` with Conventional Commits.
2. [`release-please`](https://github.com/googleapis/release-please) opens a
   *release PR* with the bump, the new `CHANGELOG.md` section and the fallback
   in `internal/version/version.go`.
3. Merging that PR creates the tag `vX.Y.Z` and the GitHub Release.
4. The same workflow cross-compiles `make dist` and attaches the archives.

The workflow lives in `.github/workflows/release.yml`; config in
`release-please-config.json` + `.release-please-manifest.json`. The manifest
starts at `0.0.0`, which release-please treats as *no release yet* — so the
first release takes the tool's default (`1.0.0`); every release after that is
a SemVer bump of the previous tag.

## Documentation

Docs are docs-as-code: plain Markdown under `docs/`, organized by reader intent
(tutorial / how-to / reference / explanation — see [Index](../index.md)).
`docs/_style-guide.md` holds the formatting rules and the skeleton for a new
page. A relative link or anchor broken by a move fails `go test ./...` via the
link check in `internal/doccheck`. Absolute `http(s)://` links (including the
README badges) are checked over the network by the same package, in a separate
CI job: `DOCCHECK_EXTERNAL=1 go test ./internal/doccheck -run
TestExternalLinksReachable`.

## License

By contributing you agree that your work is released under the
[MIT License](../../LICENSE) — © 2026 discorgento.

---

🔮 ouija docs — [Index](../index.md) · [Architecture](../explanation/architecture.md)
