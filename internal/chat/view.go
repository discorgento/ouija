package chat

import (
	"fmt"
	"image/color"
	"slices"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"

	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"
	"github.com/discorgento/ouija/internal/theme"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/version"
)

// ─── Glamour renderer ─────────────────────────────────────────────────

// ouijaStyleJSON is a custom glamour style matching the ouija color scheme.
// Base text is the terminal's own color; headings keep their # prefix in the palette's pink and
// violets, list items keep their bullets, inline code is pink on dark, and code
// blocks are syntax-highlighted in the same palette. Code blocks get no
// background of their own: the card's would be restored after every token.
var ouijaStyleJSON = fmt.Sprintf(`{
    "strong": {"bold": true},
    "emph": {"italic": true},
    "code": {"prefix": "\u00a0", "suffix": "\u00a0", "color": "%[1]s", "background_color": "#303030"},
    "code_block": {"margin": 1, "chroma": {
        "error": {"color": "#ffffff", "background_color": "#d7005f"},
        "comment": {"color": "#6c6c8a", "italic": true},
        "comment_preproc": {"color": "#ff87d7"},
        "keyword": {"color": "#af87ff", "bold": true},
        "keyword_reserved": {"color": "#af87ff", "bold": true},
        "keyword_namespace": {"color": "#ff5fd7"},
        "keyword_type": {"color": "#87afff"},
        "operator": {"color": "#d7afff"},
        "punctuation": {"color": "#9e9e9e"},
        "name_builtin": {"color": "#ff87d7"},
        "name_tag": {"color": "#af87ff"},
        "name_attribute": {"color": "#87d7ff"},
        "name_class": {"color": "#ffd787", "bold": true},
        "name_decorator": {"color": "#ffd787"},
        "name_function": {"color": "#87d7ff"},
        "literal_number": {"color": "#ffaf5f"},
        "literal_string": {"color": "#afd787"},
        "literal_string_escape": {"color": "#87ffd7"},
        "generic_deleted": {"color": "#ff5f5f"},
        "generic_inserted": {"color": "#87ff87"},
        "generic_emph": {"italic": true},
        "generic_strong": {"bold": true},
        "generic_subheading": {"color": "#8a8a8a"}
    }},
    "heading": {"bold": true},
    "h1": {"prefix": "# ", "color": "%[1]s", "bold": true},
    "h2": {"prefix": "## ", "color": "%[2]s", "bold": true},
    "h3": {"prefix": "### ", "color": "%[3]s", "bold": true},
    "h4": {"prefix": "#### ", "bold": true},
    "h5": {"prefix": "##### ", "bold": true},
    "h6": {"prefix": "###### ", "bold": true},
    "list": {"level_indent": 2},
    "item": {"block_prefix": "• "},
    "enumeration": {"block_prefix": ". "},
    "block_quote": {"color": "245", "italic": true},
    "link": {"color": "%[2]s", "underline": true},
    "hr": {"color": "#5f5f5f"}
}`, theme.Hex(theme.Pink), theme.Hex(theme.Lavender), theme.Hex(theme.Violet))

// glamRenderers caches TermRenderer instances by word-wrap width.
// Width rarely changes (only on terminal resize), so this avoids
// recreating the renderer on every frame.
// The cache is bounded at maxGlamRenderers entries to prevent unbounded
// growth from repeated resizes.
var (
	glamMu           sync.Mutex
	glamRenderers    = make(map[int]*glamour.TermRenderer)
	maxGlamRenderers = 8
)

// renderGlamourFull renders the entire markdown text through glamour
// with word wrapping at the given width. Returns ANSI-styled text
// without a trailing newline. Falls back to plain text on error.
func renderGlamourFull(text string, width int) string {
	if text == "" || width < 10 {
		return text
	}
	r := getGlamRenderer(width)
	if r == nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimSuffix(out, "\n")
}

// getGlamRenderer returns a cached TermRenderer for the given width,
// creating one if necessary.
func getGlamRenderer(width int) *glamour.TermRenderer {
	glamMu.Lock()
	defer glamMu.Unlock()

	if r, ok := glamRenderers[width]; ok {
		return r
	}
	r, err := glamour.NewTermRenderer(
		glamour.WithStylesFromJSONBytes([]byte(ouijaStyleJSON)),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return nil
	}
	if len(glamRenderers) >= maxGlamRenderers {
		// Evict all on overflow — terminal resize is infrequent,
		// so rebuilding the cache is negligible.
		glamRenderers = make(map[int]*glamour.TermRenderer)
	}
	glamRenderers[width] = r
	return r
}

// ─── Card component ────────────────────────────────────────────────────

// card is a reusable card component with background, foreground,
// configurable padding, and ANSI reset restoration.
// Create instances via newCard and customize padding with WithPadding.
//
// The ANSI escape sequences for the foreground and background colors are
// derived automatically from the color.Color values via styleCode —
// no hand-crafted ANSI strings needed.
type card struct {
	bg     color.Color
	fg     color.Color
	bgCode string      // cached ANSI for background
	fgCode string      // cached ANSI for foreground
	padV   int         // vertical padding (top + bottom)
	padH   int         // horizontal padding (left + right)
	border color.Color // rounded border color (nil = no border)
}

