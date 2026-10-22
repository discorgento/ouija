# Contributing to ouija

Thanks for helping. This file is the short version. The full developer guide,
covering workflow, code conventions, error handling, logging, naming and
testing, lives in **[`docs/how-to/contribute.md`](docs/how-to/contribute.md)**.

## Requirements

- **Go 1.26+** — the `go` directive in `go.mod` is the source of truth
- **`make`** — optional; every command has a raw `go` equivalent

## The one rule: `make check` must pass

```sh
make check    # gofmt -l . + go vet ./... + go test ./...
```

CI runs these checks and more on every push and pull request, and a PR with a
failing check cannot merge. `.github/workflows/ci.yml` runs gofmt, checks that
`go.mod` is tidy and generated code is current, runs vet for
linux/darwin/windows, staticcheck, actionlint, `go test -race ./...`, the build
and govulncheck. You can run each extra check locally as a `make` target:
`make tidy-check gen-check cross-vet lint actionlint vuln`.

While iterating:

```sh
go test ./engine -run TestBashDenylist -v
make test-race          # before touching concurrency (chat, engine)
```

## Before you open a PR

- Read the right page first: [where to make changes](docs/how-to/contribute.md#where-to-make-changes).
- **Security-sensitive code** (permissions, the file and bash sandboxes, the
  git restore, the bash denylist, project trust, config handling, the provider
  client, the MCP server) **fails closed**: when in doubt, it denies. A change that makes any
  of it more permissive must include tests proving the previous behaviour was
  wrong. If you cannot prove that, open an issue before writing code. See
  [SECURITY.md](SECURITY.md).
- Commits follow [Conventional Commits](https://www.conventionalcommits.org/)
  (`fix(engine): …`), without ticket numbers. The commit types also drive the
  automated release: a `feat` or `fix` commit bumps the version and adds a
  changelog entry.
- Docs changes must keep every relative link and anchor working;
  `go test ./...` checks them (`internal/doccheck`), and fails too when a
  config key, a command or an `OUIJA_*` variable is missing from
  `docs/reference/`. A separate CI job, which needs the network, checks
  absolute links.
- **Never edit a version by hand**: the git tag is the version, created by the
  [release pipeline](.github/workflows/release.yml) — see
  [Releases](docs/how-to/contribute.md#releases).

## Contributing a totem

A totem is an AI agent written as Go code. You can contribute one in two ways:

- **Upstream**, so it ships in the `ouija` binary and appears in `ouija help`
  and MCP for everyone: run `ouija generate totem` in this repo, implement the
  `Run` method, and open a PR. See [Add a totem](docs/how-to/add-totem.md).
- **External**, in your own repo, binary and MCP namespace: import the public
  `bootstrap`, `engine` and `registry` packages and build your own CLI. Your
  totem inherits the engine, budget, sandbox, trace and MCP export without a
  fork.

The engine lives in `engine/`, not `internal/`, because it is designed to be
imported.

## License

By contributing you agree that your work is released under the
[MIT License](LICENSE) — © 2026 discorgento.
