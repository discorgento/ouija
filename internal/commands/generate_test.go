package commands

import (
	"os"
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
	if !strings.Contains(string(gomod), "github.com/discorgento/ouija") {
		t.Errorf("go.mod lacks ouija require:\n%s", gomod)
	}

	totem, _ := os.ReadFile(filepath.Join(dir, "totems", "my-agent", "my-agent.go"))
	if !strings.Contains(string(totem), "registry.Register(&MyAgent{})") {
		t.Errorf("totem lacks register call:\n%s", totem)
	}
	if strings.Contains(string(totem), "TODO") {
		t.Errorf("external totem should not be a TODO stub:\n%s", totem)
	}

	host, _ := os.ReadFile(filepath.Join(dir, "cmd", "my-agent", "main.go"))
	for _, want := range []string{
		"bootstrap.Setup",
		"ServeMCP",
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
