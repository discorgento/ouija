package chat

import (
	"slices"
	"testing"

	"github.com/discorgento/ouija/internal/engine"
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
