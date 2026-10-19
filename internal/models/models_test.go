package models

import (
	"testing"
)

// testStore creates a Store with the given provider definitions.
func testStore(t testing.TB, providers map[string]ProviderDef) *Store {
	t.Helper()
	return &Store{data: &ModelsFile{Providers: providers}}
}

func TestProvidersForFiltersUnregistered(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"anthropic": {Label: "Anthropic", Models: map[string]ModelDef{"m1": {}}},
		"ghost":     {Label: "Ghost", Models: map[string]ModelDef{"m1": {}}},
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
			"a-model": {Label: "A"},
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
		"test": {Models: map[string]ModelDef{"m1": {}}},
	})
	if ms := s.Models("nonexistent"); ms != nil {
		t.Errorf("expected nil for unknown provider, got %v", ms)
	}
}

func TestModelInfo(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"test": {Models: map[string]ModelDef{
			"m1": {Label: "Model One", ContextWindow: 100_000},
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
	_, ok = s.ModelInfo("test", "nope")
	if ok {
		t.Error("expected false for unknown model")
	}
}

func TestModelSortOrder(t *testing.T) {
	s := testStore(t, map[string]ProviderDef{
		"test": {Models: map[string]ModelDef{
			"z": {},
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

// The built-in catalog is the offline fallback: every listed provider has at
// least one model.
func TestBuiltinCatalog(t *testing.T) {
	s := Load()
	for _, name := range []string{"anthropic", "openai", "openrouter", "opencode", "ollama"} {
		if len(s.Models(name)) == 0 {
			t.Errorf("built-in catalog has no models for %s", name)
		}
	}
}
