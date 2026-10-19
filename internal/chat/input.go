package chat

import (
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

type paddedInput struct {
	textarea.Model
}

func newPaddedInput() paddedInput {
	ta := textarea.New()
	ta.Placeholder = "Type a message…"
	ta.Prompt = ""
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
	s.Focused.Placeholder = s.Focused.Placeholder.Foreground(lipgloss.Color("99"))
	ta.SetStyles(s)

	ta.Focus()
	return paddedInput{Model: ta}
}

func (p paddedInput) Update(msg tea.Msg) (paddedInput, tea.Cmd) {
	var cmd tea.Cmd
	p.Model, cmd = p.Model.Update(msg)
	return p, cmd
}
