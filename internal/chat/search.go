package chat

import (
	"fmt"
	"regexp"
	"strings"

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
	m.fullTranscript() // search the whole transcript, not the streaming tail
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

// ─── History search (Ctrl+R) ──────────────────────────────────────────

// historyPickerLabel is the picker's label while it searches the messages
// sent before.
const historyPickerLabel = "History"

// openHistorySearch lists the messages sent this session, the latest first,
// filtered as the developer types; Enter puts one back in the input box.
func (m *model) openHistorySearch() (tea.Model, tea.Cmd) {
	if len(m.inputHistory) == 0 {
		return m.toast("no messages sent yet")
	}
	m.histSearch = &search{active: true, draft: m.input.Value()}
	m.input.Reset()
	m.input.Placeholder = "search your messages…"
	m.filterHistory()
	return m, nil
}

// filterHistory shows the sent messages containing the query.
func (m *model) filterHistory() {
	q := strings.ToLower(m.input.Value())
	var items []string
	for i := len(m.inputHistory) - 1; i >= 0; i-- {
		h := m.inputHistory[i]
		if strings.Contains(strings.ToLower(h), q) {
			items = append(items, h)
		}
	}
	if len(items) == 0 {
		m.picker.Hide()
	} else {
		labels := make([]string, len(items))
		for i, it := range items {
			labels[i] = ansi.Truncate(strings.Join(strings.Fields(it), " "), 90, "…")
		}
		m.picker.Show(labels, items, historyPickerLabel)
	}
	m.setViewportSize()
}

// handleHistoryKey handles a key while the history search is open.
func (m *model) handleHistoryKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.Keystroke() {
	case "esc", "escape", "ctrl+r":
		draft := m.histSearch.draft
		m.closeHistorySearch(draft)
		return m, nil
	case "enter", "tab":
		chosen := m.picker.SelectedValue()
		if chosen == "" {
			chosen = m.histSearch.draft
		}
		m.closeHistorySearch(chosen)
		return m, nil
	case "up", "down":
		m.picker.HandleKey(msg.Keystroke())
		return m, nil
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	m.filterHistory()
	return m, cmd
}

// closeHistorySearch ends the history search with text in the input box.
func (m *model) closeHistorySearch(text string) {
	m.histSearch = nil
	m.picker.Hide()
	m.input.Placeholder = inputPlaceholder
	m.input.SetValue(text)
	m.input.CursorEnd()
	m.setViewportSize()
}
