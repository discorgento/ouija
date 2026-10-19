package providers

import (
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
)

// TestKeyless checks the keyless helper the doctor relies on.
func TestKeyless(t *testing.T) {
	if Keyless("") {
		t.Error("Keyless(\"\") = true, want false")
	}
	if Keyless("test-provider-xyz") {
		t.Error("Keyless(unknown) = true, want false")
	}
	// ollama is the only keyless provider today (local server, placeholder key).
	if !Keyless("ollama") {
		t.Error("Keyless(ollama) = false, want true (local, no key)")
	}
	for _, p := range []string{"anthropic", "openai", "openrouter", "opencode"} {
		if Keyless(p) {
			t.Errorf("Keyless(%s) = true, want false (needs a key)", p)
		}
	}
}

// TestKnown checks provider registration lookup.
func TestKnown(t *testing.T) {
	if Known("") {
		t.Error(`Known("") = true, want false`)
	}
	if Known("no-such-provider") {
		t.Error("Known(unknown) = true, want false")
	}
	for _, p := range []string{"anthropic", "ollama", "openai", "openrouter", "opencode"} {
		if !Known(p) {
			t.Errorf("Known(%s) = false, want true", p)
		}
	}
}

// TestBuild checks that a factory receives its API key and returns a non-nil
// provider. The key is passed explicitly, so there is no shared state to reset.
func TestBuild(t *testing.T) {
	p, ok := Build("no-such-provider", "sk-test")
	if ok || p != nil {
		t.Errorf("Build(unknown) = (%v, %v), want (nil, false)", p, ok)
	}

	p, ok = Build("opencode", "sk-test")
	if !ok {
		t.Fatal("Build(opencode) ok = false, want true")
	}
	if p == nil {
		t.Fatal("Build(opencode) = nil provider")
	}
	if p.Name() != "opencode" {
		t.Errorf("provider Name() = %q, want opencode", p.Name())
	}
}

// TestBuild_keyIsThreaded verifies the key reaches the constructed provider —
// the crux of removing the package-level key store.
func TestBuild_keyIsThreaded(t *testing.T) {
	p, ok := Build("opencode", "sk-secret")
	if !ok {
		t.Fatal("Build(opencode) ok = false")
	}
	oc, cast := p.(*OpenAICompat)
	if !cast {
		t.Fatalf("Build(opencode) returned %T, want *OpenAICompat", p)
	}
	if oc.apiKey != "sk-secret" {
		t.Errorf("apiKey = %q, want sk-secret (must be passed to the constructor)", oc.apiKey)
	}
}

// TestRegister_duplicate panics on a duplicate name, catching wiring mistakes.
func TestRegister_duplicate(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("Register(duplicate) did not panic")
		}
	}()
	Register("opencode", func(string) engine.Provider { return nil })
}

// TestNames_isSorted checks the exported Names() helper.
func TestNames_isSorted(t *testing.T) {
	names := Names()
	if len(names) == 0 {
		t.Fatal("Names() returned zero providers")
	}
	for i := 1; i < len(names); i++ {
		if strings.Compare(names[i-1], names[i]) > 0 {
			t.Errorf("Names() not sorted: %v", names)
			break
		}
	}
	// sanity: our built-in providers are present
	for _, want := range []string{"anthropic", "ollama", "openai", "openrouter", "opencode"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Names() missing %q: %v", want, names)
		}
	}
}