// newCard creates a card with the given background and foreground colours.
// Default padding is (1, 1). Use WithPadding to customise.
func newCard(bg, fg color.Color) card {
	return card{
		bg: bg, fg: fg,
		bgCode: styleCode(bg, true),
		fgCode: styleCode(fg, false),
		padV:   1, padH: 1,
	}
}

// WithPadding returns a copy with different padding values.
func (c card) WithPadding(v, h int) card {
	c.padV = v
	c.padH = h
	return c
}

// WithBorder returns a copy framed by a rounded border in c.
func (c card) WithBorder(border color.Color) card {
	c.border = border
	return c
}

// Render renders the content inside the card at the given width.
// The card's background colour is preserved across any ANSI resets
// that rendered content (e.g. glamour) may contain.
func (c card) Render(content string, width int) string {
	s := lipgloss.NewStyle().
		Foreground(c.fg).
		Background(c.bg).
		Padding(c.padV, c.padH).
		Width(width)
	if c.border != nil {
		s = s.Border(lipgloss.RoundedBorder()).BorderForeground(c.border).BorderBackground(c.bg)
	}
	rendered := s.Render(strings.TrimSuffix(content, "\n"))
	rendered = keepCardBG(rendered, c.bgCode, c.fgCode)
	// The trailing \n terminates the last padding line — lipgloss
	// Padding does NOT include a trailing newline. Without it, the
	// next element renders on the same visual line.
	return rendered + ansi.ResetStyle + "\n"
}

// styleCode renders a test character through lipgloss with the given
// colour and returns the ANSI SGR sequence that sets it.
// Only used during card initialisation — never on hot paths.
func styleCode(c color.Color, bg bool) string {
	s := lipgloss.NewStyle()
	if bg {
		s = s.Background(c)
	} else {
		s = s.Foreground(c)
	}
	out := s.Render("x")
	// lipgloss output: "<SGR-sequence>x\x1b[m"
	idx := strings.Index(out, "x")
	if idx < 0 {
		return ""
	}
	return out[:idx]
}

// msgContentWidth returns the content width available for text inside a
// message card at the given viewport width, after reserving space for the
// pipePrefix (timeline marker + indent) and the card's horizontal padding.
func msgContentWidth(vpW int, pipePfx string) int {
	return vpW - lipgloss.Width(pipePfx) - 2
}

// Named card instances — each one maps to a specific visual element.
var (
	// Message cards  ──────────────────────────────────────────────
	UserCard   = newCard(cardBgOui, white).WithPadding(0, 1) // user messages (a bubble inside)
	AICard     = newCard(cardBgOui, white).WithPadding(0, 1) // AI messages, diffs, streaming
	BannerCard = newCard(cardBgOui, gray).WithPadding(0, 1)  // system banners
	TraceCard  = newCard(cardBgOui, white).WithPadding(0, 1) // tool trace

	// UI component cards  ─────────────────────────────────────────
	HeaderCard   = newCard(cardBg, white).WithPadding(0, 1)                          // top bar (slim)
	InputCard    = newCard(cardBg, white).WithPadding(0, 1)                          // text input (framed by state: WithBorder)
	FooterCard   = newCard(cardBg, white).WithPadding(0, 1)                          // status bar (slim)
	SlashCard    = newCard(cardBg, white).WithPadding(0, 1).WithBorder(theme.Muted)  // command picker
	ApprovalCard = newCard(cardBg, white)                                            // approval prompt
	StatusCard   = newCard(cardBg, white).WithPadding(0, 1)                          // AI activity status (slim)
	WelcomeCard  = newCard(cardBgOui, white)                                         // animated welcome banner
	ErrorCard    = newCard(cardBgOui, white).WithPadding(0, 1).WithBorder(theme.Red) // failed turn
)

// keepCardBG replaces every ANSI reset (\x1b[m) in the rendered string
// with reset + the card's own style codes (fg+bg). Glamour emits resets
// inside markdown which would clear the card background — this restores
// the card colours after each reset without needing raw ANSI strings.
//
// Both reset spellings count: lipgloss writes \x1b[m, the syntax highlighter
// \x1b[0m.
func keepCardBG(s, bgCode, fgCode string) string {
	s = strings.ReplaceAll(s, "\x1b[0m", ansi.ResetStyle)
	return strings.ReplaceAll(s, ansi.ResetStyle, ansi.ResetStyle+fgCode+bgCode)
}

// ─── Main View ────────────────────────────────────────────────────────

