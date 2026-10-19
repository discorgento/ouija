package chat

import (
	"context"
	"iter"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
)

// switchEngine is the slice of EngineAPI /provider touches.
type switchEngine struct {
	cfg      *engine.Config
	provider string
	model    string
}

func (f *switchEngine) Chat(context.Context, engine.RunRequest) (string, []engine.Message, error) {
	return "", nil, nil
}
func (f *switchEngine) Config() *engine.Config                     { return f.cfg }
func (f *switchEngine) Spent() (int, int, float64)                 { return 0, 0, 0 }
func (f *switchEngine) Tracer() *engine.Tracer                     { return nil }
func (f *switchEngine) PermissionConfig() *engine.PermissionConfig { return nil }
func (f *switchEngine) ResetApprovals()                            {}
func (f *switchEngine) SetModel(name string)                       { f.model = name; f.cfg.Model = name }
func (f *switchEngine) SetProvider(p engine.Provider) {
	f.provider = p.Name()
	f.cfg.Provider = p.Name()
}
func (f *switchEngine) SaveConfigAt(string) error { return nil }

type namedProvider struct{ name string }

func (p namedProvider) Name() string                      { return p.name }
func (p namedProvider) Capabilities() engine.Capabilities { return engine.Capabilities{} }
func (p namedProvider) Chat(context.Context, engine.ChatRequest) iter.Seq2[engine.StreamEvent, error] {
	return func(func(engine.StreamEvent, error) bool) {}
}

// /provider never picks a model for the developer: naming a provider opens
// its model list, and the switch happens only once a model is chosen.
func TestProviderSwitchMakesTheDeveloperPickTheModel(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir()) // never the developer's model cache
	eng := &switchEngine{cfg: &engine.Config{Provider: "ollama", Model: "llama3"}}
	m := newModel(eng, func(name string) (engine.Provider, error) { return namedProvider{name}, nil }, "", "")

	m.input.SetValue("/provider anthropic")
	m.handleSend()
	if eng.provider != "" || eng.model != "" {
		t.Fatalf("switched to %q/%q before a model was chosen", eng.provider, eng.model)
	}
	if !m.picker.Visible || m.picker.Label != modelPickerLabel {
		t.Fatalf("picker visible=%v label=%q; want the model list", m.picker.Visible, m.picker.Label)
	}
	if got := m.input.Value(); got != "/provider anthropic " {
		t.Fatalf("input = %q; want the provider kept for the second step", got)
	}

	chosen := m.picker.SelectedValue()
	m.selectCompletion()
	if got := m.input.Value(); got != "/provider anthropic "+chosen {
		t.Fatalf("input after picking = %q; want provider and model", got)
	}
	m.handleSend()
	if eng.provider != "anthropic" || eng.model != chosen || !strings.HasPrefix(chosen, "claude-") {
		t.Fatalf("switched to %q/%q; want anthropic/%s", eng.provider, eng.model, chosen)
	}
}
