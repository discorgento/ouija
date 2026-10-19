package models

import (
	"testing"
)

// testStore creates a Store with the given provider definitions.
func testStore(t testing.TB, providers map[string]ProviderDef) *Store {
	t.Helper()
	return &Store{data: &ModelsFile{Providers: providers}}
}

func TestProviders(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"anthropic": {Label: "Anthropic", Models: map[string]ModelDef{"claude-opus-4-8": {Default: true}}},
		"ollama":    {Label: "Ollama", Models: map[string]ModelDef{"llama3": {Default: true}}},
	})
	providers := s.Providers()
	if len(providers) != 2 {
		t.Fatalf("expected 2 providers, got %d: %v", len(providers), providers)
	}
	if providers[0] != "anthropic" || providers[1] != "ollama" {
		t.Errorf("expected sorted [anthropic ollama], got %v", providers)
	}
}

func TestProvidersWithLabels(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"z-last":  {Label: "Z Label", Models: map[string]ModelDef{"m1": {Default: true}}},
		"a-first": {Models: map[string]ModelDef{"m1": {Default: true}}},
	})
	names, labels := s.ProvidersWithLabels()
	if len(names) != 2 {
		t.Fatalf("expected 2 providers, got %d", len(names))
	}
	if names[0] != "a-first" || names[1] != "z-last" {
		t.Errorf("expected sorted [a-first z-last], got %v", names)
	}
	if labels[0] != "a-first" {
		t.Errorf("expected label 'a-first' (no explicit label), got %q", labels[0])
	}
	if labels[1] != "Z Label" {
		t.Errorf("expected label 'Z Label', got %q", labels[1])
	}
}

func TestProvidersForFiltersUnregistered(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"anthropic": {Label: "Anthropic", Models: map[string]ModelDef{"m1": {Default: true}}},
		"ghost":     {Label: "Ghost", Models: map[string]ModelDef{"m1": {Default: true}}},
	})
	names, labels := s.ProvidersFor([]string{"anthropic", "brand-new"})
	if len(names) != 2 {
		t.Fatalf("expected 2 providers, got %v", names)
	}
	// Catalog entry without a registered factory is dropped; the registered
	// provider missing from the catalog is appended, labelled by its name.
	if names[0] != "anthropic" || labels[0] != "Anthropic" {
		t.Errorf("first entry = %q (%q), want anthropic (Anthropic)", names[0], labels[0])
	}
	if names[1] != "brand-new" || labels[1] != "brand-new" {
		t.Errorf("second entry = %q (%q), want brand-new (brand-new)", names[1], labels[1])
	}
}

func TestModels(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"test": {Models: map[string]ModelDef{
			"b-model": {Label: "B"},
			"a-model": {Label: "A", Default: true},
		}},
	})
	models := s.Models("test")
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d: %v", len(models), models)
	}
	if models[0] != "a-model" || models[1] != "b-model" {
		t.Errorf("expected sorted [a-model b-model], got %v", models)
	}
}

func TestModelsUnknownProvider(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"test": {Models: map[string]ModelDef{"m1": {Default: true}}},
	})
	if ms := s.Models("nonexistent"); ms != nil {
		t.Errorf("expected nil for unknown provider, got %v", ms)
	}
}

func TestModelExists(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"test": {Models: map[string]ModelDef{"m1": {Default: true}, "m2": {}}},
	})
	if !s.ModelExists("test", "m1") {
		t.Error("expected m1 to exist")
	}
	if s.ModelExists("test", "nonexistent") {
		t.Error("expected nonexistent to not exist")
	}
	if s.ModelExists("unknown", "m1") {
		t.Error("expected no match for unknown provider")
	}
}

func TestModelInfo(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"test": {Models: map[string]ModelDef{
			"m1": {Label: "Model One", ContextWindow: 100_000, Default: true},
		}},
	})
	md, ok := s.ModelInfo("test", "m1")
	if !ok {
		t.Fatal("expected to find m1")
	}
	if md.Label != "Model One" {
		t.Errorf("expected label 'Model One', got %q", md.Label)
	}
	if md.ContextWindow != 100_000 {
		t.Errorf("expected context window 100000, got %d", md.ContextWindow)
	}
	if !md.Default {
		t.Error("expected m1 to be default")
	}

	_, ok = s.ModelInfo("test", "nope")
	if ok {
		t.Error("expected false for unknown model")
	}
}

