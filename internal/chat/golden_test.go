package chat

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/version"
)

// update rewrites the golden screens: go test ./internal/chat -run Golden -update
var update = flag.Bool("update", false, "rewrite the golden screens in testdata/screens")

// goldenModel is a chat at a fixed size, clock and animation frame, so its
// screens are the same on every run.
func goldenModel(t *testing.T, w, h int) *model {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	// release-please bumps the version in the source: the screens keep one.
	saved := version.Version
	version.Version = "0.0.0"
	t.Cleanup(func() { version.Version = saved })
	cfg := &engine.Config{Provider: "anthropic", Model: "claude-sonnet-5-5", ProjectRoot: "/proj", ContextWindow: 200_000}
	m := newModel(&switchEngine{cfg: cfg}, nil, "", "")
	m.welcome = welcomeInfo{branch: "main", facts: 2}
	m.tickCount = introTicks
	m.hueShift = 0
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m.startTime = time.Now()
	return m
}

// checkGolden compares the screen, without its colors, with
// testdata/screens/name.txt.
func checkGolden(t *testing.T, name string, m *model) {
	t.Helper()
	var b strings.Builder
	for _, l := range strings.Split(ansi.Strip(m.View().Content), "\n") {
		b.WriteString(strings.TrimRight(l, " ") + "\n")
	}
	got := b.String()
	path := filepath.Join("testdata", "screens", name+".txt")
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update to create it)", err)
	}
	// A Windows checkout may have turned the file's line ends into CRLF.
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Errorf("screen %s changed (run with -update if on purpose):\n--- want\n%s\n--- got\n%s", name, want, got)
	}
}

// The main screens keep their layout: a change shows here before a user sees
// it.
func TestGoldenScreens(t *testing.T) {
	t.Run("welcome", func(t *testing.T) {
		m := goldenModel(t, 100, 36)
		checkGolden(t, "welcome", m)
	})

	t.Run("conversation", func(t *testing.T) {
		m := goldenModel(t, 100, 40)
		m.msgCount, m.tokens, m.steps, m.usd = 1, 7600, 4, 0.03
		m.lines = []line{
			{role: "user", text: "explain the budget @engine/budget.go", ts: "18:27", meta: "engine/budget.go"},
			{role: "assistant", ts: "18:27", meta: "7.8s · 7.1k→518 tok · $0.03", text: "The budget caps **tokens** and `USD`.\n\n- MaxTokens\n- MaxUSD",
				tools: engine.TurnSummary{Tools: []engine.TurnTool{
					{Name: "read_file", Target: "engine/budget.go", Duration: 2 * time.Millisecond},
					{Name: "bash", Target: "go test ./engine/...", Duration: 2100 * time.Millisecond, Error: "exit status 1", Output: "--- FAIL: TestBudget\nFAIL\tengine"},
				}}},
			{role: "diff", ts: "18:27", path: "engine/budget.go", text: "  @@ -10,3 +10,3 @@\n  func f() {\n  -\treturn old\n  +\treturn new"},
			{role: "error", ts: "18:28", meta: "the API key was refused", text: "401 unauthorized", hint: "run `ouija setup`"},
		}
		m.rebuildContent()
		checkGolden(t, "conversation", m)
	})

	t.Run("approval", func(t *testing.T) {
		m := goldenModel(t, 100, 30)
		cmd := "go test ./... && git status -sb && find . -path ./.git -prune -o -type f -print | head -80"
		m.showApproval(approvalRequestMsg{tool: "bash\x00x", input: map[string]any{"command": cmd, "_preview": "will run: " + cmd + "  [go, git, find, head]"}})
		checkGolden(t, "approval", m)
	})

	t.Run("help", func(t *testing.T) {
		m := goldenModel(t, 140, 40)
		m.msgCount = 1
		m.lines = []line{{role: "help"}}
		m.rebuildContent()
		checkGolden(t, "help", m)
	})
}
