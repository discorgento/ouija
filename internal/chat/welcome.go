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
func renderWelcomeBanner(shift, width, reveal int, cfg *engine.Config, info welcomeInfo) string {
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
		Render("ask about this project — ouija reads and searches it, and edits only with your approval") + "\n\n")

	session, start := sessionPanel(cfg, info), startPanel()
	panels := lipgloss.JoinHorizontal(lipgloss.Top, session, "  ", start)
	if lipgloss.Width(panels) > width {
		panels = lipgloss.JoinVertical(lipgloss.Left, session, start)
	}
	b.WriteString(center(panels))
	if totems := totemsPanel(min(width, max(lipgloss.Width(panels), 60))); totems != "" {
		b.WriteString("\n" + center(totems))
	}
	return b.String()
}

// totemsPanel lists the registered totems — agents written in Go that run
// several independent passes and reconcile them — with what each does and
// how to run it here.
func totemsPanel(width int) string {
	totems := listTotems()
	if len(totems) == 0 {
		return ""
	}
	nameW := 0
	for _, t := range totems {
		nameW = max(nameW, len(t.name)+1)
	}
	var body strings.Builder
	body.WriteString(panelTitle.Render("◆ totems") + panelHint.Render("  agents in Go: several hands on the board, one answer"))
	for _, t := range totems {
		row := panelCmd.Render(fmt.Sprintf("%-*s", nameW, "/"+t.name)) + "  " + panelKey.Render(t.summary)
		fmt.Fprintf(&body, "\n%s", ansi.Truncate(row, max(width-4, 20), "…"))
	}
	return panelBorder.Width(width).Render(body.String())
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

// sessionPanel lists what this session runs with.
func sessionPanel(cfg *engine.Config, info welcomeInfo) string {
	project := "—"
	if cfg.ProjectRoot != "" {
		project = ansi.Truncate(displayPath(cfg.ProjectRoot), 34, "…")
	}
	if info.branch != "" {
		project += panelKey.Render("  ⎇ ") + panelVal.Render(info.branch)
	}

	window := "unknown"
	if cfg.ContextWindow > 0 {
		window = formatTokens(cfg.ContextWindow) + " tokens"
	}

	budget := "no limit"
	var limits []string
	if usd := cfg.MaxUSD(); usd > 0 {
		limits = append(limits, fmt.Sprintf("$%.2f", usd))
	}
	if cfg.Budget.MaxTokens > 0 {
		limits = append(limits, formatTokens(cfg.Budget.MaxTokens)+" tok")
	}
	if len(limits) > 0 {
		budget = strings.Join(limits, " · ")
	}

	memory := panelHint.Render("none yet — /learn <fact>")
	switch {
	case info.facts == 1:
		memory = panelVal.Render("1 fact")
	case info.facts > 1:
		memory = panelVal.Render(fmt.Sprintf("%d facts", info.facts))
	}

	rows := [][2]string{
		{"project", panelVal.Render(project)},
		{"model", panelVal.Render(cfg.Model)},
		{"provider", panelVal.Render(cfg.Provider)},
		{"context", panelVal.Render(window)},
		{"budget", panelVal.Render(budget)},
		{"memory", memory},
	}
	var body strings.Builder
	body.WriteString(panelTitle.Render("◆ session"))
	for _, r := range rows {
		fmt.Fprintf(&body, "\n%s %s", panelKey.Render(fmt.Sprintf("%-8s", r[0])), r[1])
	}
	return panelBorder.Render(body.String())
}

// startPanel lists the commands and keys worth knowing first.
func startPanel() string {
	rows := [][2]string{
		{"@file", "attach a file to your message"},
		{"/learn", "teach ouija a fact"},
		{"/model", "switch the model"},
		{"/theme", "change the colors"},
		{"Ctrl+F", "search the conversation"},
		{"/help", "every command and key"},
	}
	var body strings.Builder
	body.WriteString(panelTitle.Render("◆ start here"))
	for _, r := range rows {
		fmt.Fprintf(&body, "\n%s %s", panelCmd.Render(fmt.Sprintf("%-7s", r[0])), panelKey.Render(r[1]))
	}
	return panelBorder.Render(body.String())
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