func TestDefaultModel(t *testing.T) {
	tests := []struct {
		name      string
		providers map[string]ProviderDef
		query     string
		expected  string
	}{
		{
			name: "explicit defaultModel",
			providers: map[string]ProviderDef{
				"p": {DefaultModel: "b", Models: map[string]ModelDef{
					"a": {Label: "A"},
					"b": {Label: "B"},
				}},
			},
			query:    "p",
			expected: "b",
		},
		{
			name: "default:true flag",
			providers: map[string]ProviderDef{
				"p": {Models: map[string]ModelDef{
					"a": {Label: "A", Default: true},
					"b": {Label: "B"},
				}},
			},
			query:    "p",
			expected: "a",
		},
		{
			name: "first model fallback",
			providers: map[string]ProviderDef{
				"p": {Models: map[string]ModelDef{
					"z": {Label: "Z"},
					"a": {Label: "A"},
				}},
			},
			query:    "p",
			expected: "any", // map iteration is random — any non-empty is accepted
		},
		{
			name: "unknown provider",
			providers: map[string]ProviderDef{
				"other": {Models: map[string]ModelDef{"m1": {Default: true}}},
			},
			query:    "p",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := testStore(t, tt.providers)
			def := s.DefaultModel(tt.query)
			if tt.expected == "any" {
				if def == "" {
					t.Fatal("expected a default model, got empty")
				}
				return
			}
			if tt.expected != "" && def == "" {
				t.Fatalf("expected a default model, got empty")
			}
			if tt.expected != "" && def != tt.expected {
				t.Errorf("expected default %q, got %q", tt.expected, def)
			}
			if tt.expected == "" && def != "" {
				t.Errorf("expected empty default, got %q", def)
			}
		})
	}
}

func TestReplace(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"original": {Models: map[string]ModelDef{"m1": {Default: true}}},
	})
	if !s.ModelExists("original", "m1") {
		t.Fatal("expected original/m1 to exist")
	}

	s.Replace(&ModelsFile{Providers: map[string]ProviderDef{
		"reloaded": {Models: map[string]ModelDef{"r1": {Default: true}}},
	}})

	if !s.ModelExists("reloaded", "r1") {
		t.Fatalf("expected reloaded/r1 after Replace, got providers: %v", s.Providers())
	}
	if s.ModelExists("original", "m1") {
		t.Error("original model should not exist after Replace")
	}
}
func TestModelSortOrder(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"test": {Models: map[string]ModelDef{
			"z": {Default: true},
			"a": {},
			"m": {},
		}},
	})
	models := s.Models("test")
	if len(models) < 2 {
		t.Fatal("expected multiple models")
	}
	for i := 1; i < len(models); i++ {
		if models[i-1] > models[i] {
			t.Fatalf("models not sorted: %s > %s", models[i-1], models[i])
		}
	}
}

func TestLoadDefaultModels(t *testing.T) {
	// Load() returns the singleton with defaults — spot-check a few entries.
	s := Load()
	if s == nil {
		t.Fatal("Load() returned nil")
	}

	providers := s.Providers()
	if len(providers) == 0 {
		t.Fatal("expected at least one provider in defaults")
	}

	// Check known providers exist.
	for _, name := range []string{"anthropic", "openai", "openrouter", "ollama"} {
		if !s.ModelExists(name, "") {
			// Just check the provider has at least one model.
			if len(s.Models(name)) == 0 {
				t.Errorf("expected %s to have models in defaults", name)
			}
		}
	}

	// Verify openrouter/free is the default for openrouter.
	def := s.DefaultModel("openrouter")
	if def != "openrouter/free" {
		t.Errorf("expected openrouter/free as default, got %q", def)
	}
}
