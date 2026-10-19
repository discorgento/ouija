package chat

import (
	"fmt"
	"image/color"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2"
	"charm.land/lipgloss/v2"
	"github.com/atotto/clipboard"
	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/internal/version"
)

// ─── Glamour renderer ─────────────────────────────────────────────────

// ouijaStyleJSON is a custom glamour style matching the ouija color scheme.
// Base text is violet (#875fff ≈ lipgloss.Color("99")), with pink bold,
// lavender italic, and code blocks with dark backgrounds.
const ouijaStyleJSON = `{
    "document": {"color": "#ffffff"},
    "text": {"color": "#ffffff"},
    "strong": {"color": "#ffffff", "bold": true},
    "emph": {"color": "#ffffff", "italic": true},
    "code": {"color": "#d4d4d4", "background_color": "#303030"},
    "code_block": {"color": "#d4d4d4", "background_color": "#1e1e1e"},
    "heading": {"color": "#ffffff", "bold": true},
    "h1": {"color": "#ffffff", "bold": true},
    "h2": {"color": "#ffffff", "bold": true},
    "block_quote": {"color": "#c0c0c0", "italic": true},
    "link": {"color": "#ffffff", "underline": true},
    "hr": {"color": "#5f5f5f"}
}`

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

// renderGlamour renders plain markdown text into ANSI-styled terminal
// output without word wrapping. Used for code blocks and other cases
// where width is handled externally.
func renderGlamour(texts ...string) string {
	text := strings.Join(texts, " ")
	if text == "" {
		return ""
	}
	r := getGlamRenderer(0) // WithWordWrap(0) — no wrapping
	if r == nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return strings.TrimSuffix(out, "\n")
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
	bgCode string // cached ANSI for background
	fgCode string // cached ANSI for foreground
	padV   int    // vertical padding (top + bottom)
	padH   int    // horizontal padding (left + right)
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

// Render renders the content inside the card at the given width.
// The card's background colour is preserved across any ANSI resets
// that rendered content (e.g. glamour) may contain.
func (c card) Render(content string, width int) string {
	s := lipgloss.NewStyle().
		Foreground(c.fg).
		Background(c.bg).
		Padding(c.padV, c.padH).
		Width(width)
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
	UserCard   = newCard(cardBg, white)                    // user messages
	AICard     = newCard(cardBgOui, white)                 // AI messages, codeblocks, streaming
	BannerCard = newCard(cardBgOui, gray)                  // system banners
	TraceCard  = newCard(cardBgOui, lipgloss.Color("255")) // tool trace

	// UI component cards  ─────────────────────────────────────────
	HeaderCard   = newCard(cardBg, white).WithPadding(0, 1) // top bar (slim)
	InputCard    = newCard(cardBg, white)                   // text input
	FooterCard   = newCard(cardBg, white)                   // status bar
	SlashCard    = newCard(cardBg, white)                   // command picker
	ApprovalCard = newCard(cardBg, white)                   // approval prompt
	StatusCard   = newCard(cardBg, white).WithPadding(0, 1) // AI activity status (slim)
	WelcomeCard  = newCard(cardBgOui, white)                // animated welcome banner
)

// keepCardBG replaces every ANSI reset (\x1b[m) in the rendered string
// with reset + the card's own style codes (fg+bg). Glamour emits resets
// inside markdown which would clear the card background — this restores
// the card colours after each reset without needing raw ANSI strings.
func keepCardBG(s, bgCode, fgCode string) string {
	return strings.ReplaceAll(s, ansi.ResetStyle, ansi.ResetStyle+fgCode+bgCode)
}

// ─── Main View ────────────────────────────────────────────────────────

func (m *model) View() tea.View {
	// NOT calling rebuildContent here! View is called every frame
	// by bubbletea, and rebuildContent with SetContent → maxLineWidth
	// parses ANSI from ALL lines — infeasible at 60fps.
	// rebuildContent is called explicitly in Update when
	// content changes (handleSend, handleResponse, handleResize, tokenMsg).

	var buf strings.Builder
	w := m.width
	if w < 20 {
		w = 20
	}

	// ── top bar ─────────────────────────────────────────────────────
	indicator := hdrOui.Render("●")
	if m.waiting {
		indicator = m.spinner.View()
	}
	left := fmt.Sprintf("%s %s", hdrOui.Render("ouija"), ftDim.Render(version.Version))
	displayRoot := m.eng.Config().ProjectRoot
	if displayRoot != "" {
		home, _ := os.UserHomeDir()
		if home != "" && strings.HasPrefix(displayRoot, home) {
			displayRoot = "~" + displayRoot[len(home):]
		}
	}
	headerContent := fmt.Sprintf("%s %s  %s %s\n%s",
		indicator, left,
		hdrMeta.Render(displayRoot),
		hdrMeta.Render(fmt.Sprintf("· %s · %s", m.eng.Config().Model, m.eng.Config().Provider)),
		sepLine.Render(strings.Repeat("─", w-2)))
	buf.WriteString(HeaderCard.Render(headerContent, w))

	// ── chat viewport ──────────────────────────────────────────────
	buf.WriteString(m.vp.View())
	buf.WriteByte('\n')

	// ── AI status line (persistent, never scrolls) ─────────────────
	if m.waiting {
		status := "thinking..."
		if m.currentTool != "" {
			status = m.currentTool
		}
		buf.WriteString(StatusCard.Render(
			fmt.Sprintf(" %s %s", m.spinner.View(), status), w))
	}

	// ── slash command list / picker ────────────────────────────────
	if m.picker.Visible {
		buf.WriteString(m.picker.Render(w))
	} else if m.showCmdList {
		var cmdBuf strings.Builder
		for i, sc := range slashCmds {
			sel := " "
			s := ftDim
			if i == m.selCmdIdx {
				sel = "❯"
				s = ftVal
			}
			fmt.Fprintf(&cmdBuf, " %s %s  %s\n", sel, s.Render(sc.cmd), ftDim.Render(sc.desc))
		}
		buf.WriteString(SlashCard.Render(cmdBuf.String(), w))
	}

	// ── Approval prompt or pending edit state ─────────────────────
	if m.approvalReq != nil {
		body := RenderApprovalBox(m.approvalReq, m.toolCallN, m.approvalIdx)
		buf.WriteString(ApprovalCard.Render(body, w))
	} else if m.pendingEdit != nil {
		editMsg := lipgloss.NewStyle().Italic(true).Foreground(lipgloss.Color("208")).Render("  ✎ editing in external editor...\n")
		buf.WriteString(ApprovalCard.Render(editMsg, w))
	} else {
		fmt.Fprintf(&buf, "%s", InputCard.Render(m.input.View(), w))
	}

	// ── status bar ─────────────────────────────────────────────────
	elapsed := time.Since(m.startTime)
	left = ftScope.Render(fmt.Sprintf("%d msg", m.msgCount))
	if m.steps > 0 {
		left += fmt.Sprintf("  %s", ftDim.Render(fmt.Sprintf("%d steps", m.steps)))
	}

	// Budget progress bar — compact visual of the most restrictive ceiling.
	displayTokens := m.tokens
	if m.waiting && m.streamingTokens > displayTokens {
		displayTokens = m.streamingTokens
	}
	if displayTokens > 0 {
		b := m.eng.Config().Budget
		pct, lbl := budgetUsage(displayTokens, m.usd, b.MaxUSD, b.MaxTokens)
		bar := progressBar(8, pct/100)
		left += fmt.Sprintf("  %s  %s", bar, ftScope.Render(fmt.Sprintf("%s%.1f%%", lbl, pct)))
	}

	left += fmt.Sprintf("  %s",
		ftDim.Render(fmt.Sprintf("%dm%ds",
			int(elapsed.Minutes())%60, int(elapsed.Seconds())%60)))
	scrollPct := m.vp.ScrollPercent()
	if scrollPct > 0 && scrollPct < 0.99 {
		left += fmt.Sprintf("  %s", ftScope.Render(fmt.Sprintf("%d%%", int(scrollPct*100))))
	}

	right := ftVal.Render(fmt.Sprintf("%d tok", displayTokens))
	if m.usd > 0 {
		right = fmt.Sprintf("%s  %s", ftVal.Render(fmt.Sprintf("$%.2f", m.usd)), right)
	}
	if len(m.pendingQueue) > 0 {
		right = fmt.Sprintf("%s  %s", ftScope.Render(fmt.Sprintf("(%d queued)", len(m.pendingQueue))), right)
	}
	var shortcuts string
	switch {
	case m.approvalReq != nil:
		// The approval key is scoped to the target (tool\x00path), so reduce it
		// before matching — otherwise the delete hint would never render.
		if baseTool(m.approvalReq.tool) == "delete_file" {
			shortcuts = ftDim.Render("  ←/→ navigate · d delete · n no · e edit · Esc cancel")
		} else {
			shortcuts = ftDim.Render("  ←/→ navigate · y yes · n no · e edit · s session · Esc cancel")
		}
	case m.waiting:
		shortcuts = ftDim.Render("  Esc cancel")
	case len(m.pendingQueue) > 0:
		shortcuts = ftDim.Render("  ↑ edit · Ctrl+Y copy · Esc clear queue · /help")
	default:
		shortcuts = ftDim.Render("  ↑ edit · Ctrl+Y copy · /help")
	}
	right = fmt.Sprintf("%s%s", right, shortcuts)

	leftWidth := lipgloss.Width(left)
	contentWidth := w - 2
	gap := contentWidth - leftWidth - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	statusContent := left + strings.Repeat(" ", gap) + right
	fmt.Fprintf(&buf, "%s", FooterCard.Render(statusContent, w))

	v := tea.NewView(buf.String())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeAllMotion
	v.WindowTitle = "ouija"
	return v
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

	// Animated welcome — rendered once and cached; refreshed on tick.
	buf.WriteString(WelcomeCard.Render(m.welcomeBanner, vpW))

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
			pipePfx := "  " + pipeYou + " "
			contentWidth := msgContentWidth(vpW, pipePfx)
			indent := "     "
			fmt.Fprintf(&msgBuf, "%s%s %s\n", pipePfx, l.ts, roleYou.Render("You"))
			for _, textLine := range wrapLines(l.text, indent, contentWidth) {
				fmt.Fprintf(&msgBuf, "%s%s\n", pipePfx, textLine)
			}
			lineBuf.WriteString(UserCard.Render(msgBuf.String(), vpW))
			msgBuf.Reset()

		case "assistant":
			pipePfx := "  " + pipeOui + " "
			contentWidth := msgContentWidth(vpW, pipePfx)
			fmt.Fprintf(&msgBuf, "%s%s %s\n", pipePfx, l.ts, roleOui.Render("ouija"))
			rendered := renderGlamourFull(l.text, contentWidth)
			for _, line := range strings.Split(rendered, "\n") {
				fmt.Fprintf(&msgBuf, "%s%s\n", pipePfx, line)
			}
			lineBuf.WriteString(AICard.Render(msgBuf.String(), vpW))
			msgBuf.Reset()

		case "codeblock":
			var cbBuf strings.Builder
			if l.lang != "" {
				label := txtDim.Render("── " + l.lang + " ──")
				fmt.Fprintf(&cbBuf, "%s\n", label)
			}
			codeMD := fmt.Sprintf("```%s\n%s\n```", l.lang, l.text)
			rendered := renderGlamour(codeMD)
			rendered = strings.TrimPrefix(rendered, "\n")
			for _, cl := range strings.Split(rendered, "\n") {
				if lipgloss.Width(cl) == 0 {
					continue
				}
				fmt.Fprintf(&cbBuf, "%s\n", cl)
			}
			lineBuf.WriteString(AICard.Render(cbBuf.String(), vpW))

		case "banner":
			if l.text != "" {
				lineBuf.WriteString(BannerCard.Render("    "+l.text, vpW))
			}

		case "trace":
			pipePfx := "  " + pipeTrace + " "
			fmt.Fprintf(&msgBuf, "%s%s %s\n", pipePfx, l.ts, lipgloss.NewStyle().Foreground(lipgloss.Color("243")).Render("⚡ trace"))
			for _, trLine := range strings.Split(l.text, "\n") {
				if trLine != "" {
					colored := colorDiffLine(trLine)
					fmt.Fprintf(&msgBuf, "%s  %s\n", pipePfx, colored)
				}
			}
			lineBuf.WriteString(TraceCard.Render(msgBuf.String(), vpW))
			msgBuf.Reset()

		}

		l.cached = lineBuf.String()
		buf.WriteString(l.cached)
	}

	if m.waiting {
		var msgBuf strings.Builder
		pipePfx := "  " + pipeOui + " "
		contentWidth := msgContentWidth(vpW, pipePfx)
		fmt.Fprintf(&msgBuf, "%s%s %s\n",
			pipePfx, time.Now().Format("15:04"), roleOui.Render("ouija"))

		if m.partialText != "" {
			rendered := renderGlamourFull(m.partialText, contentWidth)
			for _, line := range strings.Split(rendered, "\n") {
				fmt.Fprintf(&msgBuf, "%s%s\n", pipePfx, line)
			}
		} else {
			status := "thinking..."
			if m.currentTool != "" {
				status = m.currentTool
			}
			fmt.Fprintf(&msgBuf, "%s%s\n", pipePfx, status)
		}

		buf.WriteString(AICard.Render(msgBuf.String(), vpW))
	}

	// ── Queue preview inside viewport (scrolls naturally) ──────────
	if len(m.pendingQueue) > 0 {
		var qBuf strings.Builder
		queueHint := txtDim.Render(fmt.Sprintf("● pending: %d · Esc clears", len(m.pendingQueue)))
		fmt.Fprintf(&qBuf, "%s\n", queueHint)
		for i, msg := range m.pendingQueue {
			preview := msg
			if len(preview) > int(float64(vpW)*0.4) {
				preview = preview[:int(float64(vpW)*0.4)-3] + "..."
			}
			fmt.Fprintf(&qBuf, "%s\n", txtDim.Render(fmt.Sprintf("◇ %d: %s", i+1, preview)))
		}
		buf.WriteString(BannerCard.Render(qBuf.String(), vpW))
	}

	m.vp.SetContent(buf.String())

	if m.autoScroll {
		m.vp.GotoBottom()
	}
}

