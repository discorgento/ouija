package chat

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
)

// The project's modelSuggestions must surface in the /model list, pinned before
// the built-in store models, deduplicated, and scoped to the active provider.
func TestSuggestedModels(t *testing.T) {
	store := []string{"claude-opus-4-8", "claude-sonnet-4-8"}
	project := &engine.Config{
		Provider: "anthropic",
		ModelSuggestions: map[string][]string{
			"anthropic": {"claude-project-pinned", "claude-opus-4-8"},
			"openai":    {"gpt-4o"},
		},
	}

	tests := []struct {
		name  string
		cfg   *engine.Config
		store []string
		want  []string
	}{
		{
			name:  "config suggestions first, duplicates dropped",
			cfg:   project,
			store: store,
			want:  []string{"claude-project-pinned", "claude-opus-4-8", "claude-sonnet-4-8"},
		},
		{
			name:  "another provider's suggestions never leak in",
			cfg:   &engine.Config{Provider: "ollama"},
			store: store,
			want:  store,
		},
		{
			name:  "no config section falls back to the store",
			cfg:   &engine.Config{Provider: "anthropic"},
			store: store,
			want:  store,
		},
		{
			name:  "nil config is safe",
			cfg:   nil,
			store: store,
			want:  store,
		},
		{
			name:  "blank and repeated store entries are filtered",
			cfg:   nil,
			store: []string{"", "llama3", "", "llama3"},
			want:  []string{"llama3"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := suggestedModels(tc.cfg, tc.store)
			if !slices.Equal(got, tc.want) {
				t.Errorf("suggestedModels() = %v, want %v", got, tc.want)
			}
		})
	}
}

// An empty list renders nothing, so it must take no room from the viewport.
func TestPickerHeightEmpty(t *testing.T) {
	p := &Picker{}
	p.Show(nil, nil, "Models")
	if h := p.Height(); h != 0 {
		t.Fatalf("empty picker Height = %d, want 0", h)
	}
}

// Wrapping around keeps the selection inside the visible window.
func TestPickerWrapKeepsSelectionVisible(t *testing.T) {
	items := make([]string, 25)
	for i := range items {
		items[i] = fmt.Sprintf("model-%02d", i)
	}
	p := &Picker{}
	p.Show(items, items, "Models")
	p.HandleKey("up") // 0 → 24
	if !strings.Contains(p.Render(80), "model-24") {
		t.Fatal("after wrapping up, the last item is not shown")
	}
	p.HandleKey("down") // 24 → 0
	if !strings.Contains(p.Render(80), "model-00") || p.SelectedValue() != "model-00" {
		t.Fatal("after wrapping down, the first item is not shown")
	}
	if _, confirmed := p.HandleKey("enter"); !confirmed {
		t.Fatal("enter did not confirm")
	}
}

// Picker items come from project configs and provider model lists: an escape
// sequence in one must not reach the terminal, and an ID hiding one is not
// offered at all.
func TestPickerCleansItems(t *testing.T) {
	p := &Picker{}
	p.Show([]string{"gpt\x1b]52;c;ZXZpbA==\x07-4o\nx"}, nil, "Models")
	if out := p.Render(80); strings.Contains(out, "]52;c;ZXZpbA==\x07") || strings.Contains(out, "\x1b]") {
		t.Errorf("Render passed an escape sequence through: %q", out)
	}
	got := suggestedModelsFor(&engine.Config{ModelSuggestions: map[string][]string{
		"openai": {"gpt‮4o", "gpt-4o"},
	}}, "openai", nil)
	if !slices.Equal(got, []string{"gpt-4o"}) {
		t.Errorf("suggestedModelsFor = %q, want the ID with a bidi control dropped", got)
	}
}
