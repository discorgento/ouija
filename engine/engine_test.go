package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSandbox(t *testing.T) {
	root := t.TempDir()
	ctx := context.Background()

	if _, err := resolve(ctx, root, "a/b.txt"); err != nil {
		t.Fatalf("valid path rejected: %v", err)
	}
	for _, bad := range []string{"../etc/passwd", "a/../../x", "../../../../etc/shadow"} {
		if _, err := resolve(ctx, root, bad); err == nil {
			t.Fatalf("escape not rejected: %q", bad)
		}
	}
}

func TestResolveRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	ctx := context.Background()

	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("top secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	// a symlink inside the root pointing outside it
	link := filepath.Join(root, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := resolve(ctx, root, "link/secret.txt"); err == nil {
		t.Fatal("symlink escape not rejected")
	}
}

func TestGlobStaysInSandbox(t *testing.T) {
	root := t.TempDir()
	parent := filepath.Dir(root)
	// a file in the parent dir must never show up via a ../* glob
	if err := os.WriteFile(filepath.Join(parent, "outside.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := globTool(context.Background(), root, map[string]any{"pattern": "../*"})
	if res.Error != nil {
		t.Fatal(res.Error)
	}
	if strings.Contains(res.Data, "outside.txt") || strings.Contains(res.Data, "..") {
		t.Fatalf("glob escaped the sandbox: %q", res.Data)
	}
}

func TestSafeJoinContainsScope(t *testing.T) {
	root := t.TempDir()
	if _, err := SafeJoin(root, "a/docs"); err != nil {
		t.Fatalf("valid scope rejected: %v", err)
	}
	if _, err := SafeJoin(root, "../../tmp/evil"); err == nil {
		t.Fatal("scope escape not rejected")
	}
}

func TestExtractJSON(t *testing.T) {
	js, ok := extractJSON(`blah blah {"a":1,"b":"x"} trailing text`)
	if !ok || js != `{"a":1,"b":"x"}` {
		t.Fatalf("extractJSON = %q ok=%v", js, ok)
	}
	if _, ok := extractJSON("no json here"); ok {
		t.Fatal("should fail without JSON")
	}
	if _, ok := extractJSON(`{"broken": }`); ok {
		t.Fatal("should fail with invalid JSON")
	}
}

func TestToolRiskFailsClosed(t *testing.T) {
	if toolRisk("read_file") != RiskReadOnly {
		t.Fatal("read_file should be read-only")
	}
	if toolRisk("rm_rf_everything") != RiskWrite {
		t.Fatal("unknown tool should fail closed (write)")
	}
}

func TestBudgetCeilings(t *testing.T) {
	tr := newTracker(100, 0, 0, 0, 0)
	if err := tr.Add(Usage{InputTokens: 60, OutputTokens: 0}); err != nil {
		t.Fatalf("should not exceed yet: %v", err)
	}
	if err := tr.Add(Usage{InputTokens: 60, OutputTokens: 0}); err == nil {
		t.Fatal("should exceed the token ceiling")
	}
}
