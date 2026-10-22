package mcp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/user"
	"path/filepath"
	"strings"
	"testing"

	"github.com/discorgento/ouija/registry"
)

// A line over maxLineBytes is refused with a parse error, and the next
// message is still read: it used to be read into memory whole.
func TestLongLineIsRefusedAndTheStreamContinues(t *testing.T) {
	ps := startPipeServer(t, testServer(t.TempDir()))
	ps.send(`{"jsonrpc":"2.0","id":1,"method":"ping","params":"` + strings.Repeat("x", maxLineBytes) + `"}`)
	ps.send(`{"jsonrpc":"2.0","id":2,"method":"ping"}`)
	if e := errorOf(t, ps.next()); e["code"] != float64(-32700) {
		t.Fatalf("long line: %v, want a parse error", e)
	}
	if got := ps.next(); got["id"] != float64(2) {
		t.Fatalf("after the long line got %v, want the ping", got)
	}
	if err := ps.closeIn(); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestReadLine(t *testing.T) {
	r := bufio.NewReaderSize(strings.NewReader("short\n"+strings.Repeat("y", 100)+"\nlast"), 16)
	for _, want := range []struct {
		line    string
		tooLong bool
	}{{"short\n", false}, {"", true}, {"last", false}} {
		line, tooLong, _ := readLine(r, 50)
		if string(line) != want.line || tooLong != want.tooLong {
			t.Fatalf("readLine = %q %v, want %q %v", line, tooLong, want.line, want.tooLong)
		}
	}
}

// blockingServer runs every tools/call until release is closed.
func blockingServer(t *testing.T) (*Server, chan struct{}) {
	release := make(chan struct{})
	s := testServer(t.TempDir())
	s.run = func(ctx context.Context, _ string, _ registry.Args) (any, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return map[string]string{"ok": "yes"}, nil
	}
	return s, release
}

func taskCall(id int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"tools/call","params":{"name":"ouija_task","arguments":{"task":"x"}}}`, id)
}

// A second request under an id still running is refused: a cancellation
// could not tell the two apart.
func TestDuplicateInflightIDIsRefused(t *testing.T) {
	s, release := blockingServer(t)
	ps := startPipeServer(t, s)
	ps.send(taskCall(1))
	ps.send(taskCall(1))
	if e := errorOf(t, ps.next()); e["code"] != float64(-32600) {
		t.Fatalf("duplicate id: %v, want -32600", e)
	}
	close(release)
	if got := ps.next(); got["id"] != float64(1) || got["error"] != nil {
		t.Fatalf("the first call: %v", got)
	}
	if err := ps.closeIn(); err != nil {
		t.Fatal(err)
	}
}

// Calls beyond maxCalls running at once are refused, not started: each one
// has its own budget.
func TestTooManyCallsAreRefused(t *testing.T) {
	s, release := blockingServer(t)
	ps := startPipeServer(t, s)
	for id := range maxCalls + 1 {
		ps.send(taskCall(id + 1))
	}
	got := ps.next()
	if e := errorOf(t, got); e["code"] != float64(-32000) || got["id"] != float64(maxCalls+1) {
		t.Fatalf("call over the limit: %v", got)
	}
	close(release)
	for range maxCalls {
		if got := ps.next(); got["error"] != nil {
			t.Fatalf("a call under the limit failed: %v", got)
		}
	}
	if err := ps.closeIn(); err != nil {
		t.Fatal(err)
	}
}

func readURI(t *testing.T, s *Server, uri string) map[string]any {
	t.Helper()
	return callOn(t, s, fmt.Sprintf(`{"jsonrpc":"2.0","id":1,"method":"resources/read","params":{"uri":%q}}`, uri))
}

// Only ouija://project/ URIs are read: another scheme or a bare path used to
// be joined onto the root as is.
func TestResourceReadRequiresTheScheme(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := testServer(root)
	for _, uri := range []string{"a.txt", "file:///a.txt", "OUIJA://project/a.txt", "/a.txt"} {
		if e := errorOf(t, readURI(t, s, uri)); e["code"] != float64(-32602) {
			t.Errorf("%s: %v", uri, e)
		}
	}
}

// Errors name the resource, not where the project is on disk.
func TestResourceErrorsHideAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	s := testServer(root)
	for _, uri := range []string{"ouija://project/missing.txt", "ouija://project/../x", "ouija://project/.env"} {
		e := errorOf(t, readURI(t, s, uri))
		if msg, _ := e["message"].(string); strings.Contains(msg, root) {
			t.Errorf("%s: the error shows the root: %q", uri, msg)
		}
	}
}

// Started from / or the home folder, the resource API serves nothing: it
// has no permission gate, and the "project" would be all the user's files.
func TestResourceReadRefusesABroadRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	linkToRoot := filepath.Join(t.TempDir(), "rootlink")
	if err := os.Symlink(string(filepath.Separator), linkToRoot); err != nil {
		linkToRoot = string(filepath.Separator)
	}
	for _, root := range []string{string(filepath.Separator), home, filepath.Dir(home), linkToRoot} {
		for _, uri := range []string{resourceProjectURI, resourceProjectURI + "notes.txt"} {
			if e := errorOf(t, readURI(t, testServer(root), uri)); !strings.Contains(e["message"].(string), "resources are off") {
				t.Errorf("root %s, %s: %v", root, uri, e)
			}
		}
	}
	// A project inside the home folder is fine.
	proj := filepath.Join(home, "proj")
	if err := os.Mkdir(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	resultOf(t, readURI(t, testServer(proj), resourceProjectURI))
}

// Started from / or the home folder, tool calls are refused too: the totem's
// sandbox would be all the user's files, readable, searchable and writable.
// The error tells the user how to fix it, and no totem runs.
func TestToolCallRefusesABroadRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	linkToHome := filepath.Join(t.TempDir(), "homelink")
	if err := os.Symlink(home, linkToHome); err != nil {
		linkToHome = home
	}
	for _, root := range []string{string(filepath.Separator), home, filepath.Dir(home), linkToHome} {
		s := testServer(root)
		ran := false
		s.run = func(context.Context, string, registry.Args) (any, error) { ran = true; return "done", nil }
		res := resultOf(t, callOn(t, s, taskCall(1)))
		if res["isError"] != true {
			t.Errorf("root %s: tool call not refused: %v", root, res)
		}
		text := res["content"].([]any)[0].(map[string]any)["text"].(string)
		if !strings.HasPrefix(text, "CONFIG: ") || !strings.Contains(text, "project directory") {
			t.Errorf("root %s: refusal does not say how to fix it: %q", root, text)
		}
		if ran {
			t.Errorf("root %s: the totem ran", root)
		}
	}
	// A project inside the home folder runs the totem.
	proj := filepath.Join(home, "proj")
	if err := os.Mkdir(proj, 0o700); err != nil {
		t.Fatal(err)
	}
	s := testServer(proj)
	ran := false
	s.run = func(context.Context, string, registry.Args) (any, error) { ran = true; return "done", nil }
	if res := resultOf(t, callOn(t, s, taskCall(1))); res["isError"] != false || !ran {
		t.Fatalf("project root: %v, ran=%v", res, ran)
	}
}

// A resources/read the client cancelled gets no answer, like a tool call.
func TestResourceReadCancelledIsNotAnswered(t *testing.T) {
	s := testServer(t.TempDir())
	call := &inflightCall{cancel: func() {}}
	call.byClient.Store(true)
	ctx := context.WithValue(context.Background(), inflightKey{}, call)
	var buf bytes.Buffer
	s.handleResourceRead(ctx, bufio.NewWriter(&buf), rpcRequest{
		JSONRPC: "2.0", ID: []byte("1"), Method: "resources/read",
		Params: []byte(`{"uri":"ouija://project/"}`),
	})
	if buf.Len() != 0 {
		t.Fatalf("a cancelled read was answered: %s", buf.String())
	}
}

// A dangling symlink planted at the .bak name must not make the backup land
// where it points.
func TestBackupFileDoesNotFollowALink(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(cfg, []byte(`{"secret":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "elsewhere", "planted")
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, cfg+".bak"); err != nil {
		t.Skip("no symlinks here:", err)
	}
	_ = backupFile(cfg)
	if _, err := os.Stat(target); err == nil {
		t.Fatal("the backup was written through the planted link")
	}
}

