package chat

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/discorgento/ouija/internal/theme"
)

// inputPlaceholder is the input box's hint (the search box has its own).
const inputPlaceholder = "Ask about the project… (/ for commands)"

type paddedInput struct {
	textarea.Model
}

func newPaddedInput() paddedInput {
	ta := textarea.New()
	ta.Placeholder = inputPlaceholder
	// A ❯ on the first line; continuation lines line up under the text.
	prompt := lipgloss.NewStyle().Bold(true).Foreground(theme.Pink).Render("❯ ")
	ta.SetPromptFunc(2, func(info textarea.PromptInfo) string {
		if info.LineNumber == 0 {
			return prompt
		}
		return "  "
	})
	ta.SetWidth(40)
	ta.SetHeight(1)
	ta.MaxHeight = 4
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.KeyMap.InsertNewline = key.NewBinding()
	ta.KeyMap.Paste = key.NewBinding(key.WithKeys("ctrl+v"))

	s := ta.Styles()
	// Keep Base empty — external card wraps View() with background + padding.
	// Only clear per-line backgrounds so they don't override the card.
	s.Focused.CursorLine = s.Focused.CursorLine.Background(lipgloss.NoColor{})
	s.Focused.Text = s.Focused.Text.Background(lipgloss.NoColor{})
	s.Focused.EndOfBuffer = s.Focused.EndOfBuffer.Background(lipgloss.NoColor{})
	s.Focused.Placeholder = s.Focused.Placeholder.Foreground(theme.Violet)
	ta.SetStyles(s)

	ta.Focus()
	return paddedInput{Model: ta}
}

func (p paddedInput) Update(msg tea.Msg) (paddedInput, tea.Cmd) {
	var cmd tea.Cmd
	p.Model, cmd = p.Model.Update(msg)
	return p, cmd
}
