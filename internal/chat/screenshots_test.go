package chat

import (
	"flag"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/version"
)

// screens writes the README screenshots' scenes, with their colors, as ANSI
// files: make screens turns them into PNGs (internal/tools/screenshots).
//
//	go test ./internal/chat -run Screenshots -screens=/abs/dir
var screens = flag.String("screens", "", "write the screenshot scenes as .ans files to this directory")

// Every screenshot is one terminal size: a diff goes side by side from 140
// columns of card, the 142 of the terminal.
const screenW, screenH = 142, 37

// budgetDiff is the edit the conversation and diff scenes show: output tokens
// now count toward the budget.
const budgetDiff = `  @@ -38,8 +38,9 @@
  // Add charges a call's usage to the budget.
  func (b *Budget) Add(u Usage) error {
  -	b.tokens += u.InputTokens
  +	b.tokens += u.InputTokens + u.OutputTokens
  +	b.calls++
  	if b.max > 0 && b.tokens > b.max {
  		return ErrBudgetExceeded
  	}
  	return nil
  }`

// cacheDiff is the diff scene's edit: the model list fetched once per TTL.
const cacheDiff = `  @@ -12,8 +12,24 @@
  // ListModels returns the models the provider serves.
  -func ListModels(ctx context.Context) ([]Model, error) {
  -	return provider.Models(ctx)
  -}
  +func (c *cache) List(ctx context.Context) ([]Model, error) {
  +	c.mu.Lock()
  +	defer c.mu.Unlock()
  +	if c.models != nil && time.Since(c.at) < c.ttl {
  +		return c.models, nil // fresh: no request
  +	}
  +	models, err := c.provider.Models(ctx)
  +	if err != nil {
  +		return nil, err
  +	}
  +	c.models, c.at = models, time.Now()
  +	return models, nil
  +}
  
  // Model is one model a provider serves.
  type Model struct {
  	ID      string
  	Context int
  }`

// screenModel is goldenModel as a user sees it: the released version, a
// project under the home directory, a budget and a session minutes old.
func screenModel(t *testing.T) *model {
	t.Helper()
	released := version.Version
	t.Setenv("HOME", "/home/user")
	m := goldenModel(t, screenW, screenH)
	version.Version = released // goldenModel's cleanup puts it back as well
	cfg := m.eng.Config()
	cfg.ProjectRoot = "/home/user/code/ouija"
	cfg.Budget.MaxTokens = 500_000
	m.welcome.facts, m.welcomeKey = 4, ""
	m.startTime = time.Now().Add(-4*time.Minute - 12*time.Second)
	m.setViewportSize()
	return m
}

// TestScreenshots writes the four scenes of the README: the welcome, a reply
// with its tools and diff, a diff side by side and /task at work.
func TestScreenshots(t *testing.T) {
	if *screens == "" {
		t.Skip("set -screens=<dir> to write the screenshot scenes")
	}
	scenes := map[string]func(m *model){
		"chat-welcome": func(*model) {},
		"chat-conversation": func(m *model) {
			m.msgCount, m.tokens, m.steps, m.usd = 2, 18_400, 6, 0.07
			m.lines = []line{
				{role: "user", text: "why does the budget ignore output tokens? @engine/budget.go", ts: "14:02", meta: "engine/budget.go"},
				{role: "assistant", ts: "14:02", meta: "6.1s · 9.8k→412 tok · $0.04",
					text: "`Add` charges only **input** tokens:\n\n```go\nb.tokens += u.InputTokens\n```\n\n" +
						"Output tokens never reach the ceiling, so a long reply can overspend. I'll count both and cover it with a test.",
					tools: engine.TurnSummary{Tools: []engine.TurnTool{
						{Name: "read_file", Target: "engine/budget.go", Duration: 3 * time.Millisecond},
						{Name: "grep", Target: "OutputTokens", Duration: 12 * time.Millisecond},
						{Name: "bash", Target: "go test ./engine/ -run Budget", Duration: 2400 * time.Millisecond, Error: "exit status 1",
							Output: "--- FAIL: TestBudgetCountsOutput (0.00s)\n    budget_test.go:41: tokens = 1200, want 1500\nFAIL\tgithub.com/acme/app/engine"},
					}}},
				{role: "diff", ts: "14:02", path: "engine/budget.go", text: budgetDiff},
			}
		},
		"chat-diff": func(m *model) {
			m.msgCount, m.tokens, m.steps, m.usd = 3, 24_100, 9, 0.09
			m.lines = []line{
				{role: "user", text: "cache the model list for a minute", ts: "14:05"},
				{role: "assistant", ts: "14:05", meta: "4.3s · 11.2k→286 tok · $0.02",
					text: "Done: `List` asks the provider once per TTL and serves the cached list in between."},
				{role: "diff", ts: "14:05", path: "engine/models.go", text: cacheDiff},
			}
		},
		"chat-task": func(m *model) {
			now := time.Now()
			m.msgCount, m.tokens, m.steps, m.usd = 2, 48_200, 31, 0.21
			m.lines = []line{
				{role: "user", text: "where is the model list fetched?", ts: "15:08"},
				{role: "assistant", ts: "15:08", meta: "3.2s · 6.4k→188 tok · $0.01",
					text: "In `engine/models.go`: `ListModels` asks the provider on every call, and `/model` calls it each time the picker opens.",
					tools: engine.TurnSummary{Tools: []engine.TurnTool{
						{Name: "grep", Target: "ListModels", Duration: 9 * time.Millisecond},
						{Name: "read_file", Target: "engine/models.go", Duration: 2 * time.Millisecond},
					}}},
				{role: "user", text: "/task add a cache to the model list fetch", ts: "15:10"},
			}
			m.waiting = true
			m.responseStart = now.Add(-48 * time.Second)
			// Real labels: the scout, then pass × dimension (totems/task).
			ago := func(d time.Duration) time.Time { return now.Add(-d) }
			done := func(label string, d time.Duration) *totemStep {
				return &totemStep{label: label, done: true, dur: d}
			}
			running := func(label string, d time.Duration) *totemStep {
				return &totemStep{label: label, start: ago(d)}
			}
			m.totem = &totemRun{name: "task", start: ago(48 * time.Second), runs: []*totemStep{
				done("scout", 9100*time.Millisecond),
				done("p1:correctness", 21*time.Second),
				done("p1:best-practices", 18*time.Second),
				done("p1:security", 24*time.Second),
				done("p1:performance", 19*time.Second),
				done("p2:correctness", 26*time.Second),
				running("p2:best-practices", 38*time.Second),
				running("p2:security", 38*time.Second),
				done("p2:performance", 22*time.Second),
				running("p3:correctness", 37*time.Second),
				running("p3:best-practices", 37*time.Second),
				running("p3:security", 31*time.Second),
				running("p3:performance", 29*time.Second),
			}}
		},
	}
	if err := os.MkdirAll(*screens, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, setup := range scenes {
		t.Run(name, func(t *testing.T) {
			m := screenModel(t)
			setup(m)
			m.setViewportSize()
			if m.totem != nil {
				m.rebuildStreaming()
			}
			if err := os.WriteFile(filepath.Join(*screens, name+".ans"), []byte(m.View().Content), 0o644); err != nil {
				t.Fatal(err)
			}
		})
	}
}
