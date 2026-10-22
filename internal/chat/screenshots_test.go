package chat

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/version"
)

// screens writes the README visuals' scenes, with their colors, as ANSI files:
// make screens turns them into PNGs and GIFs (internal/tools/screenshots).
//
//	go test ./internal/chat -run Screenshots -screens=/abs/dir
//
// A still is <dir>/<name>.ans; an animation is <dir>/<name>/<NNNN>_<ms>.ans,
// one file per frame, shown for ms milliseconds, in name order.
var screens = flag.String("screens", "", "write the screenshot scenes as .ans files to this directory")

// Every visual is one terminal size, so they all come out the same size.
const screenW, screenH = 100, 30

// budgetDiff is the edit the conversation scene shows: output tokens now count
// toward the budget.
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

// writeScene writes one still or one animation frame.
func writeScene(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestScreenshots writes the stills of the README: the welcome, a reply with
// its tools and diff, and /task at work.
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
					text: "`Add` charges only **input** tokens, so a long reply can overspend. I'll count both and cover it with a test.",
					tools: engine.TurnSummary{Tools: []engine.TurnTool{
						{Name: "read_file", Target: "engine/budget.go", Duration: 3 * time.Millisecond},
						{Name: "grep", Target: "OutputTokens", Duration: 12 * time.Millisecond},
						{Name: "bash", Target: "go test ./engine/ -run Budget", Duration: 2400 * time.Millisecond, Error: "exit status 1",
							Output: "--- FAIL: TestBudgetCountsOutput (0.00s)\n    budget_test.go:41: tokens = 1200, want 1500\nFAIL\texample.com/app/engine"},
					}}},
				{role: "diff", ts: "14:02", path: "engine/budget.go", text: budgetDiff},
			}
		},
		"chat-task": func(m *model) {
			now := time.Now()
			m.msgCount, m.tokens, m.steps, m.usd = 2, 48_200, 31, 0.21
			m.lines = []line{
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
	for name, setup := range scenes {
		t.Run(name, func(t *testing.T) {
			m := screenModel(t)
			setup(m)
			m.setViewportSize()
			if m.totem != nil {
				m.rebuildStreaming()
			}
			writeScene(t, filepath.Join(*screens, name+".ans"), m.View().Content)
		})
	}
}

// ─── Animations ────────────────────────────────────────────────────────

// demoEngine is the chat's engine for the demo: a real tracer, so the live
// tools line and the reply's tools come from the spans as in a real turn.
type demoEngine struct {
	*switchEngine
	tr            *engine.Tracer
	tokens, steps int
	usd           float64
	turn          *engine.Span
	running       *engine.Span
}

func (e *demoEngine) Tracer() *engine.Tracer                     { return e.tr }
func (e *demoEngine) Spent() (int, int, float64)                 { return e.tokens, e.steps, e.usd }
func (e *demoEngine) Config() *engine.Config                     { return e.switchEngine.Config() }
func (e *demoEngine) PermissionConfig() *engine.PermissionConfig { return nil }

// startTool starts a tool call's span as the engine's tool runner does.
func (e *demoEngine) startTool(name, attr, target string) {
	s := e.turn.StartChild("tool." + name)
	s.SetAttr("tool", name)
	s.SetAttr(attr, target)
	e.running = s
}

// endTool ends the running call after d, with its output's tail and error.
func (e *demoEngine) endTool(d time.Duration, output, errText string) {
	s := e.running
	s.Start = time.Now().Add(-d)
	if output != "" {
		s.SetAttr("output", output)
	}
	if errText != "" {
		s.SetError(errText)
	}
	s.End()
	e.running = nil
}

// animation writes the frames of one animation: <dir>/<name>/<NNNN>_<ms>.ans.
type animation struct {
	t    *testing.T
	dir  string
	next int
}

// frame writes the screen, shown for ms milliseconds.
func (a *animation) frame(content string, ms int) {
	a.t.Helper()
	writeScene(a.t, filepath.Join(a.dir, fmt.Sprintf("%04d_%d.ans", a.next, ms)), content)
	a.next++
}

// demoQuestion is what the demo types.
const demoQuestion = "Avg panics on an empty slice, fix it @stats.go"

// demoDiff is the fix the demo approves, as the edit tool previews it.
const demoDiff = `  @@ -3,7 +3,10 @@
  // Avg returns the mean of xs.
  func Avg(xs []int) int {
  +	if len(xs) == 0 {
  +		return 0
  +	}
  	sum := 0
  	for _, x := range xs {
  		sum += x`

// demoReply is the reply as it streams: first why, then (after the fix and
// the passing test) what was done.
var demoReply = []string{
	"`Avg` divides by `len(xs)`",
	"`Avg` divides by `len(xs)` without checking it, so an empty slice",
	"`Avg` divides by `len(xs)` without checking it, so an empty slice divides by zero. " +
		"I'll return **0** for it.",
}

const demoDone = "\n\nFixed: an empty slice now averages to 0, and `go test ./...` passes."

// TestDemo writes assets/demo.gif's frames: the welcome, a question typed,
// the tools at work and a failing test, the reply streaming, the fix shown for
// approval, approved, the test passing, and the reply with its diff.
func TestDemo(t *testing.T) {
	if *screens == "" {
		t.Skip("set -screens=<dir> to write the screenshot scenes")
	}
	m := screenModel(t)
	eng := &demoEngine{switchEngine: m.eng.(*switchEngine), tr: engine.NewTracer(true)}
	m.eng = eng
	cfg := eng.Config()
	cfg.ProjectRoot = "/home/user/code/stats"
	m.welcomeKey = ""
	m.setViewportSize()

	a := &animation{t: t, dir: filepath.Join(*screens, "demo")}
	// The clock the screen shows: the session's age and the reply's.
	session := 4*time.Minute + 12*time.Second
	var sent time.Duration
	var at time.Duration
	frames := []string{"⣾", "⣽", "⣻", "⢿", "⡿", "⣟", "⣯", "⣷"}
	spin := 0
	shoot := func(ms int) {
		now := time.Now()
		// Half a second into each second: the counters never flip between runs.
		m.startTime = now.Add(-(session + at + 500*time.Millisecond))
		if m.waiting {
			m.responseStart = now.Add(-(at - sent) - 40*time.Millisecond)
			m.spinner.Spinner.Frames = append(append([]string(nil), frames[spin%len(frames):]...), frames[:spin%len(frames)]...)
			spin++
			m.setViewportSize()
		}
		a.frame(fixClock(m.View().Content, "14:02"), ms)
		at += time.Duration(ms) * time.Millisecond
	}

	// The welcome, then the question typed a character at a time.
	m.setViewportSize()
	shoot(1600)
	for i := 1; i <= len(demoQuestion); i++ {
		m.input.SetValue(demoQuestion[:i])
		m.setViewportSize()
		ms := 55
		if demoQuestion[i-1] == ' ' {
			ms = 90
		}
		shoot(ms)
	}
	shoot(600)

	// Sent: as beginGeneration does it.
	m.input.Reset()
	m.lines = append(m.lines, line{role: "user", text: demoQuestion, ts: "14:02", meta: "stats.go"})
	m.msgCount = 1
	m.waiting, sent = true, at
	eng.turn = eng.tr.Start("chat")
	m.eng.Tracer().NextTurn()
	eng.turn = eng.tr.Start("turn")
	for range 3 {
		shoot(160)
	}

	// The tools: the file read, then the test run, which fails.
	tool := func(name, attr, target string) {
		eng.startTool(name, attr, target)
		m.currentTool = name + ": " + target
		if m.partialText != "" && !strings.HasSuffix(m.partialText, "\n") {
			m.partialText += "\n\n"
		}
	}
	tool("read_file", "path", "stats.go")
	shoot(450)
	eng.endTool(2*time.Millisecond, "", "")
	tool("bash", "command", "go test ./...")
	for range 4 {
		shoot(250)
	}
	eng.endTool(1400*time.Millisecond, "--- FAIL: TestAvgEmpty (0.00s)\npanic: runtime error: integer divide by zero [recovered]\nFAIL\texample.com/stats\t0.004s", "exit status 1")
	m.currentTool = ""
	shoot(400)

	// The reply streams in.
	for _, text := range demoReply {
		m.partialText = text
		m.streamingTokens = len(m.partialText) / 4
		shoot(380)
	}
	shoot(500)

	// The fix, shown for approval.
	tool("edit_file", "path", "stats.go")
	m.showApproval(approvalRequestMsg{tool: "edit_file\x00stats.go", path: "stats.go", input: map[string]any{
		"path":      "stats.go",
		"oldString": "\tsum := 0",
		"newString": "\tif len(xs) == 0 {\n\t\treturn 0\n\t}\n\tsum := 0",
		"_preview":  demoDiff,
	}})
	shoot(3200)

	// Approved: the edit runs, then the test again, which passes.
	m.approvalReq = nil
	m.nextApproval()
	eng.endTool(3*time.Millisecond, "", "")
	tool("bash", "command", "go test ./...")
	for range 4 {
		shoot(250)
	}
	eng.endTool(1100*time.Millisecond, "ok  \texample.com/stats\t0.003s", "")
	m.currentTool = ""
	m.partialText += demoDone[:len(demoDone)/2]
	m.streamingTokens = len(m.partialText) / 4
	shoot(300)
	m.partialText = strings.TrimSuffix(m.partialText, demoDone[:len(demoDone)/2]) + demoDone
	m.streamingTokens = len(m.partialText) / 4
	shoot(300)

	// The reply settles, with its tools and the diff (handleResponse).
	eng.turn.SetAttr("input_tokens", "8400")
	eng.turn.SetAttr("output_tokens", "236")
	eng.turn.End()
	eng.tokens, eng.steps, eng.usd = 8636, 5, 0.02
	text := m.partialText
	m.waiting, m.partialText, m.streamingTokens = false, "", 0
	m.tokens, m.steps, m.usd = eng.Spent()
	m.lines = append(m.lines,
		line{role: "assistant", text: text, ts: "14:02", meta: "6.4s · 8.4k→236 tok · $0.02", tools: eng.tr.LastTurn()},
		line{role: "diff", ts: "14:02", path: "stats.go", text: demoDiff})
	m.setViewportSize()
	shoot(5000)
}

// fixClock puts clock in place of the wall-clock time a streaming reply's
// header shows, so every run draws the same frames.
func fixClock(screen, clock string) string {
	now := time.Now()
	for _, t := range []time.Time{now, now.Add(-time.Minute)} {
		screen = strings.ReplaceAll(screen, " "+t.Format("15:04")+" ", " "+clock+" ")
	}
	return screen
}

// TestInstallChat writes the last frame of assets/install.gif: `ouija` opening
// the chat once the setup picked OpenRouter's free router. The frames before it
// come from internal/commands (TestInstallFrames); this one sorts last.
func TestInstallChat(t *testing.T) {
	if *screens == "" {
		t.Skip("set -screens=<dir> to write the screenshot scenes")
	}
	m := screenModel(t)
	cfg := m.eng.Config()
	cfg.Provider, cfg.Model, cfg.ContextWindow = "openrouter", "openrouter/free", 0
	cfg.ProjectRoot = "/home/user/code/stats"
	cfg.Budget.MaxTokens = 0
	m.welcome.facts, m.welcomeKey = 0, ""
	m.startTime = time.Now().Add(-500 * time.Millisecond)
	m.setViewportSize()
	a := &animation{t: t, dir: filepath.Join(*screens, "install"), next: 9000}
	a.frame(m.View().Content, 4500)
}
