package commands

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"charm.land/huh/v2"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9-_]{1,31}$`)

// Generate scaffolds a new totem in the totems/ directory.
//
// Usage: ouija generate totem
func Generate(args []string) error {
	if len(args) < 1 || args[0] != "totem" {
		return fmt.Errorf("usage: ouija generate totem")
	}

	name := ""
	summary := ""
	printBanner()
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Totem name").
				Description("2-32 chars, lowercase, digits, hyphens, underscores").
				Validate(func(s string) error {
					if !validName.MatchString(s) {
						return fmt.Errorf("use 2-32 lowercase chars, digits, hyphens, underscores")
					}
					wd, _ := os.Getwd()
					dir := filepath.Join(wd, "totems", s)
					if _, err := os.Stat(dir); err == nil {
						return fmt.Errorf("totem %q already exists", s)
					}
					return nil
				}).
				Value(&name),
			huh.NewInput().
				Title("Short description").
				Description("One line for ouija help, e.g. \"Generate a report from scout data\"").
				Value(&summary),
		).Title("New totem"),
	).WithTheme(ouijaTheme{}).Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil
		}
		return fmt.Errorf("generate: %w", err)
	}

	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("getwd: %w", err)
	}
	dir := filepath.Join(wd, "totems", name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("totem %q already exists at %s", name, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	pkg := strings.ReplaceAll(strings.ReplaceAll(name, "-", "_"), " ", "_")
	structName := toCamel(name)

	src := fmt.Sprintf(`package %[1]s

import (
	"context"
	"fmt"
	"strings"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/registry"
)

func init() { registry.Register(&%[2]s{}) }

type %[2]s struct{}

func (t *%[2]s) Name() string    { return %[3]q }
func (t *%[2]s) Summary() string { return %[4]q }
func (t *%[2]s) Init(_ context.Context, _ *engine.Engine, _ *engine.Config) error { return nil }
func (t *%[2]s) Run(_ context.Context, eng *engine.Engine, args registry.Args) (any, error) {
	// TODO: implement the totem logic.
	return &result{Name: t.Name(), Task: args.Task}, nil
}
func (t *%[2]s) Cleanup() error { return nil }

type result struct {
	Name string %[5]sjson:"name"%[6]s
	Task string %[5]sjson:"task"%[6]s
}

func (r *result) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Totem %%q executed", r.Name)
	if r.Task != "" {
		fmt.Fprintf(&b, "\n  Task:  %%q", r.Task)
	}
	b.WriteString("\n")
	return b.String()
}
`, pkg, structName, name, summary, "`", "`")

	path := filepath.Join(dir, name+".go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	fmt.Printf("✓ Created totem/%[1]s/%[1]s.go\n", name)

	fmt.Print("◇ Running go generate ./totems/... ")
	if err := runCmd("go", "generate", "./totems/"); err != nil {
		return fmt.Errorf("go generate: %w", err)
	}
	fmt.Println("done")

	fmt.Print("◇ Running go install ./cmd/ouija... ")
	if err := runCmd("go", "install", "./cmd/ouija"); err != nil {
		return fmt.Errorf("go install: %w", err)
	}
	fmt.Println("done")

	fmt.Println()
	fmt.Printf("✓ Totem %q ready!\n", name)
	fmt.Println()
	fmt.Println("  What now?")
	fmt.Printf("  1. Open totems/%[1]s/%[1]s.go\n", name)
	fmt.Printf("  2. Implement the Run() method with your logic\n")
	fmt.Printf("  3. Test: ouija %[1]s\n", name)
	fmt.Println()
	fmt.Println("  See CONTRIBUTING.md for project rules. Check existing totems")
	fmt.Println("  (sticker, doc, task) for implementation examples.")

	return nil
}

// runCmd executes a command and streams stderr on failure.
func runCmd(name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// toCamel converts kebab-case or snake_case to CamelCase.
func toCamel(s string) string {
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '-' || r == '_' })
	for i, p := range parts {
		if len(p) > 0 {
			parts[i] = strings.ToUpper(p[:1]) + p[1:]
		}
	}
	return strings.Join(parts, "")
}
