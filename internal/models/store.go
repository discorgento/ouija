// Package models provides the canonical list of providers and their available
// models. Edit defaultModels() in this file to add/remove providers and models.
// No file I/O — models live in memory only. Safe for concurrent use.
package models

import (
	"maps"
	"slices"
	"sync"
)

// ─── Types ────────────────────────────────────────────────────────────────

// ModelsFile is the in-memory structure of providers and models.
type ModelsFile struct {
	Providers map[string]ProviderDef
}

// ProviderDef describes a provider and the models it supports.
type ProviderDef struct {
	// Label is an optional human-readable name.
	Label string
	// Models is a map of model ID → metadata.
	Models map[string]ModelDef
	// DefaultModel overrides the first model or one marked default:true.
	DefaultModel string
}

// ModelDef describes a single model within a provider.
type ModelDef struct {
	Label         string
	ContextWindow int
	Default       bool
}

// ─── Singleton ────────────────────────────────────────────────────────────

// Load returns the global Store. Safe for concurrent use.
var Load = sync.OnceValue(func() *Store {
	return &Store{data: defaultModels()}
})

// Store holds the canonical models list, immutable after init.
// For testability, construct directly: &Store{data: &ModelsFile{...}}.
type Store struct {
	mu   sync.RWMutex
	data *ModelsFile
}

// ─── Defaults ─────────────────────────────────────────────────────────────

// defaultModels returns the built-in models. Edit this function to add or
// remove providers/models — this is the single source of truth.
func defaultModels() *ModelsFile {
	return &ModelsFile{
		Providers: map[string]ProviderDef{
			"anthropic": {
				Label: "Anthropic (Claude)",
				Models: map[string]ModelDef{
					"claude-opus-4-8":   {Label: "Claude Opus 4", ContextWindow: 200_000, Default: true},
					"claude-sonnet-4-8": {Label: "Claude Sonnet 4", ContextWindow: 200_000},
					"claude-haiku-3-5":  {Label: "Claude Haiku 3.5", ContextWindow: 200_000},
				},
			},
			"openai": {
				Label: "OpenAI (GPT)",
				Models: map[string]ModelDef{
					"gpt-4o":      {Label: "GPT-4o", ContextWindow: 128_000, Default: true},
					"gpt-4o-mini": {Label: "GPT-4o Mini", ContextWindow: 128_000},
					"o3-mini":     {Label: "o3 Mini", ContextWindow: 200_000},
				},
			},
			"openrouter": {
				Label: "OpenRouter (free models)",
				Models: map[string]ModelDef{
					"poolside/laguna-m.1:free":     {Label: "Laguna M.1 (free)"},
					"nvidia/nemotron-3-super:free": {Label: "Nemotron 3 Super (free)"},
					"tencent/hy3-preview:free":     {Label: "Hy3 preview (free)"},
					"qwen/qwen3-coder:free":        {Label: "Qwen 3 Coder (free)"},
					"openrouter/owl-alpha":         {Label: "OWL Alpha (free)"},
					"openai/gpt-oss-120b:free":     {Label: "GPT-OSS 120B (free)"},
					"openrouter/free":              {Label: "OpenRouter Free (fallback)", Default: true},
				},
			},
			"opencode": {
				Label: "OpenCode Zen (free)",
				Models: map[string]ModelDef{
					"big-pickle":             {Label: "Big Pickle (free)", Default: true},
					"deepseek-v4-flash-free": {Label: "DeepSeek V4 Flash (free)"},
					"mimo-v2.5-free":         {Label: "MiMo V2.5 (free)"},
					"north-mini-code-free":   {Label: "North Mini Code (free)"},
					"nemotron-3-ultra-free":  {Label: "Nemotron 3 Ultra (free)"},
				},
			},
			"ollama": {
				Label: "Ollama (local, free)",
				Models: map[string]ModelDef{
					"qwen2.5-coder": {Label: "Qwen 2.5 Coder", Default: true},
					"llama3.2":      {Label: "Llama 3.2"},
					"mistral":       {Label: "Mistral"},
				},
			},
		},
	}
}

