package totems

import (
	"testing"

	"github.com/discorgento/ouija/registry"
)

// Importing this package pulls in imports_gen.go, whose blank imports run each
// totem's init(). This is therefore the smoke test that `go generate ./totems/`
// is up to date and every totem is well-formed.
func TestRegisteredTotemsAreWellFormed(t *testing.T) {
	list := registry.List()
	if len(list) < 3 {
		t.Fatalf("registry has %d totems, want at least the 3 built-ins (task, doc, sticker)", len(list))
	}

	seen := make(map[string]bool, len(list))
	for _, tt := range list {
		name := tt.Name()
		if name == "" {
			t.Fatal("a registered totem has an empty name")
		}
		if tt.Summary() == "" {
			t.Errorf("totem %q has an empty summary (it would show blank in `ouija help`)", name)
		}
		if seen[name] {
			t.Errorf("totem %q is registered twice", name)
		}
		seen[name] = true
	}

	for _, want := range []string{"task", "doc", "sticker"} {
		if _, ok := registry.Get(want); !ok {
			t.Errorf("built-in totem %q is not registered — is imports_gen.go stale? run: go generate ./totems/", want)
		}
	}
}
