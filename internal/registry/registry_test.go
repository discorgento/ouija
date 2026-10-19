package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/discorgento/ouija/internal/engine"
)

type fakeTotem struct{ name, summary string }

func (f *fakeTotem) Name() string    { return f.name }
func (f *fakeTotem) Summary() string { return f.summary }
func (f *fakeTotem) Init(context.Context, *engine.Engine, *engine.Config) error {
	return nil
}
func (f *fakeTotem) Run(context.Context, *engine.Engine, Args) (any, error) { return nil, nil }
func (f *fakeTotem) Cleanup() error                                         { return nil }

func TestRegisterAndGet(t *testing.T) {
	const name = "test-register-and-get"
	Register(&fakeTotem{name: name, summary: "a test totem"})

	got, ok := Get(name)
	if !ok {
		t.Fatalf("Get(%q) = false after Register", name)
	}
	if got.Summary() != "a test totem" {
		t.Errorf("Summary() = %q, want a test totem", got.Summary())
	}

	if _, ok := Get("test-does-not-exist"); ok {
		t.Error("Get(unknown) = true, want false")
	}
}

// Registering the same name twice must not duplicate it in List(); the name
// order is user-visible through Help().
func TestRegister_DuplicateKeepsSingleEntry(t *testing.T) {
	const name = "test-duplicate"
	Register(&fakeTotem{name: name, summary: "first"})
	Register(&fakeTotem{name: name, summary: "second"})

	count := 0
	for _, tt := range List() {
		if tt.Name() == name {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%q appears %d times in List(), want 1", name, count)
	}
	got, ok := Get(name)
	if !ok {
		t.Fatalf("Get(%q) = false", name)
	}
	if got.Summary() != "second" {
		t.Errorf("Get() returned the stale totem: summary = %q, want second", got.Summary())
	}
}

func TestList_SortedByName(t *testing.T) {
	Register(&fakeTotem{name: "zzz-test-sort", summary: "z"})
	Register(&fakeTotem{name: "aaa-test-sort", summary: "a"})

	list := List()
	for i := 1; i < len(list); i++ {
		if list[i-1].Name() > list[i].Name() {
			t.Fatalf("List() not sorted: %q before %q", list[i-1].Name(), list[i].Name())
		}
	}
}

// Help() is the binary's front door: it must list every registered totem and
// advertise the real flag names (regression: it used to advertise --trace,
// which does not exist — the flag is --trace-file).
func TestHelp_ListsTotemsAndFlags(t *testing.T) {
	Register(&fakeTotem{name: "test-help-listing", summary: "shows up in help"})

	help := Help()
	if !strings.Contains(help, "test-help-listing") {
		t.Error("Help() does not list a registered totem")
	}
	if !strings.Contains(help, "shows up in help") {
		t.Error("Help() does not show the totem summary")
	}
	for _, want := range []string{"--json", "--trace-file", "ouija init", "ouija chat"} {
		if !strings.Contains(help, want) {
			t.Errorf("Help() is missing %q", want)
		}
	}
	if strings.Contains(help, "--trace ") {
		t.Error("Help() advertises --trace, which is not a real flag")
	}
}