func (m *model) View() tea.View {
	// NOT calling rebuildContent here! View is called every frame
	// by bubbletea, and rebuildContent with SetContent → maxLineWidth
	// parses ANSI from ALL lines — infeasible at 60fps.
	// rebuildContent is called explicitly in Update when
	// content changes (handleSend, handleResponse, handleResize, tokenMsg).
	w := max(m.width, 20)
	// The last line must not end in a newline: it would be one line more than
	// the terminal has and push the header off the screen.
	screen := m.renderHeader(w) + m.vp.View() + "\n" + m.renderBottom(w)
	v := tea.NewView(strings.TrimSuffix(screen, "\n"))
	v.AltScreen = true
	// Cell motion, not all motion: only the wheel is used, and every mouse
	// move redrew the screen.
	v.MouseMode = tea.MouseModeCellMotion
	if m.selectMode {
		v.MouseMode = tea.MouseModeNone // the terminal selects text again
	}
	v.ReportFocus = true // a long reply ends with a notification in the background
	v.WindowTitle = m.windowTitle()
	return v
}

// renderHeader renders the top bar: logo, branch and project on the left,
// model, provider and context-window use on the right. What does not fit is
// dropped (the context gauge, then the project path) so it never wraps.
func (m *model) renderHeader(w int) string {
	cfg := m.eng.Config()
	width := w - 2
	sep := sepLine.Render(" │ ")

	left := hdrOui.Render("● ") + theme.GradientText("ouija", m.hueShift) + " " + ftDim.Render(version.Version)
	if m.welcome.branch != "" {
		left += sep + hdrBranch.Render("⎇ "+m.welcome.branch)
	}
	path := ""
	if cfg.ProjectRoot != "" {
		path = sep + hdrMeta.Render(displayPath(cfg.ProjectRoot))
	}

	right := hdrModel.Render(cfg.Model) + hdrMeta.Render(" · "+cfg.Provider)
	ctx := ""
	if cfg.ContextWindow > 0 {
		used := engine.ContextTokens(m.system, m.history)
		ratio := float64(used) / float64(cfg.ContextWindow)
		ctx = sep + hdrMeta.Render("ctx ") + contextGauge(6, ratio) + " " + ftScope.Render(fmt.Sprintf("%d%%", int(ratio*100)))
	}

	fits := func(l, r string) bool { return lipgloss.Width(l)+lipgloss.Width(r)+1 <= width }
	switch {
	case fits(left+path, right+ctx):
		left, right = left+path, right+ctx
	case fits(left, right+ctx):
		right += ctx
	}
	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	top := ansi.Truncate(left+strings.Repeat(" ", gap)+right, width, "…")
	return HeaderCard.Render(top+"\n"+sepLine.Render(strings.Repeat("─", width)), w)
}

// contextGauge renders how full the context window is as ▰▱ cells, colored
// like the budget bar: violet, amber past half, red past 80%.
func contextGauge(cells int, ratio float64) string {
	ratio = min(max(ratio, 0), 1)
	filled := int(ratio*float64(cells) + 0.5)
	if ratio > 0 && filled == 0 {
		filled = 1 // anything in the window shows
	}
	return lipgloss.NewStyle().Foreground(severityColor(ratio)).Render(strings.Repeat("▰", filled)) +
		lipgloss.NewStyle().Foreground(theme.Muted).Render(strings.Repeat("▱", cells-filled))
}

// severityColor is the color of a gauge at ratio: violet, amber past half,
// red past 80%.
func severityColor(ratio float64) color.Color {
	switch {
	case ratio > 0.8:
		return theme.Red
	case ratio > 0.5:
		return theme.Amber
	}
	return theme.Purple
}

// bannerStyle colors a transcript notice by its leading symbol: success,
// warning, error or cancellation; anything else stays muted.
func bannerStyle(text string) lipgloss.Style {
	s := lipgloss.NewStyle().Foreground(theme.Gray)
	switch {
	case strings.HasPrefix(text, "✓"):
		return s.Foreground(theme.Green)
	case strings.HasPrefix(text, "⚠"):
		return s.Foreground(theme.Amber)
	case strings.HasPrefix(text, "✕"), strings.HasPrefix(text, "✗"):
		return s.Foreground(theme.Red)
	}
	return s
}

