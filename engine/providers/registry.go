package providers

import (
	"fmt"
	"sort"

	"github.com/discorgento/ouija/engine"
)

// registry maps a provider name to its constructor. Constructors take the API
// key explicitly, so there is no package-level mutable key store: the caller
// (app) resolves the key from config and passes it in. This keeps provider
// construction pure and safe to call from tests in parallel.
var registry = map[string]func(apiKey string) engine.Provider{}

// Register makes a provider constructor available by name.
// Called from init() in each provider file.
func Register(name string, factory func(apiKey string) engine.Provider) {
	if _, ok := registry[name]; ok {
		panic(fmt.Sprintf("provider %q already registered", name))
	}
	registry[name] = factory
}

// Build returns a new provider instance by name, wired with apiKey.
// Returns nil, false when the name is unknown.
func Build(name, apiKey string) (engine.Provider, bool) {
	fn, ok := registry[name]
	if !ok {
		return nil, false
	}
	return fn(apiKey), true
}

// Known reports whether a provider name is registered.
func Known(name string) bool {
	_, ok := registry[name]
	return ok
}

// Names returns all registered provider names in alphabetical order.
func Names() []string {
	n := make([]string, 0, len(registry))
	for name := range registry {
		n = append(n, name)
	}
	sort.Strings(n)
	return n
}

// Keyless reports whether a provider works without an API key. Only local
// providers qualify today (Ollama talks to a local server and supplies a
// placeholder key of its own).
func Keyless(name string) bool {
	return name == "ollama"
}
