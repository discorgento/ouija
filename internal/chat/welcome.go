package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/knowledge"
	"github.com/discorgento/ouija/internal/theme"
)

// ─── Welcome screen ───────────────────────────────────────────────────

// welcomeInfo is what the welcome screen shows beyond the config, read once
// when the chat starts.
type welcomeInfo struct {
	branch string // git branch of the project ("" outside a repo)
	facts  int    // project facts saved with `ouija learn`
}

// loadWelcomeInfo reads the git branch and the knowledge facts of root.
func loadWelcomeInfo(root string) welcomeInfo {
	if root == "" {
		return welcomeInfo{}
	}
	info := welcomeInfo{branch: gitBranch(root)}
	for _, l := range strings.Split(knowledge.Load(root), "\n") {
		if strings.HasPrefix(l, "- ") {
			info.facts++
		}
	}
	return info
}

// gitBranch returns the branch checked out in root, the short commit on a
// detached HEAD, or "" when root is not a git checkout (a worktree's .git is a
// file: no branch shown rather than a git subprocess on startup).
func gitBranch(root string) string {
	b, err := os.ReadFile(filepath.Join(root, ".git", "HEAD"))
	if err != nil {
		return ""
	}
	head := strings.TrimSpace(string(b))
	if ref, ok := strings.CutPrefix(head, "ref: refs/heads/"); ok {
		return ref
	}
	if len(head) >= 7 {
		return head[:7]
	}
	return ""
}

// displayPath shortens a path under the home directory to ~/….
func displayPath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" && strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

var sparkleFrames = []string{"◆", "◇", "✦", "✧", "❖", "◈", "✧", "✦", "◇"}

var (
	panelBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(theme.Muted).
			Padding(0, 1)
	panelTitle = lipgloss.NewStyle().Bold(true).Foreground(theme.Lavender)
	panelKey   = lipgloss.NewStyle().Foreground(theme.Muted)
	panelVal   = lipgloss.NewStyle().Foreground(theme.Text)
	panelHint  = lipgloss.NewStyle().Italic(true).Foreground(theme.Mauve)
	panelCmd   = lipgloss.NewStyle().Bold(true).Foreground(theme.Pink)
)

// renderWelcomeBanner builds the welcome screen at the given content width:
// the logo in a flowing gradient (shift moves it), a tagline, and two panels —
// the session (project, model, context, budget, memory) and where to start.
//
// reveal is the intro: while it is ≥ 0, only the logo's first reveal columns
// show, a planchette (◉) moves under them, and the letters it passed are
// spelled out — the board spelling its name. Below 0 the logo is whole.
func renderWelcomeBanner(shift, width, reveal int, info welcomeInfo) string {
	center := func(s string) string { return lipgloss.PlaceHorizontal(width, lipgloss.Center, s) }

	var b strings.Builder
	b.WriteString(center(renderLogo(shift, width, reveal)) + "\n")

	if w := logoWidth(); reveal >= 0 && width >= w+2 {
		col := min(reveal, w-1)
		planchette := strings.Repeat(" ", col) + lipgloss.NewStyle().Bold(true).Foreground(theme.Pink).Render("◉") + strings.Repeat(" ", w-1-col)
		spelled := "OUIJA"[:min(5, col*5/w+1)]
		b.WriteString(center(planchette) + "\n")
		b.WriteString(center(theme.GradientText(strings.Join(strings.Split(spelled, ""), " · "), shift)) + "\n")
	} else {
		spark := lipgloss.NewStyle().Bold(true).Foreground(theme.Rainbow[shift%len(theme.Rainbow)]).
			Render(sparkleFrames[shift%len(sparkleFrames)])
		tagline := lipgloss.NewStyle().Italic(true).Foreground(theme.Lavender).Render("the board is listening")
		b.WriteString(center(spark+"  "+tagline+"  "+spark) + "\n\n")
	}
	b.WriteString(panelHint.Width(width).Align(lipgloss.Center).
		Render("ask about this project — ouija edits only with your approval") + "\n\n")
	b.WriteString(center(startGrid(width)) + "\n")
	if line := totemsLine(width); line != "" {
		b.WriteString("\n" + center(line) + "\n")
	}
	if note := sessionNote(info); note != "" {
		b.WriteString("\n" + center(note))
	}
	return b.String()
}

