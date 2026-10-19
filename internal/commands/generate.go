package commands

import (
	"errors"
	"fmt"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"charm.land/huh/v2"

	"github.com/discorgento/ouija/internal/version"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9-_]{1,31}$`)

// reservedNames are CLI subcommands: `ouija <name>` (or `<bin> mcp|help` on a
// bootstrap host) runs the builtin, never a totem with that name.
var reservedNames = map[string]bool{
	"help": true, "version": true, "setup": true, "init": true, "trust": true, "config": true,
	"learn": true, "generate": true, "chat": true, "mcp": true, "doctor": true,
}

// buildSuffixes are GOOS/GOARCH values: a file named x_<one of these>.go is
// compiled only on that platform (the totem file is <name>.go).
var buildSuffixes = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "hurd": true,
	"illumos": true, "ios": true, "js": true, "linux": true, "netbsd": true, "openbsd": true,
	"plan9": true, "solaris": true, "wasip1": true, "windows": true, "zos": true,
	"386": true, "amd64": true, "arm": true, "arm64": true, "loong64": true, "mips": true,
	"mipsle": true, "mips64": true, "mips64le": true, "ppc64": true, "ppc64le": true,
	"riscv64": true, "s390x": true, "wasm": true,
}

// validateTotemName rejects names the scaffold would turn into a package that
// does not build, is never compiled, or cannot be reached from the CLI.
func validateTotemName(name string) error {
	if !validName.MatchString(name) {
		return fmt.Errorf("use 2-32 lowercase chars, digits, hyphens, underscores")
	}
	pkg := strings.ReplaceAll(name, "-", "_")
	parts := strings.Split(name, "_")
	switch {
	case token.IsKeyword(pkg) || pkg == "main":
		return fmt.Errorf("%q is a Go keyword or main: the package would not build", name)
	case name == "testdata" || strings.HasSuffix(name, "_test"):
		return fmt.Errorf("%q is ignored by the go tool (testdata / _test)", name)
	case len(parts) > 1 && buildSuffixes[parts[len(parts)-1]]:
		return fmt.Errorf("%q ends in a GOOS/GOARCH suffix: the file would build on one platform only", name)
	case reservedNames[name]:
		return fmt.Errorf("%q is an ouija subcommand: `ouija %s` would never reach the totem", name, name)
	}
	return nil
}

// Generate scaffolds a new totem.
//
// Usage: ouija generate totem [--external [dir]]
//
// Without --external it adds a totem to this repo's totems/ directory. With
// --external it scaffolds a standalone Go module (go.mod + totem + cmd host)
// that imports the public bootstrap/engine/registry packages and builds its
// own binary — for agents you keep in your own repo.
func Generate(args []string) error {
	if len(args) < 1 || args[0] != "totem" {
		return fmt.Errorf("usage: ouija generate totem [--external [dir]]")
	}
	external := false
	target := ""
	for _, a := range args[1:] {
		switch {
		case a == "--external":
			external = true
		case strings.HasPrefix(a, "-"):
			return fmt.Errorf("unknown flag: %s", a)
		default:
			target = a
		}
	}

	name := ""
	summary := ""
	printBanner()
	title := "New totem"
	if external {
		title = "New external totem (standalone module)"
	}
	err := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Totem name").
				Description("2-32 chars, lowercase, digits, hyphens, underscores").
				Validate(func(s string) error {
					if err := validateTotemName(s); err != nil {
						return err
					}
					wd, _ := os.Getwd()
					base := wd
					if external && target != "" {
						base = target
					}
					dir := filepath.Join(base, "totems", s)
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
		).Title(title),
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
	if external {
		return scaffoldExternal(wd, target, name, summary)
	}
	return scaffoldInternal(wd, name, summary)
}

// internalTotemSource is the Go source of a new totem inside this repo.
func internalTotemSource(name, summary string) string {
	pkg := strings.ReplaceAll(strings.ReplaceAll(name, "-", "_"), " ", "_")
	structName := toCamel(name)

	return fmt.Sprintf(`package %[1]s

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
func (t *%[2]s) Run(_ context.Context, eng *engine.Engine, args registry.Args) (any, error) {
	// TODO: implement the totem logic.
	return &result{Name: t.Name(), Task: args.Task}, nil
}

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
}

