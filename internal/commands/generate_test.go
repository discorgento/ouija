package commands

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteExternalModule(t *testing.T) {
	dir := t.TempDir()
	base, err := writeExternalModule(dir, "", "my-agent", "Example external agent")
	if err != nil {
		t.Fatal(err)
	}
	if base != dir {
		t.Fatalf("base = %q, want %q", base, dir)
	}

	for _, rel := range []string{
		"go.mod",
		filepath.Join("totems", "my-agent", "my-agent.go"),
		filepath.Join("cmd", "my-agent", "main.go"),
		"README.md",
		".gitignore",
	} {
		if _, err := os.Stat(filepath.Join(dir, rel)); err != nil {
			t.Errorf("missing %s: %v", rel, err)
		}
	}

	gomod, _ := os.ReadFile(filepath.Join(dir, "go.mod"))
	if !strings.Contains(string(gomod), "module github.com/you/my-agent") {
		t.Errorf("go.mod lacks module line:\n%s", gomod)
	}
	// A development build pins nothing (tidy takes the latest release); a
	// release build pins itself — see TestOuijaRequire.
	if strings.Contains(string(gomod), "v1.0.0") {
		t.Errorf("go.mod pins a version that does not exist:\n%s", gomod)
	}

	totem, _ := os.ReadFile(filepath.Join(dir, "totems", "my-agent", "my-agent.go"))
	if !strings.Contains(string(totem), "registry.Register(&MyAgent{})") {
		t.Errorf("totem lacks register call:\n%s", totem)
	}
	if strings.Contains(string(totem), "TODO") {
		t.Errorf("external totem should not be a TODO stub:\n%s", totem)
	}

	// The binary is ignored only at the root: a bare pattern would also hide
	// cmd/my-agent/, the host's source.
	gi, _ := os.ReadFile(filepath.Join(dir, ".gitignore"))
	for _, line := range strings.Split(strings.TrimSpace(string(gi)), "\n") {
		if line == "my-agent" {
			t.Errorf(".gitignore has the bare pattern %q, which ignores cmd/my-agent/", line)
		}
	}
	if !strings.Contains(string(gi), "/my-agent") {
		t.Errorf(".gitignore does not ignore the built binary:\n%s", gi)
	}

	host, _ := os.ReadFile(filepath.Join(dir, "cmd", "my-agent", "main.go"))
	for _, want := range []string{
		"bootstrap.Main",
		`bootstrap.WithName("my-agent")`,
		`_ "github.com/you/my-agent/totems/my-agent"`,
	} {
		if !strings.Contains(string(host), want) {
			t.Errorf("host lacks %q", want)
		}
	}
}

func TestWriteExternalModuleRefusesExistingGoMod(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := writeExternalModule(dir, "", "my-agent", "s"); err == nil {
		t.Fatal("want error on existing go.mod")
	}
}

func TestWriteExternalModuleCustomDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agents")
	if _, err := writeExternalModule("", dir, "guard", "Guard agent"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "cmd", "guard", "main.go")); err != nil {
		t.Fatalf("custom dir not used: %v", err)
	}
}

func TestValidateTotemName(t *testing.T) {
	for name, ok := range map[string]bool{
		"report": true, "my-totem": true, "data_sync": true,
		"go": false, "type": false, "main": false, "testdata": false,
		"foo_test": false, "bot_windows": false, "app_js": false, "x_arm64": false,
		"doctor": false, "chat": false, "mcp": false, "x": false, "Bad": false,
	} {
		if err := validateTotemName(name); (err == nil) != ok {
			t.Errorf("validateTotemName(%q) = %v, want ok=%v", name, err, ok)
		}
	}
}

// Both scaffolds compile against the current public API: a scaffold that
// stops building would only be found by the next person who runs generate.
func TestScaffoldsCompile(t *testing.T) {
	if testing.Short() {
		t.Skip("runs the go tool")
	}
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	base, err := writeExternalModule(t.TempDir(), "", "demo-totem", "A demo")
	if err != nil {
		t.Fatal(err)
	}
	// The internal scaffold, as one more package of the same module.
	if err := os.MkdirAll(filepath.Join(base, "inrepo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "inrepo", "inrepo.go"), []byte(internalTotemSource("inrepo", `say "hi"`)), 0o644); err != nil {
		t.Fatal(err)
	}
	gomod := filepath.Join(base, "go.mod")
	b, _ := os.ReadFile(gomod)
	b = append(b, []byte("\nreplace github.com/discorgento/ouija => "+filepath.ToSlash(repo)+"\n")...)
	if err := os.WriteFile(gomod, b, 0o644); err != nil {
		t.Fatal(err)
	}
	// tidy -e: tidy resolves the imports of every OS, and a Linux-only one
	// (landlock) is never in the module cache of a macOS or Windows machine
	// running this offline. vet, for this OS, still fails on a broken scaffold.
	for _, args := range [][]string{{"mod", "tidy", "-e"}, {"vet", "./..."}} {
		cmd := exec.Command("go", args...)
		cmd.Dir = base
		cmd.Env = append(os.Environ(), "GOPROXY=off", "GOFLAGS=-mod=mod", "GOWORK=off")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("go %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
}

func TestOuijaRequire(t *testing.T) {
	for v, want := range map[string]string{
		"0.0.0":     "",
		"1.2.0":     "\nrequire github.com/discorgento/ouija v1.2.0\n",
		"1.3.0-dev": "",
	} {
		if got := ouijaRequire(v); got != want {
			t.Errorf("ouijaRequire(%q) = %q, want %q", v, got, want)
		}
	}
}

func TestCheckOuijaCheckout(t *testing.T) {
	if err := checkOuijaCheckout(t.TempDir()); err == nil || !strings.Contains(err.Error(), "--external") {
		t.Fatalf("outside a checkout: %v", err)
	}
	if err := checkOuijaCheckout("../.."); err != nil {
		t.Fatalf("in the repo: %v", err)
	}
}