// renderLogo paints the figlet logo with a diagonal gradient sheen, or just the
// name when the screen is narrower than the logo.
func renderLogo(shift, width, reveal int) string {
	logoW := logoWidth()
	if width < logoW+2 {
		return theme.GradientText("o u i j a", shift)
	}
	// Each row starts two steps further along the gradient: a diagonal sheen.
	colors := theme.Gradient(logoW+2*len(ouijaArt), shift)
	lines := make([]string, len(ouijaArt))
	for row, l := range ouijaArt {
		var lb strings.Builder
		for col, r := range l + strings.Repeat(" ", logoW-len(l)) {
			if r == ' ' || (reveal >= 0 && col > reveal) {
				lb.WriteRune(' ')
				continue
			}
			lb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(colors[col+2*row]).Render(string(r)))
		}
		lines[row] = lb.String()
	}
	return strings.Join(lines, "\n")
}

// logoWidth is the width of the figlet logo.
func logoWidth() int {
	w := 0
	for _, l := range ouijaArt {
		w = max(w, len(l))
	}
	return w
}

// introTicks is how many welcome ticks the intro takes (≈1.4s at 120ms).
const introTicks = 12

// introReveal is the intro's progress at tick (see renderWelcomeBanner), or
// -1 once it is over.
func introReveal(tick int) int {
	if tick >= introTicks || theme.NoMotion {
		return -1
	}
	return tick * logoWidth() / introTicks
}

// startGrid is where to start: four entries, the rest is in /help. /task
// stands for the totems — several independent passes, one answer.
func startGrid(width int) string {
	rows := [][2]string{
		{"@file", "attach a file"},
		{"/task", "plan it with several passes"},
		{"/learn", "teach ouija a fact"},
		{"/help", "every command and totem"},
	}
	cell := func(r [2]string) string {
		return panelCmd.Render(fmt.Sprintf("%-6s", r[0])) + "  " + panelKey.Render(r[1])
	}
	cols := []string{
		lipgloss.JoinVertical(lipgloss.Left, cell(rows[0]), cell(rows[2])),
		lipgloss.JoinVertical(lipgloss.Left, cell(rows[1]), cell(rows[3])),
	}
	grid := lipgloss.JoinHorizontal(lipgloss.Top, cols[0], "     ", cols[1])
	if lipgloss.Width(grid) > width { // narrow: one column
		grid = lipgloss.JoinVertical(lipgloss.Left, cell(rows[0]), cell(rows[1]), cell(rows[2]), cell(rows[3]))
	}
	return grid
}

// totemsLine names the registered totems on one line — the agents in Go that
// run several independent passes and reconcile them — each a slash command
// here ("" when none is registered).
func totemsLine(width int) string {
	totems := listTotems()
	if len(totems) == 0 {
		return ""
	}
	names := make([]string, len(totems))
	for i, t := range totems {
		names[i] = panelCmd.Render("/" + t.name)
	}
	line := panelTitle.Render("totems") + "  " + strings.Join(names, panelKey.Render(" · "))
	if full := line + panelHint.Render(" — agents in Go: several hands, one answer"); lipgloss.Width(full) <= width {
		return full
	}
	return ansi.Truncate(line, width, "…")
}

// sessionNote is what neither the header nor the status bar says: what
// ouija remembers about the project ("" when nothing).
func sessionNote(info welcomeInfo) string {
	switch {
	case info.facts == 1:
		return panelKey.Render("1 fact remembered about this project")
	case info.facts > 1:
		return panelKey.Render(fmt.Sprintf("%d facts remembered about this project", info.facts))
	}
	return ""
}

// renderWelcomeLine is the welcome once the conversation has started: the
// name and tagline on one line, centered.
func renderWelcomeLine(shift, width int) string {
	line := theme.GradientText("ouija", shift) + panelHint.Render("  ·  the board is listening  ·  /help")
	return lipgloss.PlaceHorizontal(width, lipgloss.Center, line)
}

