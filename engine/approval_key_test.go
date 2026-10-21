package engine

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// "Approve for session" covers one target. A tool without a path or command
// (a custom http_post) was keyed by its name alone, so approving one URL
// approved every later URL.
func TestApprovalKeyCoversTheArguments(t *testing.T) {
	a := approvalSessionKey("http_post", "", map[string]any{"url": "https://ok.example"})
	b := approvalSessionKey("http_post", "", map[string]any{"url": "https://evil.example/exfil"})
	if a == b {
		t.Fatalf("both URLs share the key %q", a)
	}
	// The same arguments in another order are the same call.
	c := approvalSessionKey("deploy", "", map[string]any{"env": "prod", "tag": "v1"})
	d := approvalSessionKey("deploy", "", map[string]any{"tag": "v1", "env": "prod"})
	if c != d {
		t.Fatalf("argument order changed the key: %q vs %q", c, d)
	}
	// The engine's own _preview does not split one call into two keys.
	if approvalSessionKey("x", "", map[string]any{"a": 1}) != approvalSessionKey("x", "", map[string]any{"a": 1, "_preview": "diff"}) {
		t.Fatal("_preview changed the key")
	}
}

// The gate checks the path the tool will really touch: the file tools put an
// absolute path under the project, so the gate must too, or approving
// notes.txt pre-approved "<root>/notes.txt" (another file entirely).
func TestGatePathMatchesTheTools(t *testing.T) {
	root := t.TempDir()
	r := NewToolRunner(ToolRunnerConfig{Tools: NewTools(root), Doom: &DoomDetector{}})
	rel := r.checkPathFor("notes.txt")
	abs := r.checkPathFor(filepath.Join(root, "notes.txt"))
	if rel == abs {
		t.Fatalf("notes.txt and %s map to the same checked path %s, but the tools write different files", filepath.Join(root, "notes.txt"), rel)
	}
	if want, _ := SafeJoin(root, filepath.Join(root, "notes.txt")); abs != want {
		t.Fatalf("checked %s, the tools write %s", abs, want)
	}
	_ = context.Background()
}

// A call approved "for the session" and repeated identically is still a loop:
// only a human's fresh answer resets the doom-loop count, not the cache.
func TestDoomLoopStillFiresOnSessionApprovals(t *testing.T) {
	root := t.TempDir()
	am := NewApprovalManager()
	prompts := 0
	am.SetCallback(func(context.Context, string, map[string]any) (ApprovalResult, error) {
		prompts++
		return ApprovalResult{Allowed: true, ForSession: true}, nil
	})
	r := NewToolRunner(ToolRunnerConfig{
		Tools: NewTools(root),
		Gate:  &gateState{permConfig: NewDefaultPermissionConfig(), approvalMgr: am},
		Doom:  &DoomDetector{},
	})
	call := ToolCall{Name: "write_file", Input: map[string]any{"path": "a.txt", "content": "x"}}
	blocked := -1
	for i := 0; i < 10; i++ {
		res := r.Run(context.Background(), "t", call, nil, nil)
		if res.Error != nil && res.Error.Code == ErrCodeDoomLoop {
			blocked = i
			break
		}
	}
	if prompts != 1 || blocked < 0 {
		t.Fatalf("prompts = %d, blocked at call %d; want one prompt and the loop blocked", prompts, blocked)
	}
}

// Deleting a directory must not take protected files with it: delete_file
// "app" removed app/.env, and the preview only said "1 items inside".
func TestDeleteDirKeepsProtectedFiles(t *testing.T) {
	root := t.TempDir()
	for _, f := range []string{"app/.env", "app/main.go", "build/out.bin"} {
		p := filepath.Join(root, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	del := func(p string) ToolResult {
		return deleteFileTool(context.Background(), root, map[string]any{"path": p})
	}
	if res := del("app"); res.Error == nil {
		t.Fatal("deleted a directory holding .env")
	}
	if _, err := os.Stat(filepath.Join(root, "app", ".env")); err != nil {
		t.Fatalf(".env is gone: %v", err)
	}
	if res := del("build"); res.Error != nil {
		t.Fatalf("an ordinary directory: %+v", res)
	}
	// A nested clone goes whole: its .git/config is protected against being
	// written, not against the repository being deleted.
	if err := os.MkdirAll(filepath.Join(root, "vendor", "lib", ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "vendor", "lib", ".git", "config"), []byte("[core]"), 0o600); err != nil {
		t.Fatal(err)
	}
	if res := del("vendor/lib"); res.Error != nil {
		t.Fatalf("a nested clone: %+v", res)
	}
}

// The protected-name checks see a path the way the OS will open it: macOS and
// Windows fold "ſ" (long s) onto "s", and Windows drops trailing dots and
// spaces, so these all named a protected file. Tested through policyName so
// every OS runs it.
func TestPolicyNameMatchesTheFilesystem(t *testing.T) {
	for _, tc := range []struct {
		in        string
		fold, win bool
		want      string
	}{
		{"/home/u/.\u017f\u017fh/id_rsa", true, false, "/home/u/.ssh/id_rsa"},
		{"/p/.ouija.j\u017fon", true, false, "/p/.ouija.json"},
		{"/p/.ou\u0131ja.json", true, false, "/p/.ouija.json"},
		{"/home/u/\u0130d_rsa", true, false, "/home/u/id_rsa"},
		{"/p/\u212aube/x", true, false, "/p/kube/x"},
		{"C:/Users/u/.ssh./id_rsa", true, true, "c:/users/u/.ssh/id_rsa"},
		{"C:/p/.ouija.json.", true, true, "c:/p/.ouija.json"},
		{"C:/p/.gitconfig ", true, true, "c:/p/.gitconfig"},
		{"C:/p/../q/./r", true, true, "c:/p/../q/./r"},
		{"/home/u/.ssh/id_rsa", false, false, "/home/u/.ssh/id_rsa"},
	} {
		if got := policyName(tc.in, tc.fold, tc.win); got != tc.want {
			t.Errorf("policyName(%q, fold=%v, win=%v) = %q, want %q", tc.in, tc.fold, tc.win, got, tc.want)
		}
	}
	// And on this OS, the real check refuses what it would open.
	if foldCase && !isProtectedPath(filepath.Join(t.TempDir(), ".\u017f\u017fh", "id_rsa")) {
		t.Error("isProtectedPath let .ſſh through on a case-folding OS")
	}
}

// An extra tool with a path keys its approval on every argument too: one
// approved upload of a file must not approve sending it elsewhere. A
// model-chosen "_" argument counts like any other.
func TestApprovalKeyCoversArgumentsBesideThePath(t *testing.T) {
	ok := approvalSessionKey("upload", "/p/a.txt", map[string]any{"path": "a.txt", "url": "https://ok"})
	evil := approvalSessionKey("upload", "/p/a.txt", map[string]any{"path": "a.txt", "url": "https://evil"})
	if ok == evil {
		t.Error("upload: the url does not change the approval key")
	}
	if approvalSessionKey("x", "", map[string]any{"_body": "a"}) == approvalSessionKey("x", "", map[string]any{"_body": "b"}) {
		t.Error("a model-chosen _ argument is left out of the key")
	}
	// The file tools keep one approval per file for the session.
	if approvalSessionKey("edit_file", "/p/a.go", map[string]any{"old": "1"}) != approvalSessionKey("edit_file", "/p/a.go", map[string]any{"old": "2"}) {
		t.Error("edit_file: a session approval no longer covers the file")
	}
}
