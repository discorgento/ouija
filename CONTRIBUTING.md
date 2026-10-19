# Contributing to ouija

Thanks for helping. The full developer guide — workflow, code conventions,
error handling, logging, naming, testing — lives in
**[`docs/how-to/contribute.md`](docs/how-to/contribute.md)**. This file is the
short version.

## Requirements

- **Go 1.26+** — the `go` directive in `go.mod` is the source of truth
- **`make`** — optional; every command has a raw `go` equivalent

## The one rule: `make check` must pass

```sh
make check    # gofmt -l . + go vet ./... + go test ./...
```

CI runs the same gate on every push and pull request
(`.github/workflows/ci.yml`: gofmt, vet, `go test -race ./...`, build), so a red
PR cannot merge.

While iterating:

```sh
go test ./engine -run TestBashDenylist -v
make test-race          # before touching concurrency (chat, engine)
```

## Before you open a PR

- Read the right page first: [where to make changes](docs/how-to/contribute.md#where-to-make-changes).
- **Security-sensitive code** (permissions, sandbox, bash denylist, config
  handling) **fails closed**. A change that makes any of them more permissive
  must come with tests proving the previous behaviour was wrong — otherwise open
  an issue before writing code. See [SECURITY.md](SECURITY.md).
- Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
  (`fix(engine): …`) — no ticket numbers, this is an open-source project. They
  also drive the automated release (`feat`/`fix` → version bump + changelog).
- Docs changes must keep every relative link and anchor working —
  `go test ./...` checks them (`internal/doccheck`). Absolute links are checked
  by a separate, network-bound CI job.
- **Never edit a version by hand**: the git tag is the version, created by the
  [release pipeline](.github/workflows/release.yml) — see
  [Releases](docs/how-to/contribute.md#releases).

## Contributing a totem

A totem is an agent workflow in Go. You can contribute one two ways:

- **Upstream** (ships in the `ouija` binary, appears in `ouija help` and MCP for
  everyone): `ouija generate totem` in this repo, implement the `Run` method,
  open a PR. See [Add a totem](docs/how-to/add-totem.md).
- **External** (your own repo, your own binary, your own MCP namespace): import
  the public `bootstrap`, `engine`, `registry` packages, build your own CLI.
  The engine, budget, sandbox, trace and MCP export are all inherited — no fork
  needed.

The engine is in `engine/`, not `internal/` — it is designed to be imported.

## License

By contributing you agree that your work is released under the
[MIT License](LICENSE) — © 2026 discorgento.