// renderSessionSummary is the card printed when the chat exits: how long it
// ran, what it cost, and the files ouija changed.
func renderSessionSummary(m *model, cfg *engine.Config, tokens, steps int, usd float64) string {
	rows := [][2]string{
		{"time", formatElapsed(time.Since(m.startTime))},
		{"messages", fmt.Sprintf("%d", m.msgCount)},
		{"tokens", fmt.Sprintf("%s · %d steps", formatTokens(tokens), steps)},
		{"model", cfg.Model + panelKey.Render(" · "+cfg.Provider)},
	}
	if usd > 0 {
		rows = append(rows, [2]string{"cost", fmt.Sprintf("$%.2f", usd)})
	}
	var body strings.Builder
	body.WriteString(theme.GradientText("ouija", 0) + panelHint.Render("  ·  the board rests"))
	for _, r := range rows {
		fmt.Fprintf(&body, "\n%s %s", panelKey.Render(fmt.Sprintf("%-9s", r[0])), panelVal.Render(r[1]))
	}
	seen := map[string]bool{}
	var files []string
	for _, l := range m.lines {
		if l.role == "diff" && !seen[l.path] {
			seen[l.path] = true
			files = append(files, l.path)
		}
	}
	if len(files) > 0 {
		fmt.Fprintf(&body, "\n%s %s", panelKey.Render(fmt.Sprintf("%-9s", "changed")), panelVal.Render(fmt.Sprintf("%d file(s)", len(files))))
		for i, f := range files {
			if i == 8 {
				fmt.Fprintf(&body, "\n%s %s", strings.Repeat(" ", 9), panelKey.Render(fmt.Sprintf("… and %d more", len(files)-i)))
				break
			}
			fmt.Fprintf(&body, "\n%s %s", strings.Repeat(" ", 9), panelCmd.Render("✎ ")+panelVal.Render(f))
		}
	}
	return panelBorder.BorderForeground(theme.Lavender).Render(body.String())
}

// renderHelp is /help: the slash commands and the keys, a name column in pink
// and what it does beside it — two columns when the width allows.
func renderHelp(cmdList []slashCmd, width int) string {
	section := func(title string, rows [][2]string) string {
		nameW := 0
		for _, r := range rows {
			nameW = max(nameW, ansi.StringWidth(r[0]))
		}
		var b strings.Builder
		b.WriteString(panelTitle.Render("◆ " + title))
		for _, r := range rows {
			pad := strings.Repeat(" ", nameW-ansi.StringWidth(r[0]))
			fmt.Fprintf(&b, "\n%s%s  %s", panelCmd.Render(r[0]), pad, panelKey.Render(r[1]))
		}
		return b.String()
	}
	cmds := make([][2]string, len(cmdList))
	for i, c := range cmdList {
		cmds[i] = [2]string{c.cmd, c.desc}
	}
	left, right := section("commands", cmds), section("keys", helpKeys)
	if both := lipgloss.JoinHorizontal(lipgloss.Top, left, "    ", right); lipgloss.Width(both) <= width {
		return "  " + strings.ReplaceAll(both, "\n", "\n  ")
	}
	var b strings.Builder
	for _, l := range strings.Split(left+"\n\n"+right, "\n") {
		b.WriteString("  " + ansi.Truncate(l, width-2, "…") + "\n")
	}
	return strings.TrimSuffix(b.String(), "\n")
}

// learn saves a fact about the project (docs/knowledge.md, which the chat and
// the task totem read) and tells the model now, for this conversation too;
// with no fact it shows what ouija knows.
func (m *model) learn(fact string) {
	root := m.eng.Config().ProjectRoot
	if fact == "" {
		known := strings.TrimSpace(knowledge.Load(root))
		if known == "" {
			m.say("assistant", "Nothing learned about this project yet. Teach a fact with `/learn <fact>` — "+
				"it goes to `"+knowledge.FileName+"` and every later chat and `/task` reads it.")
			return
		}
		m.say("assistant", known)
		return
	}
	if err := knowledge.Save(root, fact); err != nil {
		m.addBanner("⚠ not learned: " + err.Error())
		return
	}
	if !strings.Contains(m.system, knownFactsHeading) {
		m.system += "\n\n" + knownFactsHeading + "\n"
	}
	m.system += "- " + fact + "\n"
	m.welcome.facts++
	m.welcomeKey = "" // the session panel counts the facts
	m.addBanner("✓ learned — saved to " + knowledge.FileName + "; ouija will know it in every chat here")
}

// knownFactsHeading introduces the project facts in the system prompt (the
// one cmd/ouija builds the chat's prompt with).
const knownFactsHeading = "## Known project facts"
