package chat

import (
	"strings"

	"github.com/discorgento/ouija/internal/engine/providers"
	"github.com/discorgento/ouija/internal/models"
)

// updateCmdList shows/hides the slash command or argument suggestion list.
// When the input is "/model " or "/provider " it switches to argument mode.
// Returns true when the visible state (showCmdList / picker visibility) changed.
func (m *model) updateCmdList() bool {
	input := m.input.Value()
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		changed := m.showCmdList || m.picker.Visible
		m.showCmdList = false
		m.picker.Hide()
		return changed
	}

	// Check for argument completion mode: /model […], /provider […], etc.
	parts := strings.Fields(input)
	hasSpaceAfterCmd := len(parts) >= 2 || strings.HasSuffix(input, " ")
	if hasSpaceAfterCmd {
		baseCmd := parts[0]
		typed := ""
		if len(parts) >= 2 {
			typed = parts[len(parts)-1]
		}

		switch baseCmd {
		case "/model":
			modelNames := suggestedModels(m.eng.Config(), models.Load().Models(m.eng.Config().Provider))
			m.picker.Show(modelNames, modelNames, "Models")
			if typed != "" {
				m.pickerFilter(typed)
			}
			return true
		case "/provider":
			names, _ := models.Load().ProvidersFor(providers.Names())
			m.picker.Show(names, names, "Providers")
			if typed != "" {
				m.pickerFilter(typed)
			}
			return true
		}
	}

	// Default: show command list (not using picker — it's small and fixed).
	wasPicker := m.picker.Visible
	m.picker.Hide()
	m.showCmdList = true
	m.selCmdIdx = 0
	for i, sc := range slashCmds {
		if strings.HasPrefix(sc.cmd, trimmed) {
			m.selCmdIdx = i
			break
		}
	}
	return !wasPicker // true if we transitioned from picker to cmd list or were in neither
}

// pickerFilter filters the picker items by the typed prefix.
// First tries display text, then actual values (case-insensitive).
func (m *model) pickerFilter(typed string) {
	if typed == "" || !m.picker.Visible {
		return
	}
	items := m.picker.Items
	values := m.picker.Values
	lower := strings.ToLower(typed)

	// Try matching display text first.
	for i, s := range items {
		if strings.HasPrefix(strings.ToLower(s), lower) {
			m.picker.selected = i
			m.picker.offset = 0
			return
		}
	}
	// Try matching actual values.
	for i, v := range values {
		if strings.HasPrefix(strings.ToLower(v), lower) {
			m.picker.selected = i
			m.picker.offset = 0
			return
		}
	}
}

// selectCompletion applies the currently selected suggestion to the input.
func (m *model) selectCompletion() {
	if m.picker.Visible {
		val := m.picker.SelectedValue()
		if val == "" {
			return
		}
		input := m.input.Value()
		parts := strings.Fields(input)
		if len(parts) >= 1 {
			m.input.SetValue(parts[0] + " " + val)
		}
		m.picker.Hide()
		return
	}
	if m.selCmdIdx >= 0 && m.selCmdIdx < len(slashCmds) {
		m.input.SetValue(slashCmds[m.selCmdIdx].cmd)
	}
	m.showCmdList = false
}
