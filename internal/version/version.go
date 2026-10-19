// Package version holds ouija's version string.
//
// Version is a var, not a const, so release builds can inject it at link time:
//
//	make build VERSION=v0.0.1     # the Makefile strips the "v" → 0.0.1
//
// Resolution order:
//
//  1. The git tag (`git describe --tags`), injected by the Makefile (local
//     builds) and by .github/workflows/release.yml (release builds). The tag
//     is created automatically by release-please — never edit a version by
//     hand.
//  2. The fallback below, used by plain `go build` / `go run` where no tag is
//     available. release-please keeps it equal to the latest release through
//     the x-release-please-version annotation.
//
// The value is what both binaries print, and what the MCP `serverInfo` and
// the TUI header show.
package version

// Version is the ouija release version. Overridable with `-ldflags -X`.
var Version = "0.0.0" // x-release-please-version
