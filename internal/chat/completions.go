package chat

import (
	"slices"
	"strings"

	"github.com/discorgento/ouija/engine/providers"
	"github.com/discorgento/ouija/internal/models"
)

// updateCmdList shows/hides the slash command or argument suggestion list.
// When the input is "/model " or "/provider " it switches to argument mode.
// A list is shown only while it can complete what is typed: once nothing
// matches, it hides, so Enter sends the input as typed — a list left open on
// its first item used to turn `/save notes.md` into `/clear`.
// Returns true when the visible state (showCmdList / picker visibility) changed.
func (m *model) updateCmdList() bool {
	wasCmd, wasPicker := m.showCmdList, m.picker.Visible
	changed := func() bool { return wasCmd != m.showCmdList || wasPicker != m.picker.Visible || m.picker.Visible }

	input := m.input.Value()
	m.showCmdList = false
	// An @mention being typed: the project's files that match it.
	if typed, ok := typedMention(input); ok {
		m.showFilePicker(typed)
		return changed()
	}
	if !strings.HasPrefix(strings.TrimSpace(input), "/") {
		m.picker.Hide()
		return changed()
	}

	parts := strings.Fields(input)
	trailing := strings.HasSuffix(input, " ")
	if len(parts) >= 2 || trailing {
		// Argument mode: the word being typed (empty right after a space).
		typed := ""
		if !trailing {
			typed = parts[len(parts)-1]
		}
		m.picker.Hide()
		switch {
		case parts[0] == "/model" && len(parts)+boolInt(trailing) == 2:
			names := suggestedModels(m.eng.Config(), models.IDs(m.providerModels(m.eng.Config().Provider)))
			m.picker.Show(names, names, modelPickerLabel)
			m.picker.Current = m.eng.Config().Model
		case parts[0] == "/provider" && len(parts)+boolInt(trailing) == 2:
			names, _ := models.Load().ProvidersFor(providers.Names())
			m.picker.Show(names, names, "Providers")
			m.picker.Current = m.eng.Config().Provider
		case parts[0] == "/provider" && len(parts)+boolInt(trailing) == 3:
			// Second step: this provider's models (not the provider list).
			m.showModelPicker(parts[1])
		}
		if typed != "" && !m.pickerFilter(typed) {
			m.picker.Hide() // nothing to complete: Enter sends what was typed
		}
		return changed()
	}

	// The command name itself: list the commands it can still become.
	m.picker.Hide()
	for i, sc := range m.cmds {
		if strings.HasPrefix(sc.cmd, parts[0]) {
			m.showCmdList = true
			m.selCmdIdx = i
			break
		}
	}
	return changed()
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// pickerFilter selects the item whose value is typed, else the first whose
// display text, else value, starts with it (case-insensitive). It reports
// whether one matched.
func (m *model) pickerFilter(typed string) bool {
	if !m.picker.Visible {
		return false
	}
	lower := strings.ToLower(typed)
	if i := slices.Index(m.picker.Values, typed); i >= 0 { // exact id: llama3, not llama3.2
		m.picker.selected = i
		m.picker.offset = max(0, i-maxPickerLines+1)
		return true
	}
	for _, list := range [][]string{m.picker.Items, m.picker.Values} {
		for i, s := range list {
			if strings.HasPrefix(strings.ToLower(s), lower) {
				m.picker.selected = i
				m.picker.offset = max(0, i-maxPickerLines+1)
				return true
			}
		}
	}
	return false
}

// selectCompletion applies the currently selected suggestion to the input.
func (m *model) selectCompletion() {
	if m.picker.Visible {
		val := m.picker.SelectedValue()
		if val == "" {
			return
		}
		switch m.picker.Label {
		case filePickerLabel:
			m.completeMention(val)
			m.picker.Hide()
			return
		case themePickerLabel:
			m.picker.Hide()
			m.input.Reset()
			m.saveTheme(val)
			return
		case sessionsPickerLabel:
			m.picker.Hide()
			m.input.Reset()
			m.resumeSession(val)
			return
		}
		input := m.input.Value()
		parts := strings.Fields(input)
		switch {
		case len(parts) >= 2 && parts[0] == "/provider" && m.picker.Label == modelPickerLabel:
			// Second step of /provider: keep the chosen provider, add the model.
			m.input.SetValue(parts[0] + " " + parts[1] + " " + val)
		case len(parts) >= 1:
			m.input.SetValue(parts[0] + " " + val)
		}
		m.picker.Hide()
		return
	}
	if m.selCmdIdx >= 0 && m.selCmdIdx < len(m.cmds) {
		m.input.SetValue(m.cmds[m.selCmdIdx].cmd)
	}
	m.showCmdList = false
}
