package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetProtectedFiles makes the next check read the credential files again.
func resetProtectedFiles(t *testing.T) {
	protectedFiles.Lock()
	protectedFiles.dirs = nil
	protectedFiles.Unlock()
	t.Cleanup(func() {
		protectedFiles.Lock()
		protectedFiles.dirs = nil
		protectedFiles.Unlock()
	})
}

// A hard link is the key under another name: notes.txt linked to
// ~/.ssh/id_rsa must be refused like the key, by the file tools, bash and
// the walk grep and glob use.
func TestHardLinkToACredentialIsProtected(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	resetProtectedFiles(t)
	key := filepath.Join(home, ".ssh", "id_rsa")
	if err := os.MkdirAll(filepath.Dir(key), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(key, []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(key, filepath.Join(root, "notes.txt")); err != nil {
		t.Skip("no hard links across these dirs:", err)
	}
	if err := os.WriteFile(filepath.Join(root, "plain.txt"), []byte("ok"), 0o600); err != nil {
		t.Fatal(err)
	}
	// A hard link between two ordinary files (pnpm's store) is fine.
	if err := os.Link(filepath.Join(root, "plain.txt"), filepath.Join(root, "plain2.txt")); err != nil {
		t.Fatal(err)
	}

	if _, err := resolve(context.Background(), root, "notes.txt"); err == nil {
		t.Error("resolve handed out a hard link to the key")
	}
	if _, err := resolve(context.Background(), root, "plain2.txt"); err != nil {
		t.Errorf("an ordinary hard link was refused: %v", err)
	}
	if _, ok := commandRefsProtectedPathIn(root, "cat notes.txt"); !ok {
		t.Error("bash could cat the hard link")
	}
	var seen []string
	if err := walkFiles(context.Background(), root, root, func(_, rel string, _ os.DirEntry) error {
		seen = append(seen, rel)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, rel := range seen {
		if rel == "notes.txt" {
			t.Error("the walk (grep, glob) listed the hard link")
		}
	}
}

// A credential deep in a directory (gcloud's legacy_credentials, aws's sso
// cache) and one added after the list was read are both covered.
func TestHardLinkToADeepOrNewCredential(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	resetProtectedFiles(t)
	write := func(rel string) string {
		t.Helper()
		p := filepath.Join(home, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("SECRET"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	deep := write(".config/gcloud/legacy_credentials/me@x.com/adc.json")
	if err := os.Link(deep, filepath.Join(root, "a.txt")); err != nil {
		t.Skip("no hard links here:", err)
	}
	if !linksToProtected(filepath.Join(root, "a.txt")) {
		t.Error("a hard link to a credential four levels down was not caught")
	}
	// The list is cached now; a key made afterwards must still be found.
	fresh := write(".ssh/id_ed25519")
	if err := os.Link(fresh, filepath.Join(root, "b.txt")); err != nil {
		t.Fatal(err)
	}
	if !linksToProtected(filepath.Join(root, "b.txt")) {
		t.Error("a hard link to a key created after the cache was filled was not caught")
	}
}

// A hard link inside the project is the protected file under another name:
// .env linked as notes.txt was read and grepped without a prompt, and
// .git/config linked as notes.txt was written. pnpm's node_modules, hard
// links from its store, stays readable.
func TestHardLinkToProjectSecretRefused(t *testing.T) {
	root, store := t.TempDir(), t.TempDir()
	writeTree(t, root, map[string]string{".env": "API_KEY=s3cr3t\n", ".git/config": "[core]\n"})
	if err := os.WriteFile(filepath.Join(store, "index.js"), []byte("module.exports = 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "node_modules", "pkg"), 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		"notes.txt":                     filepath.Join(root, ".env"),
		"cfg.txt":                       filepath.Join(root, ".git", "config"),
		"node_modules/pkg/index.js":     filepath.Join(store, "index.js"),
		"node_modules/pkg/notes.txt":    filepath.Join(root, ".env"),
		"node_modules/pkg/notes-cfg.md": filepath.Join(root, ".git", "config"),
	} {
		if err := os.Link(target, filepath.Join(root, filepath.FromSlash(link))); err != nil {
			t.Skipf("hard links: %v", err)
		}
	}
	ctx := context.Background()
	for _, p := range []string{"notes.txt", "node_modules/pkg/notes.txt", "node_modules/pkg/notes-cfg.md"} {
		if res := readFileTool(ctx, root, map[string]any{"path": p}); res.Error == nil {
			t.Errorf("read_file %s handed out a protected file: %q", p, res.Data)
		}
	}
	if res := grepTool(ctx, root, map[string]any{"pattern": "s3cr3t|core"}); strings.Contains(res.Data, "s3cr3t") || strings.Contains(res.Data, "core") {
		t.Errorf("grep found the linked secrets: %q", res.Data)
	}
	if res := writeFileTool(ctx, root, map[string]any{"path": "cfg.txt", "content": "[core]\n\tfsmonitor = evil\n"}); res.Error == nil {
		t.Error("write_file wrote .git/config through a hard link")
	}
	if b, _ := os.ReadFile(filepath.Join(root, ".git", "config")); string(b) != "[core]\n" {
		t.Errorf(".git/config is now %q", b)
	}
	if res := readFileTool(ctx, root, map[string]any{"path": "node_modules/pkg/index.js"}); res.Error != nil {
		t.Errorf("a pnpm-linked file in node_modules: %+v", res.Error)
	}
}