// ─── Public API ───────────────────────────────────────────────────────────

// Providers returns the sorted list of provider names.
func (s *Store) Providers() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil {
		return nil
	}
	return slices.Sorted(maps.Keys(s.data.Providers))
}

// ProvidersWithLabels returns provider names and their display labels.
func (s *Store) ProvidersWithLabels() (names []string, labels []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil {
		return nil, nil
	}
	names = slices.Sorted(maps.Keys(s.data.Providers))
	labels = make([]string, len(names))
	for i, name := range names {
		if pd := s.data.Providers[name]; pd.Label != "" {
			labels[i] = pd.Label
		} else {
			labels[i] = name
		}
	}
	return
}

// ProvidersFor returns the provider picker list restricted to the providers
// that actually exist at runtime. `registered` is the registry's name list:
// catalog entries whose provider is not registered are dropped (selecting one
// could only fail with "unknown provider"), and registered providers absent
// from the catalog are appended so they stay reachable. Labels come from the
// catalog when known, else the name itself.
func (s *Store) ProvidersFor(registered []string) (names, labels []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	reg := make(map[string]struct{}, len(registered))
	for _, name := range registered {
		reg[name] = struct{}{}
	}

	var extra []string
	if s.data == nil {
		for name := range reg {
			extra = append(extra, name)
		}
	} else {
		for name := range s.data.Providers {
			if _, ok := reg[name]; ok {
				names = append(names, name)
			}
		}
		for name := range reg {
			if _, ok := s.data.Providers[name]; !ok {
				extra = append(extra, name)
			}
		}
	}

	slices.Sort(names)
	slices.Sort(extra)
	names = append(names, extra...)

	labels = make([]string, len(names))
	for i, name := range names {
		if s.data != nil {
			if pd := s.data.Providers[name]; pd.Label != "" {
				labels[i] = pd.Label
				continue
			}
		}
		labels[i] = name
	}
	return names, labels
}

// Models returns the sorted list of model names for the given provider.
// Returns nil if the provider is unknown.
func (s *Store) Models(provider string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil {
		return nil
	}
	pd, ok := s.data.Providers[provider]
	if !ok {
		return nil
	}
	return slices.Sorted(maps.Keys(pd.Models))
}

// ModelExists reports whether the model exists for the given provider.
func (s *Store) ModelExists(provider, model string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil {
		return false
	}
	pd, ok := s.data.Providers[provider]
	if !ok {
		return false
	}
	_, ok = pd.Models[model]
	return ok
}

// DefaultModel returns the default model for the given provider.
// Precedence: provider.defaultModel > model marked default:true > first model.
// Returns "" if the provider is unknown.
func (s *Store) DefaultModel(provider string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil {
		return ""
	}
	pd, ok := s.data.Providers[provider]
	if !ok {
		return ""
	}

	// 1) Explicit defaultModel.
	if pd.DefaultModel != "" {
		if _, ok := pd.Models[pd.DefaultModel]; ok {
			return pd.DefaultModel
		}
	}

	// 2) Any model marked default:true.
	for name, md := range pd.Models {
		if md.Default {
			return name
		}
	}

	// 3) First model (map iteration is random — any is acceptable).
	for name := range pd.Models {
		return name
	}
	return ""
}

// ModelInfo returns the metadata for a specific model, if it exists.
func (s *Store) ModelInfo(provider, model string) (ModelDef, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.data == nil {
		return ModelDef{}, false
	}
	pd, ok := s.data.Providers[provider]
	if !ok {
		return ModelDef{}, false
	}
	md, ok := pd.Models[model]
	return md, ok
}

// Replace atomically swaps the entire models store. Useful for tests or
// future hot-reload from an external source.
func (s *Store) Replace(data *ModelsFile) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = data
}