// renderBottom renders everything below the viewport: AI status line, slash
// list or picker, approval prompt or input, and the status bar. setViewportSize
// measures it, so the viewport always gets exactly the lines left over.
func (m *model) renderBottom(w int) string {
	var buf strings.Builder

	// ── AI status line (persistent, never scrolls) ─────────────────
	if m.waiting && m.approvalReq == nil { // the approval card shows the call
		buf.WriteString(StatusCard.Render(m.renderStatusLine(), w))
	}

	// ── slash command list / picker ────────────────────────────────
	if m.picker.Visible {
		buf.WriteString(m.picker.Render(w))
	} else if m.showCmdList {
		var cmdBuf strings.Builder
		typed := strings.TrimSpace(m.input.Value())
		cmdW := 0
		for _, sc := range slashCmds {
			cmdW = max(cmdW, lipgloss.Width(sc.cmd))
		}
		for i, sc := range slashCmds {
			matches := strings.HasPrefix(sc.cmd, typed)
			sel, s, desc := " ", ftDim, ftDim
			if !matches {
				s, desc = ftScope, ftScope // cannot become what is typed
			}
			if i == m.selCmdIdx {
				sel, s, desc = pickerCursor.Render("❯"), ftVal, hdrMeta
			}
			name := s.Render(sc.cmd)
			if matches {
				name = slashTyped.Render(typed) + s.Render(sc.cmd[len(typed):])
			}
			pad := strings.Repeat(" ", cmdW-lipgloss.Width(sc.cmd))
			row := fmt.Sprintf(" %s %s%s  %s", sel, name, pad, desc.Render(sc.desc))
			fmt.Fprintf(&cmdBuf, "%s\n", ansi.Truncate(row, w-2, "…"))
		}
		buf.WriteString(SlashCard.Render(cmdBuf.String(), w))
	}

	// ── Approval prompt or pending edit state ─────────────────────
	if m.approvalReq != nil {
		// border (2) + padding (2) around the body
		body := RenderApprovalBox(m.approvalReq, m.toolCallN, m.approvalIdx, w-4)
		buf.WriteString(ApprovalCard.WithBorder(approvalToolColor(m.approvalReq.tool)).Render(body, w))
	} else if m.pendingEdit != nil {
		editMsg := lipgloss.NewStyle().Italic(true).Foreground(theme.Orange).Render("  ✎ editing in external editor...\n")
		buf.WriteString(ApprovalCard.Render(editMsg, w))
	} else {
		border := theme.Violet // ready
		switch {
		case m.search.active, m.histSearch != nil:
			border = theme.Amber
		case m.waiting:
			border = theme.Muted // typing now queues the message
		}
		buf.WriteString(InputCard.WithBorder(border).Render(m.input.View(), w))
	}

	buf.WriteString(FooterCard.Render(m.renderStatusBar(w-2), w))
	return buf.String()
}

// renderStatusLine says what ouija is doing while a reply runs: the tool in
// use (icon, name, target) or a shimmering "thinking", then the time spent and
// the tokens streamed so far.
func (m *model) renderStatusLine() string {
	elapsed := time.Since(m.responseStart)
	var what string
	if m.currentTool != "" {
		tool, arg, _ := strings.Cut(m.currentTool, ": ")
		c := approvalToolColor(tool)
		what = lipgloss.NewStyle().Bold(true).Foreground(c).Render(approvalToolIcon(tool)+" "+tool) +
			" " + hdrMeta.Render(arg)
	} else {
		// The gradient flows with time: the spinner's ticks redraw the screen.
		what = theme.GradientText("thinking…", int(elapsed/(80*time.Millisecond)))
	}
	meta := fmt.Sprintf("%ds", int(elapsed.Seconds()))
	if m.streamingTokens > 0 {
		meta += " · ~" + formatTokens(m.streamingTokens) + " tok"
	}
	line := fmt.Sprintf(" %s %s  %s", m.spinner.View(), what, ftScope.Render(meta))
	return ansi.Truncate(line, max(m.width-2, 18), "…")
}

