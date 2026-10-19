package chat

import (
	"fmt"
	"strings"

	"github.com/discorgento/ouija/engine"
)

// ─── /model suggestions ───────────────────────────────────────────────────

// suggestedModels merges the active project's `modelSuggestions` (from
// .ouija/config.json, keyed by provider) with the built-in store list.
// Config entries come first so a project pins its own models at the top of the
// picker; duplicates are dropped and IDs the store doesn't know still show (as
// the raw ID) and stay selectable. Both /model surfaces — the picker opened by
// a bare `/model` and the inline list after `/model ` — go through here, so
// they can never drift apart.
func suggestedModels(cfg *engine.Config, storeIDs []string) []string {
	provider := ""
	if cfg != nil {
		provider = cfg.Provider
	}
	return suggestedModelsFor(cfg, provider, storeIDs)
}

// suggestedModelsFor is suggestedModels for any provider: the project's
// modelSuggestions for it first, then the catalog.
func suggestedModelsFor(cfg *engine.Config, provider string, storeIDs []string) []string {
	seen := make(map[string]bool, len(storeIDs)+4)
	out := make([]string, 0, len(storeIDs)+4)
	add := func(ids []string) {
		for _, id := range ids {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	if cfg != nil {
		add(cfg.ModelSuggestions[provider])
	}
	add(storeIDs)
	return out
}

// ─── Picker component — reusable scrollable selection list ────────────

// maxPickerLines is the maximum number of suggestion items shown in the
// picker list. If there are more, a "… and N more" line is shown and the
// list scrolls as the user navigates with up/down.
const maxPickerLines = 10

// Picker is a reusable scrollable selection list component.
// Use it for any feature that needs a selectable list: model picker,
// provider picker, command list, etc.
//
// Usage:
//
//	p := &Picker{}
//	p.Show(items, values, "Models")
//	p.Render(width)  // returns ANSI string
//	p.Height()       // lines the picker takes
//	p.HandleKey("down")
//	p.SelectedValue() // current selection
type Picker struct {
	// Public — set by caller.
	Items   []string // display labels
	Values  []string // actual values (nil = use Items)
	Label   string   // section header (e.g. "Models")
	Visible bool

	// Internal state.
	selected int
	offset   int
}

// Show opens the picker with the given items, values, and label.
func (p *Picker) Show(items, values []string, label string) {
	p.Items = items
	p.Values = values
	p.Label = label
	p.selected = 0
	p.offset = 0
	p.Visible = true
}

// Hide closes the picker and clears state.
func (p *Picker) Hide() {
	p.Visible = false
	p.Items = nil
	p.Values = nil
	p.Label = ""
	p.selected = 0
	p.offset = 0
}

// HandleKey processes a key event for the picker.
// Returns (handled, confirmed) where confirmed is true when Enter is pressed.
func (p *Picker) HandleKey(key string) (handled, confirmed bool) {
	if !p.Visible {
		return false, false
	}

	listLen := len(p.Items)
	if listLen == 0 {
		listLen = 1
	}

	switch key {
	case "down":
		p.selected = (p.selected + 1) % listLen
		if p.selected >= p.offset+maxPickerLines {
			p.offset = p.selected - maxPickerLines + 1
		}
		return true, false
	case "up":
		p.selected = (p.selected - 1 + listLen) % listLen
		if p.selected < p.offset {
			p.offset = p.selected
		}
		return true, false
	case "tab":
		// Tab completes without closing.
		return true, false
	case "enter":
		return true, true
	case "esc", "escape":
		p.Hide()
		return true, false
	}
	return false, false
}

// SelectedValue returns the currently selected value.
// Falls back to Items when Values is nil or out of range.
func (p *Picker) SelectedValue() string {
	if !p.Visible || p.selected < 0 || p.selected >= len(p.Items) {
		return ""
	}
	if p.Values != nil && p.selected < len(p.Values) {
		return p.Values[p.selected]
	}
	return p.Items[p.selected]
}

// Height returns the number of terminal lines the picker occupies,
// including the label, visible items, "… and N more" line, and card
// padding. Used for viewport size calculation.
func (p *Picker) Height() int {
	if !p.Visible || len(p.Items) == 0 {
		return 0 // Render draws nothing for an empty list
	}
	n := len(p.Items)
	if n > maxPickerLines {
		n = maxPickerLines + 1 // +1 for "… and N more" line
	}
	// 4 = label(1) + card padding top(1) + card padding bottom(1) + internal blank(1)
	return 4 + n
}

// Render renders the picker as an ANSI string inside a SlashCard.
func (p *Picker) Render(width int) string {
	if !p.Visible || len(p.Items) == 0 {
		return ""
	}

	var buf strings.Builder
	fmt.Fprintf(&buf, "%s:\n", p.Label)

	total := len(p.Items)
	start := p.offset
	end := start + maxPickerLines
	if end > total {
		end = total
	}
	for i := start; i < end; i++ {
		sel := " "
		sty := ftDim
		if i == p.selected {
			sel = "❯"
			sty = ftVal
		}
		fmt.Fprintf(&buf, " %s %s\n", sel, sty.Render(p.Items[i]))
	}
	if total > end {
		fmt.Fprintf(&buf, "    %s\n", ftDim.Render(fmt.Sprintf("… and %d more", total-end)))
	}
	return SlashCard.Render(buf.String(), width)
}
