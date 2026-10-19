package version

import (
	"regexp"
	"testing"
)

// semver guards the default injected into plain `go build` / `go run` output.
// An empty or ad-hoc default would silently break `ouija --version`, the MCP
// handshake and the TUI header (they all read this var).
var semver = regexp.MustCompile(`^\d+\.\d+\.\d+`)

func TestVersion_DefaultLooksLikeSemver(t *testing.T) {
	if !semver.MatchString(Version) {
		t.Fatalf("Version = %q, want a semver-looking default (e.g. 1.0.0)", Version)
	}
}