// renderStatusBar renders the status bar content at the given width: counters
// and budget on the left, cost, tokens and key hints on the right. What does
// not fit is dropped (hints first) so the bar never wraps.
func (m *model) renderStatusBar(width int) string {
	left := ftScope.Render(fmt.Sprintf("%d msg", m.msgCount))
	if m.steps > 0 {
		left += fmt.Sprintf("  %s", ftDim.Render(fmt.Sprintf("%d steps", m.steps)))
	}

	// Budget progress bar — compact visual of the most restrictive ceiling,
	// only when one is set.
	displayTokens := m.tokens
	if m.waiting && m.streamingTokens > displayTokens {
		displayTokens = m.streamingTokens
	}
	b := m.eng.Config().Budget
	if pct, lbl, ok := budgetUsage(displayTokens, m.usd, b.MaxUSD, b.MaxTokens); ok && displayTokens > 0 {
		what := "budget"
		if lbl == "" {
			what = "token budget"
		}
		left += fmt.Sprintf("  %s %s %s", ftScope.Render(what), contextGauge(6, pct/100), ftScope.Render(fmt.Sprintf("%.0f%%", pct)))
	}

	left += fmt.Sprintf("  %s", ftDim.Render(formatElapsed(time.Since(m.startTime))))
	scrollPct := m.vp.ScrollPercent()
	if scrollPct > 0 && scrollPct < 0.99 {
		left += fmt.Sprintf("  %s", ftScope.Render(fmt.Sprintf("%d%%", int(scrollPct*100))))
	}

	right := ftVal.Render(formatTokens(displayTokens) + " tok")
	if m.usd > 0 {
		right = fmt.Sprintf("%s  %s", ftVal.Render(fmt.Sprintf("$%.2f", m.usd)), right)
	}
	if len(m.pendingQueue) > 0 {
		right = fmt.Sprintf("%s  %s", ftScope.Render(fmt.Sprintf("(%d queued)", len(m.pendingQueue))), right)
	}
	// Lines that arrived while scrolled up: say so, at the start of the bar.
	if unseen := len(m.lines) - m.seenLines; unseen > 0 && !m.autoScroll && !m.vp.AtBottom() {
		left = pillNew.Render(fmt.Sprintf(" ↓ %d new · End ", unseen)) + "  " + left
	}
	if m.selectMode {
		left = pillSelect.Render(" SELECT ") + "  " + left
	}

	var shortcuts string
	switch {
	case m.search.active:
		shortcuts = "  " + lipgloss.NewStyle().Foreground(theme.Amber).Render("search "+m.searchStatus())
	case m.toastText != "":
		shortcuts = "  " + bannerStyle(m.toastText).Render(m.toastText)
	case m.approvalReq != nil:
		// The approval box shows its own keys.
	case m.waiting:
		shortcuts = ftDim.Render("  Esc cancel")
	case len(m.pendingQueue) > 0:
		shortcuts = ftDim.Render("  ↑ edit · Ctrl+Y copy · Esc clear queue · /help")
	default:
		shortcuts = "  " + ftScope.Render("tip ") + ftDim.Render(currentTip(time.Since(m.startTime))+" · /help")
	}
	if lipgloss.Width(left)+lipgloss.Width(right)+lipgloss.Width(shortcuts)+1 <= width {
		right += shortcuts
	} else if m.toastText != "" {
		right = shortcuts // a notice wins over the counters
	}

	gap := max(width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	return ansi.Truncate(left+strings.Repeat(" ", gap)+right, width, "…")
}

// tips teach the keys and commands one at a time, in the status bar.
var tips = []string{
	"@ attaches a file to the message",
	"Ctrl+F searches the conversation",
	"/undo takes the last file change back",
	"/task plans with several independent passes",
	"Ctrl+T shows each tool call",
	"Alt+Y copies the last code block",
	"/retry asks the last message again",
	"ouija chat -c resumes the last conversation",
	"Shift+Enter adds a new line",
	"↑ edits your last message",
}

// tipEvery is how long a tip stays.
const tipEvery = 20 * time.Second

// currentTip is the tip shown elapsed into the session.
func currentTip(elapsed time.Duration) string {
	return tips[int(elapsed/tipEvery)%len(tips)]
}

// formatElapsed renders a session duration: "4m12s", or "1h04m" past an hour.
func formatElapsed(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dm%ds", int(d.Minutes()), int(d.Seconds())%60)
}

// formatTokens renders a token count compactly: 950, 12.3k, 200k, 1.2M.
func formatTokens(n int) string {
	var s string
	switch {
	case n >= 1_000_000:
		s = fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	case n >= 1_000:
		s = fmt.Sprintf("%.1fk", float64(n)/1_000)
	default:
		return fmt.Sprintf("%d", n)
	}
	return strings.Replace(s, ".0", "", 1) // 200.0k reads as 200k
}

// ─── rebuildContent ───────────────────────────────────────────────────

func (m *model) rebuildContent() {
	vpW := m.vp.Width()
	if vpW < 10 {
		vpW = 10
	}

	// A line's rendering depends only on its content and the width, and lines
	// are never edited after they are appended: re-render only on resize.
	if vpW != m.cacheWidth {
		for i := range m.lines {
			m.lines[i].cached = ""
		}
		m.cacheWidth = vpW
	}

	var buf strings.Builder

	// Animated welcome — cached; re-rendered when the gradient moves (tick),
	// the width changes or /model and /provider change what it shows.
	// After the first message it shrinks to one line: the header shows the
	// session from then on.
	cfg := m.eng.Config()
	compact := m.msgCount > 0
	// Alone on screen, it is centered in the viewport's height.
	alone := len(m.lines) == 0 && !m.waiting
	if key := fmt.Sprintf("%d|%d|%d|%s|%s|%d|%v|%v", m.hueShift, vpW, m.vp.Height(), cfg.Model, cfg.Provider, cfg.ContextWindow, compact, alone); key != m.welcomeKey {
		switch {
		case compact:
			m.welcomeBanner = WelcomeCard.WithPadding(0, 1).Render(renderWelcomeLine(m.hueShift, vpW-2), vpW)
		default:
			banner := renderWelcomeBanner(m.hueShift, vpW-2, introReveal(m.tickCount), cfg, m.welcome)
			if alone {
				banner = lipgloss.PlaceVertical(max(m.vp.Height()-2, lipgloss.Height(banner)), lipgloss.Center, banner)
			}
			m.welcomeBanner = WelcomeCard.Render(banner, vpW)
		}
		m.welcomeKey = key
	}
	buf.WriteString(m.welcomeBanner)

	for i := range m.lines {
		l := &m.lines[i]

		// Use cached rendering if available and width matches.
		if l.cached != "" {
			buf.WriteString(l.cached)
			continue
		}

		// Render from scratch and cache.
		var lineBuf strings.Builder
		var msgBuf strings.Builder

		switch l.role {
		case "user":
			lineBuf.WriteString(UserCard.Render(renderUserBubble(l.text, l.ts, l.meta, vpW-2), vpW))

		case "assistant":
			pipePfx := "  " + pipeOui + " "
			contentWidth := msgContentWidth(vpW, pipePfx)
			meta := ""
			if l.meta != "" {
				meta = ftScope.Render(" · " + l.meta)
			}
			fmt.Fprintf(&msgBuf, "%s%s %s%s\n", pipePfx, l.ts, roleOui.Render("ouija"), meta)
			// The tools the reply came from: one line, or the details.
			if tools := m.renderTurnTools(l, contentWidth); len(tools) > 0 {
				for _, tl := range tools {
					fmt.Fprintf(&msgBuf, "%s%s\n", pipePfx, tl)
				}
				fmt.Fprintf(&msgBuf, "%s\n", pipePfx)
			}
			rendered := renderGlamourFull(l.text, contentWidth)
			for _, line := range strings.Split(rendered, "\n") {
				fmt.Fprintf(&msgBuf, "%s%s\n", pipePfx, line)
			}
			lineBuf.WriteString(AICard.Render(msgBuf.String(), vpW))
			msgBuf.Reset()

		case "help":
			lineBuf.WriteString(AICard.Render(renderHelp(vpW-2), vpW))

		case "error":
			lineBuf.WriteString(ErrorCard.Render(renderErrorBlock(l.meta, l.text, l.hint, vpW-4), vpW))

		case "diff":
			lineBuf.WriteString(AICard.Render(renderDiffBlock(l.path, l.text, vpW-2), vpW))

		case "banner":
			if l.text != "" {
				lineBuf.WriteString(BannerCard.Render("  "+bannerStyle(l.text).Render(l.text), vpW))
			}

		case "trace":
			pipePfx := "  " + pipeTrace + " "
			for _, tl := range m.renderTurnTools(l, msgContentWidth(vpW, pipePfx)) {
				fmt.Fprintf(&msgBuf, "%s%s\n", pipePfx, tl)
			}
			lineBuf.WriteString(TraceCard.Render(msgBuf.String(), vpW))
			msgBuf.Reset()
		}

		// One blank line between blocks (the cards themselves are slim).
		if lineBuf.Len() > 0 {
			l.cached = blockGap(vpW) + lineBuf.String()
		}
		buf.WriteString(l.cached)
	}

	// What is settled (the welcome and the lines) is kept apart from what is
	// still moving (rebuildStreaming redraws only the latter).
	static := buf.String()
	m.staticLines = strings.Split(strings.TrimSuffix(static, "\n"), "\n")
	m.vpTail = false
	m.vp.SetContent(static + m.renderLive(vpW))

	if m.search.query != "" {
		m.applySearch() // the content changed under the highlights
	}
	if m.autoScroll {
		m.vp.GotoBottom()
		m.seenLines = len(m.lines)
	}
}

// renderLive renders what moves while a reply runs, below the settled lines:
// the turn's tools, the reply streaming in, the queued messages.
func (m *model) renderLive(vpW int) string {
	var buf strings.Builder
	if m.totem != nil { // a totem's board replaces the chat turn's tools
		pipePfx := "  " + pipeTrace + " "
		var tb strings.Builder
		for _, tl := range m.renderTotemBoard(msgContentWidth(vpW, pipePfx)) {
			fmt.Fprintf(&tb, "%s%s\n", pipePfx, tl)
		}
		buf.WriteString(blockGap(vpW) + TraceCard.Render(tb.String(), vpW))
	}
	// The running turn's tools: one line counting them, or (Ctrl+T) each call
	// as it starts and finishes.
	if m.waiting && m.totem == nil {
		pipePfx := "  " + pipeTrace + " "
		sum, width := m.eng.Tracer().LastTurn(), msgContentWidth(vpW, pipePfx)
		tools := renderToolsBlock(sum, width, true)
		if !m.showTrace && len(sum.Tools) > 0 {
			tools = []string{renderLiveSummary(sum, m.approvalReq != nil, width)}
		}
		if tools != nil {
			var tb strings.Builder
			for _, tl := range tools {
				fmt.Fprintf(&tb, "%s%s\n", pipePfx, tl)
			}
			buf.WriteString(blockGap(vpW) + TraceCard.Render(tb.String(), vpW))
		}
	}

	// The reply streaming in. Before its first token the status line below the
	// viewport is the only place showing what ouija is doing.
	if m.waiting && m.partialText != "" {
		var msgBuf strings.Builder
		pipePfx := "  " + pipeOui + " "
		contentWidth := msgContentWidth(vpW, pipePfx)
		fmt.Fprintf(&msgBuf, "%s%s %s\n",
			pipePfx, time.Now().Format("15:04"), roleOui.Render("ouija"))
		rendered := m.renderStreamingMarkdown(m.partialText, contentWidth)
		for _, line := range strings.Split(rendered, "\n") {
			fmt.Fprintf(&msgBuf, "%s%s\n", pipePfx, line)
		}
		buf.WriteString(blockGap(vpW) + AICard.Render(msgBuf.String(), vpW))
	}

	// ── Queue preview inside viewport (scrolls naturally) ──────────
	if len(m.pendingQueue) > 0 {
		var qBuf strings.Builder
		queueHint := txtDim.Render(fmt.Sprintf("● pending: %d · Esc clears", len(m.pendingQueue)))
		fmt.Fprintf(&qBuf, "%s\n", queueHint)
		for i, msg := range m.pendingQueue {
			preview := msg
			if len(preview) > int(float64(vpW)*0.4) {
				preview = strings.ToValidUTF8(preview[:int(float64(vpW)*0.4)-3], "") + "..."
			}
			fmt.Fprintf(&qBuf, "%s\n", txtDim.Render(fmt.Sprintf("◇ %d: %s", i+1, preview)))
		}
		buf.WriteString(blockGap(vpW) + BannerCard.Render(qBuf.String(), vpW))
	}

	return buf.String()
}

// streamFrame is the shortest time between two redraws of a streaming reply:
// about 20 frames a second, so the text flows instead of landing in chunks.
const streamFrame = 50 * time.Millisecond

// rebuildStreaming redraws a reply as it streams. At the bottom of the
// transcript (the usual case) the viewport gets only the last screenful of
// the settled lines plus the live block: measuring every line of a long
// conversation on each frame cost more than drawing it. Scrolled up, or
// searching, it falls back to the full rebuild.
func (m *model) rebuildStreaming() {
	if !m.autoScroll || m.search.query != "" || m.staticLines == nil {
		m.rebuildContent()
		return
	}
	vpW := max(m.vp.Width(), 10)
	if vpW != m.cacheWidth {
		m.rebuildContent()
		return
	}
	keep := min(len(m.staticLines), m.vp.Height())
	tail := append(slices.Clone(m.staticLines[len(m.staticLines)-keep:]),
		strings.Split(m.renderLive(vpW), "\n")...) // as SetContent splits static+live
	m.vp.SetContentLines(tail)
	m.vpTail = true
	m.vp.GotoBottom()
}

// fullTranscript puts the whole transcript back in the viewport when it holds
// only the tail (rebuildStreaming), before the developer scrolls it.
func (m *model) fullTranscript() {
	if m.vpTail {
		m.rebuildContent()
	}
}

// renderStreamingMarkdown renders a reply still streaming: the paragraphs
// already complete are rendered once and kept, only the last one is rendered
// again on each frame. The final reply is rendered whole.
func (m *model) renderStreamingMarkdown(text string, width int) string {
	cut := settledMarkdown(text)
	if cut == 0 {
		return renderGlamourFull(text, width)
	}
	head, tail := text[:cut], text[cut:]
	if head != m.streamHead || width != m.streamWidth {
		m.streamHead, m.streamWidth = head, width
		m.streamHeadOut = renderGlamourFull(head, width)
	}
	if strings.TrimSpace(tail) == "" {
		return m.streamHeadOut
	}
	return m.streamHeadOut + "\n\n" + renderGlamourFull(tail, width)
}

// settledMarkdown returns where the complete paragraphs of text end: after
// its last blank line outside a code fence (0 when there is none).
func settledMarkdown(text string) int {
	cut, inFence, pos := 0, false, 0
	for _, l := range strings.SplitAfter(text, "\n") {
		pos += len(l)
		trimmed := strings.TrimSpace(l)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			inFence = !inFence
		}
		if trimmed == "" && !inFence && strings.HasSuffix(l, "\n") {
			cut = pos
		}
	}
	if cut >= len(text) {
		// Ending on a blank line: everything is settled but keep the last
		// paragraph live (it may be a list or a table still growing).
		return 0
	}
	return cut
}

