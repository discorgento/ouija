package engine

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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

// The gate checks the path the tool will really touch: an absolute path
// into the project names the same file as the relative one, so both map to
// one checked path, the one the tools write.
func TestGatePathMatchesTheTools(t *testing.T) {
	root := t.TempDir()
	r := NewToolRunner(ToolRunnerConfig{Tools: NewTools(root), Doom: &DoomDetector{}})
	rel := r.checkPathFor("notes.txt")
	abs := r.checkPathFor(filepath.Join(root, "notes.txt"))
	if rel != abs {
		t.Fatalf("notes.txt and its absolute path are one file, but checked as %s and %s", rel, abs)
	}
	if want, _ := SafeJoin(root, filepath.Join(root, "notes.txt")); abs != want {
		t.Fatalf("checked %s, the tools write %s", abs, want)
	}
	_ = context.Background()
}

// A model often names files by their absolute path. Inside the project that
// is the file itself (it was joined onto the root: the read found nothing
// and the write landed in a new <root>/home/... tree); outside it is
// refused, with a hint to use a relative path.
func TestAbsolutePathsNameTheFile(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if res := readFileTool(ctx, root, map[string]any{"path": filepath.Join(root, "main.go")}); res.Error != nil || !strings.Contains(res.Data, "package main") {
		t.Fatalf("reading the project's file by its absolute path: %s", res.String())
	}
	abs := filepath.Join(root, "sub", "new.txt")
	if res := writeFileTool(ctx, root, map[string]any{"path": abs, "content": "x"}); res.Error != nil {
		t.Fatalf("writing by absolute path: %s", res.String())
	}
	if b, err := os.ReadFile(abs); err != nil || string(b) != "x" {
		t.Fatalf("the write did not land at %s: %v", abs, err)
	}
	outside := filepath.Join(t.TempDir(), "x.txt")
	res := writeFileTool(ctx, root, map[string]any{"path": outside, "content": "x"})
	if res.Error == nil || !strings.Contains(res.Error.Message, "outside the project") {
		t.Fatalf("a write outside the project: %s", res.String())
	}
	if _, err := os.Stat(outside); err == nil {
		t.Fatal("a write outside the project landed")
	}
	if entries, _ := os.ReadDir(root); len(entries) != 2 {
		t.Fatalf("the project holds %d entries; an absolute path made a stray tree", len(entries))
	}
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

// A path the prompt cannot show as it is never reaches it: a newline pushed
// the real target (…/../../Makefile) out of a cut prompt line.
func TestPathWithControlCharRefusedBeforePrompt(t *testing.T) {
	root := t.TempDir()
	am := NewApprovalManager()
	prompts := 0
	am.SetCallback(func(context.Context, string, map[string]any) (ApprovalResult, error) {
		prompts++
		return ApprovalResult{Allowed: true}, nil
	})
	r := NewToolRunner(ToolRunnerConfig{
		Tools: NewTools(root),
		Gate:  &gateState{permConfig: NewDefaultPermissionConfig(), approvalMgr: am},
		Doom:  &DoomDetector{},
	})
	for _, tool := range []string{"write_file", "read_file", "delete_file"} {
		for _, p := range []string{"docs/notes.md\n\n  will create docs/notes.md/../../Makefile", "a\tb", "a\u202eb", "a\x1b[2Jb"} {
			res := r.Run(context.Background(), "t", ToolCall{Name: tool, Input: map[string]any{"path": p, "content": "x"}}, nil, nil)
			if res.Error == nil || res.Error.Code != ErrCodeInvalidInput {
				t.Fatalf("%s %q: %+v, want refused", tool, p, res)
			}
		}
	}
	if prompts != 0 {
		t.Fatalf("%d prompts for paths that are refused", prompts)
	}
	if _, err := os.Stat(filepath.Join(root, "Makefile")); err == nil {
		t.Fatal("the Makefile was written")
	}
}

// The prompt is given the file the write lands on, cleaned and followed, and
// the name it is reached by; a session approval covers that file, so a
// symlink later pointed elsewhere asks again.
func TestApprovalShowsAndKeysThePhysicalTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root := t.TempDir()
	for _, f := range []string{"Makefile", "notes.txt", "README"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("orig"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	am := NewApprovalManager()
	var asked []map[string]any
	am.SetCallback(func(_ context.Context, _ string, in map[string]any) (ApprovalResult, error) {
		asked = append(asked, in)
		return ApprovalResult{Allowed: true, ForSession: true}, nil
	})
	r := NewToolRunner(ToolRunnerConfig{
		Tools: NewTools(root),
		Gate:  &gateState{permConfig: NewDefaultPermissionConfig(), approvalMgr: am},
		Doom:  &DoomDetector{},
	})
	write := func(p, content string) {
		t.Helper()
		if res := r.Run(context.Background(), "t", ToolCall{Name: "write_file", Input: map[string]any{"path": p, "content": content}}, nil, nil); res.Error != nil {
			t.Fatalf("write %s: %+v", p, res.Error)
		}
	}
	write("docs/x/../../README", "1")
	if got := asked[0]["_path"]; got != "README" {
		t.Fatalf("_path = %v, want the cleaned README", got)
	}
	write("notes.txt", "2") // a plain file: approved for the session
	if err := os.Remove(filepath.Join(root, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("Makefile", filepath.Join(root, "notes.txt")); err != nil {
		t.Fatal(err)
	}
	write("notes.txt", "3")
	if len(asked) != 3 {
		t.Fatalf("%d prompts; the session approval of notes.txt covered its swap to the Makefile", len(asked))
	}
	last := asked[2]
	if last["_path"] != "Makefile" || last["_via"] != "notes.txt" {
		t.Fatalf("prompt names _path=%v _via=%v, want Makefile via notes.txt", last["_path"], last["_via"])
	}
	if p, _ := last["_preview"].(string); !strings.Contains(p, "notes.txt → Makefile (symlink)") {
		t.Fatalf("preview %q does not say the write lands on the Makefile", p)
	}
}