// scaffoldInternal adds a totem to this repo and rebuilds the CLI.
func scaffoldInternal(wd, name, summary string) error {
	dir := filepath.Join(wd, "totems", name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("totem %q already exists at %s", name, dir)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}

	src := internalTotemSource(name, summary)

	path := filepath.Join(dir, name+".go")
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		return fmt.Errorf("write: %w", err)
	}

	fmt.Printf("✓ Created totems/%[1]s/%[1]s.go\n", name)

	// A failed step takes the totem back out: left behind, it (and the
	// imports_gen.go line for it) would break the build of the whole repo.
	rollback := func(step string, err error) error {
		os.RemoveAll(dir)
		_ = runCmd("", "go", "generate", "./totems/")
		return fmt.Errorf("%s: %w (totems/%s was removed)", step, err, name)
	}

	fmt.Print("◇ Running go generate ./totems/... ")
	if err := runCmd("", "go", "generate", "./totems/"); err != nil {
		return rollback("go generate", err)
	}
	fmt.Println("done")

	fmt.Print("◇ Running go install ./cmd/ouija... ")
	if err := runCmd("", "go", "install", "./cmd/ouija"); err != nil {
		return rollback("go install", err)
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

// ouijaRequire is the go.mod require line for the ouija release this binary
// is, or nothing for a development build (0.0.0): pinning v1.0.0, which did
// not exist, made `go mod tidy` fail; without a line, tidy takes the latest.
func ouijaRequire(v string) string {
	if v == "" || v == "0.0.0" || strings.Contains(v, "-") {
		return ""
	}
	return "\nrequire github.com/discorgento/ouija v" + strings.TrimPrefix(v, "v") + "\n"
}

// scaffoldExternal writes a standalone module, then resolves deps and builds.
func scaffoldExternal(wd, target, name, summary string) error {
	base, err := writeExternalModule(wd, target, name, summary)
	if err != nil {
		return err
	}
	bin := strings.ReplaceAll(name, "_", "-")
	module := "github.com/you/" + name

	fmt.Print("◇ Running go mod tidy... ")
	if err := runCmd(base, "go", "mod", "tidy"); err != nil {
		fmt.Println("skipped (is github.com/discorgento/ouija reachable?)")
		fmt.Println("  → run `go mod tidy` yourself once the module is fetchable")
	} else {
		fmt.Println("done")
		fmt.Print("◇ Building ./" + bin + "... ")
		if err := runCmd(base, "go", "build", "-o", bin, "./cmd/"+bin); err != nil {
			return fmt.Errorf("go build: %w", err)
		}
		fmt.Println("done")
	}

	fmt.Println()
	fmt.Printf("✓ External totem %q ready!\n", name)
	fmt.Println()
	fmt.Println("  What now?")
	fmt.Printf("  1. Set your module path in go.mod (currently %s)\n", module)
	fmt.Printf("  2. Implement Run() in totems/%s/%s.go\n", name, name)
	fmt.Printf("  3. Run: ./%s %s \"...\" · ./%s mcp\n", bin, name, bin)

	return nil
}

// writeExternalModule writes the module skeleton (go.mod, totem, cmd host,
// README, .gitignore) into base and returns the directory it wrote to.
func writeExternalModule(wd, target, name, summary string) (string, error) {
	base := wd
	if target != "" {
		base = target
	}
	if _, err := os.Stat(filepath.Join(base, "go.mod")); err == nil {
		return "", fmt.Errorf("%s already contains a go.mod — run in an empty directory", base)
	}
	module := "github.com/you/" + name
	pkg := strings.ReplaceAll(name, "-", "_")
	structName := toCamel(name)
	bin := strings.ReplaceAll(name, "_", "-")

	if err := os.MkdirAll(filepath.Join(base, "totems", name), 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(base, "cmd", bin), 0o755); err != nil {
		return "", fmt.Errorf("mkdir: %w", err)
	}

	files := map[string]string{
		"go.mod": fmt.Sprintf("module %s\n\ngo 1.26\n%s", module, ouijaRequire(version.Version)),

		filepath.Join("totems", name, name+".go"): fmt.Sprintf(`package %[1]s

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

// Run is your agent: the engine comes with tools, budget, sandbox and trace
// ready. eng.Run executes one ReAct pass; call it as many times as your flow
// needs — sequencing is your Go code, not a prompt.
func (t *%[2]s) Run(ctx context.Context, eng *engine.Engine, args registry.Args) (any, error) {
	res, err := eng.Run(ctx, engine.RunRequest{Label: %[3]q, Prompt: args.Task})
	if err != nil {
		return nil, err
	}
	return &result{Name: t.Name(), Text: res.Text}, nil
}

type result struct {
	Name string %[5]sjson:"name"%[6]s
	Text string %[5]sjson:"text"%[6]s
}

func (r *result) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Totem %%q result:\n\n%%s", r.Name, r.Text)
	b.WriteString("\n")
	return b.String()
}
`, pkg, structName, name, summary, "`", "`"),

		filepath.Join("cmd", bin, "main.go"): fmt.Sprintf(`// %[1]s — agents on the ouija engine, in your repo.
package main

import (
	"os"

	"github.com/discorgento/ouija/bootstrap"

	_ "%[2]s/totems/%[3]s"
)

// bootstrap.Main is the whole command line: list the totems, run one
// (%[1]s <totem> [task] [--set key=value]… [--json]), or serve them all over
// MCP (%[1]s mcp). Same config as the ouija CLI (~/.config/ouija/config.json).
func main() { os.Exit(bootstrap.Main(bootstrap.WithName(%[1]q))) }
`, bin, module, name),

		"README.md": fmt.Sprintf(`# %[1]s

Agent totems on the [ouija](https://github.com/discorgento/ouija) engine —
budget, sandbox, tracing and MCP inherited; the flow is your Go code.

## Build

`+"```sh"+`
go mod tidy
go build -o %[2]s ./cmd/%[2]s
`+"```"+`

## Use

`+"```sh"+`
./%[2]s              # list totems
./%[2]s %[1]s "task description"
./%[2]s %[1]s "task" --set key=value  # named inputs (see InputSchema)
./%[2]s %[1]s "task" --json           # raw JSON out
./%[2]s mcp          # %[2]s_%[1]s tools for Claude Code / Cursor / OpenCode
`+"```"+`

Config comes from the shared ouija config — run `+"`ouija setup`"+` once, or
drop a `+"`.ouija/config.json`"+` in the project you run from.

## Building against a local ouija checkout

To try unreleased ouija changes, point the module at your clone, then tidy:

`+"```sh"+`
go mod edit -replace github.com/discorgento/ouija=/path/to/ouija
go mod tidy
`+"```"+`
`, name, bin),

		// Anchored: a bare `<bin>` pattern would also ignore cmd/<bin>/,
		// the host's source directory.
		".gitignore": "/" + bin + "\n/dist/\n",
	}

	for rel, content := range files {
		if err := os.WriteFile(filepath.Join(base, rel), []byte(content), 0o644); err != nil {
			return "", fmt.Errorf("write %s: %w", rel, err)
		}
		fmt.Printf("✓ Created %s\n", rel)
	}
	return base, nil
}

// runCmd executes a command in dir (current directory when empty) and streams
// stderr on failure.
func runCmd(dir, name string, arg ...string) error {
	cmd := exec.Command(name, arg...)
	cmd.Dir = dir
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