// ─── Budget tracking ────────────────────────────────────────────────

// budgetUsage returns the percentage of the most restrictive budget ceiling
// consumed, and a label prefix ("$" for USD, "" for tokens). ok is false when
// no budget ceiling is configured (or both ceilings are zero/unlimited).
func budgetUsage(tokens int, usd float64, maxUSD *float64, maxTokens int) (pct float64, label string, ok bool) {
	if maxUSD != nil && *maxUSD > 0 {
		return max(usd / *maxUSD * 100, 0), "$", true
	}
	if maxTokens > 0 {
		return max(float64(tokens)/float64(maxTokens)*100, 0), "", true
	}
	return 0, "", false
}

// progressBar renders a colored bar showing how much of a budget ceiling
// has been consumed. Returns an empty string when ratio < 1% (too small
// to render meaningfully). The bar uses █ (filled) and ░ (empty) blocks,
// colored by severityColor.
func progressBar(width int, ratio float64) string {
	if ratio < 0 || width < 2 {
		return ""
	}
	if ratio > 1 {
		ratio = 1
	}
	filled := int(ratio * float64(width))
	if filled > width {
		filled = width
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)

	return lipgloss.NewStyle().Foreground(severityColor(ratio)).Render(bar)
}

// ─── Word wrapping ────────────────────────────────────────────────────

