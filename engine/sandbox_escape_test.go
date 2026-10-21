package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// An approved escape outside the project is the user's consent to that path,
// not to wherever it leads: a symlink there pointing at ~/.ssh must still be
// refused, like any protected path. The prompt showed the innocent-looking
// lexical path, and the key came back.
func TestApprovedEscapeStillRefusesProtectedTargets(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ssh := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(ssh, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ssh, "id_rsa"), []byte("PRIVATE KEY"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ssh, filepath.Join(parent, "shared")); err != nil {
		t.Skipf("symlinks: %v", err)
	}

	for name, rel := range map[string]string{
		"lexical escape":    filepath.Join("..", "shared", "id_rsa"),
		"through a link in": "inlink/id_rsa",
	} {
		if name == "through a link in" {
			if err := os.Symlink(filepath.Join(parent, "shared"), filepath.Join(root, "inlink")); err != nil {
				t.Fatal(err)
			}
		}
		approved := filepath.Join(parent, "shared", "id_rsa") // what the prompt showed and the user approved
		ctx := context.WithValue(context.Background(), escapeKey{}, approved)
		if got, err := resolve(ctx, root, rel); err == nil {
			t.Errorf("%s: resolved to %s; want the protected target refused", name, got)
		}
	}

	// An approved escape to an ordinary file still works.
	other := filepath.Join(parent, "notes.txt")
	if err := os.WriteFile(other, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), escapeKey{}, other)
	if got, err := resolve(ctx, root, filepath.Join("..", "notes.txt")); err != nil || got != other {
		t.Fatalf("ordinary approved escape: %q, %v", got, err)
	}
}

// Through the tools: the escape prompt names the real target, and a
// protected target is refused without asking at all.
func TestEscapePromptShowsTheRealTarget(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	ssh := filepath.Join(home, ".ssh")
	os.MkdirAll(ssh, 0o700)
	os.WriteFile(filepath.Join(ssh, "id_rsa"), []byte("PRIVATE KEY"), 0o600)
	parent := t.TempDir()
	root := filepath.Join(parent, "project")
	os.MkdirAll(root, 0o755)
	if err := os.Symlink(ssh, filepath.Join(parent, "shared")); err != nil {
		t.Skipf("symlinks: %v", err)
	}
	other := t.TempDir()
	os.WriteFile(filepath.Join(other, "notes.txt"), []byte("hello"), 0o644)
	if err := os.Symlink(other, filepath.Join(parent, "docs")); err != nil {
		t.Fatal(err)
	}

	var asked []string
	tools := NewTools(root)
	tools.escapeApprover = func(_ context.Context, _ string, path string) bool {
		asked = append(asked, path)
		return true
	}
	read := func(p string) ToolResult {
		return tools.Run(context.Background(), ToolCall{Name: "read_file", Input: map[string]any{"path": p}})
	}

	if res := read(filepath.Join("..", "shared", "id_rsa")); res.Error == nil || len(asked) != 0 {
		t.Fatalf("protected target: result %+v, asked %v; want refused without a prompt", res, asked)
	}
	res := read(filepath.Join("..", "docs", "notes.txt"))
	want := filepath.Join(other, "notes.txt")
	if r, err := filepath.EvalSymlinks(other); err == nil {
		want = filepath.Join(r, "notes.txt")
	}
	if res.Error != nil || len(asked) != 1 || asked[0] != want {
		t.Fatalf("ordinary escape: result %+v, asked %v; want one prompt naming %s", res, asked, want)
	}
}