// 1, 1.0 and 1e0 are the same JSON-RPC id; "1" is another.
func TestCallKeyByValue(t *testing.T) {
	for _, id := range []string{"1", "1.0", " 1e0 ", "10e-1"} {
		if got := callKey([]byte(id)); got != "1" {
			t.Errorf("callKey(%s) = %q, want 1", id, got)
		}
	}
	if callKey([]byte("9007199254740993")) == callKey([]byte("9007199254740992")) {
		t.Error("two large integer ids share a key")
	}
	if callKey([]byte(`"1"`)) == callKey([]byte("1")) {
		t.Error(`the string "1" and the number 1 share a key`)
	}
}

// An empty or redirected HOME does not hide the real home folder: the
// account database names it too. With no home folder known at all, a root
// near the top of the filesystem is refused.
func TestBroadRootWithoutHOME(t *testing.T) {
	u, err := user.Current()
	if err != nil || u.HomeDir == "" {
		t.Skip("no home folder in the account database")
	}
	for _, home := range []string{"", t.TempDir()} {
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		if !broadRoot(u.HomeDir) {
			t.Errorf("HOME=%q: the real home folder %s is not refused", home, u.HomeDir)
		}
		if !broadRoot(filepath.Dir(u.HomeDir)) {
			t.Errorf("HOME=%q: the folder above home is not refused", home)
		}
	}
	sep := string(filepath.Separator)
	for _, root := range []string{sep + "home", filepath.Join(sep+"home", "me")} {
		if !broadRootOf(root, nil) {
			t.Errorf("no home known: %s not refused", root)
		}
	}
	if deep := filepath.Join(t.TempDir(), "a", "proj"); broadRootOf(deep, nil) {
		t.Errorf("no home known: %s refused", deep)
	}
}