// ─── Welcome banner ───────────────────────────────────────────────────

var sparkleFrames = []string{"◆", "◇", "✦", "✧", "❖", "◈", "✧", "✦", "◇"}

// renderWelcomeBanner builds the animated ouija logo with rainbow cycling.
// shift is the color index offset (driven by hueShift in the model).
func renderWelcomeBanner(shift int, projectRoot string) string {

	var artLines []string
	colorIdx := shift
	for _, line := range ouijaArt {
		var coloredLine strings.Builder
		for _, r := range line {
			if r != ' ' {
				c := bannerColors[colorIdx%len(bannerColors)]
				s := lipgloss.NewStyle().Bold(true).Foreground(c)
				coloredLine.WriteString(s.Render(string(r)))
				colorIdx++
			} else {
				coloredLine.WriteRune(r)
			}
		}
		artLines = append(artLines, coloredLine.String())
	}

	spark := sparkleFrames[shift%len(sparkleFrames)]
	sparkStyle := lipgloss.NewStyle().Bold(true).Foreground(bannerColors[(shift+2)%len(bannerColors)])
	sparkled := sparkStyle.Render(spark)

	content := sparkled + "\n"
	content += strings.Join(artLines, "\n") + "\n"
	content += sparkled + "\n"
	content += "\n"
	content += bannerTag.Render("AI Project Assistant") + "\n"
	content += "\n"

	if projectRoot != "" {
		displayRoot := projectRoot
		home, _ := os.UserHomeDir()
		if home != "" && strings.HasPrefix(projectRoot, home) {
			displayRoot = "~" + projectRoot[len(home):]
		}
		rootStyle := lipgloss.NewStyle().Faint(true).Foreground(lipgloss.Color("60"))
		content += rootStyle.Render("◈ "+displayRoot) + "\n"
	}

	content += bannerAction.Render("✦ Ask · 📁 Read · 🧠 ✦")

	return lipgloss.NewStyle().Render(content)
}