// wrapLines splits text into wrapped lines, each at most maxWidth wide.
// The first line has no indent; subsequent lines are indented by indent.
func wrapLines(text, indent string, maxWidth int) []string {
	if maxWidth < 8 {
		return []string{text}
	}
	// Word-wrap each line of the message on its own: Fields over the whole
	// text dropped the line breaks and indentation of a pasted snippet.
	var lines []string
	for i, para := range strings.Split(text, "\n") {
		lead := para[:len(para)-len(strings.TrimLeft(para, " \t"))] // keep code indentation
		words := strings.Fields(para)
		prefix := ""
		if i > 0 {
			prefix = indent // continuation lines of the message are indented
		}
		if len(words) == 0 {
			lines = append(lines, prefix)
			continue
		}
		line := prefix + lead + words[0]
		for _, word := range words[1:] {
			if candidate := line + " " + word; lipgloss.Width(candidate) <= maxWidth {
				line = candidate
			} else {
				lines = append(lines, line)
				line = indent + word
			}
		}
		lines = append(lines, line)
	}
	return lines
}

// ─── History + Copy ───────────────────────────────────────────────────

func (m *model) handleHistoryPrev() (tea.Model, tea.Cmd) {
	n := len(m.inputHistory)
	if n == 0 {
		return m, nil
	}
	if m.inputHistIdx == -1 {
		m.inputSaved = m.input.Value()
		m.inputHistIdx = n - 1
	} else if m.inputHistIdx > 0 {
		m.inputHistIdx--
	} else {
		return m, nil
	}
	m.input.SetValue(m.inputHistory[m.inputHistIdx])
	m.setViewportSize()
	return m, nil
}

