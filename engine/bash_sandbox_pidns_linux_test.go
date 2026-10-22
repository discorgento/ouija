//go:build linux

package engine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Where user namespaces are forbidden (Ubuntu's AppArmor policy, many
// containers) the sandbox still runs, and the process group still ends the
// command's background jobs.
func TestSandboxWithoutUserNamespaces(t *testing.T) {
	saved := pidNamespaceAvailable
	pidNamespaceAvailable = func() bool { return false }
	t.Cleanup(func() { pidNamespaceAvailable = saved })

	project, _, run := sandboxFixture(t, DefaultSandbox())
	if out := fmtData(run(`echo inside > made; cat made`)); !strings.Contains(out, "inside") {
		t.Fatalf("the sandbox did not run without user namespaces: %s", out)
	}
	run(`(sleep 1; touch late) >/dev/null 2>&1 & echo started`)
	time.Sleep(2500 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(project, "late")); err == nil {
		t.Error("without user namespaces, a background job outlived its command")
	}
}
