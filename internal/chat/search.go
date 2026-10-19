package chat

import (
	"fmt"
	"regexp"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/discorgento/ouija/internal/theme"
)

// ─── Search (Ctrl+F) ──────────────────────────────────────────────────

var (
	searchHit      = lipgloss.NewStyle().Foreground(theme.Surface).Background(theme.Amber)
	searchHitFocus = lipgloss.NewStyle().Bold(true).Foreground(theme.Surface).Background(theme.Pink)
)

// search is the transcript search state: the query is typed in the input
// box (the draft message waits aside), matches are highlighted in the
// viewport, Enter and ↓ go to the next one, ↑ to the previous.
type search struct {
	active  bool
	draft   string // the message being typed when the search opened
	query   string
	matches int // how many
	current int // 0-based, the focused one
}

// openSearch turns the input box into the search box.
func (m *model) openSearch() (tea.Model, tea.Cmd) {
	m.search = search{active: true, draft: m.input.Value()}
	m.input.Reset()
	m.input.Placeholder = "search the conversation…"
	m.setViewportSize()
	return m, nil
}

// closeSearch clears the highlights and gives the draft back.
func (m *model) closeSearch() (tea.Model, tea.Cmd) {
	draft := m.search.draft
	m.search = search{}
	m.vp.ClearHighlights()
	m.input.Placeholder = inputPlaceholder
	m.input.SetValue(draft)
	m.setViewportSize()
	return m, nil
}

// handleSearchKey handles a key while the search box is open.
func (m *model) handleSearchKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "esc", "escape", "ctrl+f":
		return m.closeSearch()
	case "enter", "down":
		m.stepSearch(1)
		return m, nil
	case "up", "shift+enter":
		m.stepSearch(-1)
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	if q := m.input.Value(); q != m.search.query {
		m.search.query = q
		m.applySearch()
	}
	return m, cmd
}

// applySearch highlights every match of the query in the transcript and
// scrolls to the first one at or below the view.
func (m *model) applySearch() {
	m.vp.ClearHighlights()
	m.search.matches, m.search.current = 0, 0
	if m.search.query == "" {
		return
	}
	re := regexp.MustCompile("(?i)" + regexp.QuoteMeta(m.search.query))
	found := re.FindAllStringIndex(ansi.Strip(m.vp.GetContent()), -1)
	if len(found) == 0 {
		return
	}
	m.autoScroll = false // the developer is reading back
	m.vp.SetHighlights(found)
	m.search.matches = len(found)
}

// stepSearch moves to the next (dir 1) or previous (-1) match.
func (m *model) stepSearch(dir int) {
	if m.search.matches == 0 {
		return
	}
	if dir > 0 {
		m.vp.HighlightNext()
	} else {
		m.vp.HighlightPrevious()
	}
	m.search.current = (m.search.current + dir + m.search.matches) % m.search.matches
}

// searchStatus is the status bar's hint while searching.
func (m *model) searchStatus() string {
	switch {
	case m.search.query == "":
		return "type to search · Esc close"
	case m.search.matches == 0:
		return "no match · Esc close"
	}
	return fmt.Sprintf("%d/%d · Enter/↓ next · ↑ previous · Esc close", m.search.current+1, m.search.matches)
}
