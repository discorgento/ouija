package commands

import (
	"os"
	"strings"
	"testing"
)

func TestValidName(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want bool
	}{
		{"simple word", "report", true},
		{"kebab-case", "my-totem", true},
		{"snake_case", "my_totem", true},
		{"digit after the first char", "totem2", true},
		{"max length (32)", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", true},
		{"single char rejected", "a", false},
		{"33 chars rejected", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", false},
		{"uppercase rejected", "Report", false},
		{"leading digit rejected", "2totem", false},
		{"space rejected", "my totem", false},
		{"dot rejected", "my.totem", false},
		{"empty rejected", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validName.MatchString(tt.in); got != tt.want {
				t.Errorf("validName.MatchString(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestToCamel(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"report", "Report"},
		{"my-totem", "MyTotem"},
		{"my_totem", "MyTotem"},
		{"my-mixed_case", "MyMixedCase"},
		{"", ""},
	}

	for _, tt := range tests {
		if got := toCamel(tt.in); got != tt.want {
			t.Errorf("toCamel(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestGenerate_RequiresTotemSubcommand(t *testing.T) {
	for _, args := range [][]string{nil, {}, {"two"}, {"generate"}, {"TOTEM"}} {
		if err := Generate(args); err == nil {
			t.Errorf("Generate(%v) error = nil, want a usage error", args)
		}
	}
}

// The scaffold template is a string literal, so the compiler cannot see the
// module path inside it. This guards it against a module rename: a stale path
// here would generate a brand-new totem that does not build.
func TestScaffoldTemplateUsesCurrentModulePath(t *testing.T) {
	src, err := os.ReadFile("generate.go")
	if err != nil {
		t.Fatalf("read generate.go: %v", err)
	}
	text := string(src)

	for _, want := range []string{
		`"github.com/discorgento/ouija/internal/engine"`,
		`"github.com/discorgento/ouija/internal/registry"`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("scaffold template does not import %s", want)
		}
	}
	if strings.Contains(text, `"ouija/`) {
		t.Error(`scaffold template still references the old module path "ouija/..."`)
	}
}