// ─── Budget tracking ────────────────────────────────────────────────

// budgetUsage returns the percentage of the most restrictive budget ceiling
// consumed, and a label prefix ("$" for USD, "" for tokens). Returns 0 ""
// when no budget ceiling is configured (or both ceilings are zero/unlimited).
func budgetUsage(tokens int, usd float64, maxUSD *float64, maxTokens int) (pct float64, label string) {
	if maxUSD != nil && *maxUSD > 0 {
		pct = usd / *maxUSD * 100
		if pct < 0 {
			pct = 0
		}
		return pct, "$"
	}
	if maxTokens > 0 {
		pct = float64(tokens) / float64(maxTokens) * 100
		if pct < 0 {
			pct = 0
		}
		return pct, ""
	}
	return 0, ""
}

// progressBar renders a colored bar showing how much of a budget ceiling
// has been consumed. Returns an empty string when ratio < 1% (too small
// to render meaningfully). The bar uses █ (filled) and ░ (empty) blocks,
// colored by severity: violet (<50%), amber (50-80%), red (>80%).
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

	// Colors: red (>80%), amber (50-80%), violet (<50%).
	hex := "93"
	switch {
	case ratio > 0.8:
		hex = "196"
	case ratio > 0.5:
		hex = "220"
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render(bar)
}

// ─── Word wrapping ────────────────────────────────────────────────────

// wrapLines splits text into wrapped lines, each at most maxWidth wide.
// The first line has no indent; subsequent lines are indented by indent.
func wrapLines(text, indent string, maxWidth int) []string {
	if maxWidth < 8 {
		return []string{text}
	}
	words := strings.Fields(text)
	if len(words) == 0 {
		return []string{text}
	}
	var lines []string
	line := words[0]
	for _, word := range words[1:] {
		candidate := line + " " + word
		if lipgloss.Width(candidate) <= maxWidth {
			line = candidate
		} else {
			lines = append(lines, line)
			line = indent + word
		}
	}
	lines = append(lines, line)
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
				slog.Warn("clipboard", "err", err)
			}
			break
		}
	}
	return m, nil
}