func (m *model) handleHistoryNext() (tea.Model, tea.Cmd) {
	n := len(m.inputHistory)
	if n == 0 || m.inputHistIdx == -1 {
		return m, nil
	}
	if m.inputHistIdx == n-1 {
		m.inputHistIdx = -1
		m.input.SetValue(m.inputSaved)
		m.inputSaved = ""
	} else {
		m.inputHistIdx++
		m.input.SetValue(m.inputHistory[m.inputHistIdx])
	}
	m.setViewportSize()
	return m, nil
}

func (m *model) handleCopyMessage() (tea.Model, tea.Cmd) {
	for i := len(m.lines) - 1; i >= 0; i-- {
		if m.lines[i].role == "assistant" && strings.TrimSpace(m.lines[i].text) != "" {
			if err := clipboard.WriteAll(m.lines[i].text); err != nil {
				return m.toast("⚠ copy failed: " + err.Error())
			}
			return m.toast("✓ copied the last reply")
		}
	}
	return m.toast("nothing to copy yet")
}

// blockGap is the blank line between transcript blocks, on the transcript's
// background.
func blockGap(width int) string {
	return lipgloss.NewStyle().Background(cardBgOui).Width(width).Render("") + ansi.ResetStyle + "\n"
}

var (
	bubbleStyle = lipgloss.NewStyle().
			Foreground(theme.Text).
			PaddingRight(1).
			Border(lipgloss.Border{Right: "▐"}, false, true, false, false).
			BorderForeground(theme.Pink)
	bubbleHead   = lipgloss.NewStyle().Foreground(theme.Muted)
	mentionStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Lavender)
)

// renderUserBubble renders a message of the developer as a bubble on the
// right, at most three quarters of width wide, with "You · HH:MM" above it:
// their side of the conversation reads apart from ouija's at a glance.
//
// attached lists the files the message @mentions (shown under it); the
// mentions themselves are highlighted.
func renderUserBubble(text, ts, attached string, width int) string {
	maxW := max(min(width*3/4, width-4), 12)
	lines := wrapLines(text, "", maxW-2) // padding (1) + edge (1)
	for i, l := range lines {
		lines[i] = mentionRe.ReplaceAllStringFunc(l, func(m string) string {
			lead := m[:len(m)-len(strings.TrimLeft(m, " \t"))]
			return lead + mentionStyle.Render(strings.TrimLeft(m, " \t"))
		})
	}
	bubble := bubbleStyle.Render(strings.Join(lines, "\n"))
	head := roleYou.Render("You") + bubbleHead.Render(" · "+ts)
	parts := []string{head, bubble}
	if attached != "" {
		parts = append(parts, bubbleHead.Render(ansi.Truncate("⊕ attached: "+attached, maxW, "…")))
	}
	block := lipgloss.JoinVertical(lipgloss.Right, parts...)
	return lipgloss.PlaceHorizontal(width, lipgloss.Right, block)
}

// renderTurnTools renders a finished turn's tools: one summary line, or with
// Ctrl+T each call on its own line and the span tree.
func (m *model) renderTurnTools(l *line, width int) []string {
	if len(l.tools.Tools) == 0 {
		return nil
	}
	if !m.showTrace {
		return []string{renderToolsSummary(l.tools, width)}
	}
	out := renderToolsBlock(l.tools, width, false)
	for _, tr := range strings.Split(l.trace, "\n") {
		if strings.TrimSpace(tr) != "" {
			out = append(out, toolMeta.Render(ansi.Truncate(tr, width, "…")))
		}
	}
	return out
}
