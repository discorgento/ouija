package chat

import (
	"os"
	"runtime"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
)

// A conversation saved after a reply comes back whole: history, transcript,
// counters; later turns keep saving into it.
func TestSessionRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cfg := &engine.Config{ProjectRoot: "/proj", Model: "m", Provider: "p"}
	m := newModel(&switchEngine{cfg: cfg}, nil, "", "")
	if err := m.saveSession(); err != nil {
		t.Fatal(err)
	}
	if list, _ := listSessions("/proj"); len(list) != 0 {
		t.Fatal("an empty conversation was saved")
	}
	m.msgCount = 1
	m.history = []engine.Message{{Role: engine.RoleUser, Content: "what is ouija?"}, {Role: engine.RoleAssistant, Content: "a harness"}}
	m.lines = []line{{role: "user", text: "what is ouija?", ts: "10:00"}, {role: "assistant", text: "a harness", ts: "10:00", meta: "1.0s",
		tools: engine.TurnSummary{Tools: []engine.TurnTool{{Name: "read_file", Target: "README.md", Duration: 5}}}}}
	if err := m.saveSession(); err != nil {
		t.Fatal(err)
	}
	dir, _ := sessionDir("/proj")
	fi, err := os.Stat(dir + "/" + m.sessionID + ".json")
	if err != nil || (runtime.GOOS != "windows" && fi.Mode().Perm() != 0o600) { // no Unix modes on Windows
		t.Fatalf("session file: %v, mode %v (want 0600)", err, fi.Mode().Perm())
	}

	m2 := newModel(&switchEngine{cfg: cfg}, nil, "", "")
	m2.continueLatest()
	if m2.msgCount != 1 || len(m2.history) != 2 || m2.sessionID != m.sessionID {
		t.Fatalf("restored: msgs=%d history=%d id=%q", m2.msgCount, len(m2.history), m2.sessionID)
	}
	if m2.lines[1].tools.Tools[0].Target != "README.md" || !strings.Contains(m2.lines[2].text, "resumed") {
		t.Fatalf("lines: %+v", m2.lines)
	}
	if other, _ := listSessions("/elsewhere"); len(other) != 0 {
		t.Fatal("another project sees this one's sessions")
	}
	if !m2.showSessionsPicker() || !strings.Contains(m2.picker.Items[0], "what is ouija?") {
		t.Fatalf("picker items: %v", m2.picker.Items)
	}
}

func TestContinueWithoutSessions(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	m := newModel(&switchEngine{cfg: &engine.Config{ProjectRoot: "/none"}}, nil, "", "")
	m.continueLatest()
	if len(m.lines) != 1 || !strings.Contains(m.lines[0].text, "no earlier conversation") {
		t.Fatalf("lines: %+v", m.lines)
	}
}
