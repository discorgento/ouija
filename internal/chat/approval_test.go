package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// The engine scopes session approvals by target ("tool\x00path"), so every
// piece of UI that matches on the tool name must reduce the key first.
func TestBaseToolStripsTarget(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"write_file", "write_file"},
		{"write_file\x00/abs/path", "write_file"},
		{"bash\x00rm -rf /tmp", "bash"},
		{"escape:write_file\x00/abs/path", "escape:write_file"},
	}
	for _, tt := range tests {
		if got := baseTool(tt.key); got != tt.want {
			t.Errorf("baseTool(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}

func TestDisplayToolNameScopedKeys(t *testing.T) {
	if got := displayToolName("edit_file\x00/abs/main.go"); got != "edit_file" {
		t.Errorf("displayToolName = %q, want edit_file", got)
	}
	if got := displayToolName("escape:read_file\x00/outside/x"); got != "read_file (outside sandbox)" {
		t.Errorf("displayToolName = %q, want 'read_file (outside sandbox)'", got)
	}
}

func TestApprovalOptionsForScopedKeys(t *testing.T) {
	// delete_file needs its own confirm-by-'d' option set; the scoped key must
	// still resolve to it.
	opts := approvalOptionsFor("delete_file\x00/abs/old.php")
	if opts[0].key != "d" {
		t.Fatalf("scoped delete_file should use the Delete option set, got first key %q", opts[0].key)
	}
	if opts := approvalOptionsFor("write_file\x00/abs/x"); opts[0].key != "y" {
		t.Fatalf("write_file should use the default option set, got first key %q", opts[0].key)
	}
}

func TestFriendlyArgScopedKeys(t *testing.T) {
	arg := friendlyArg("write_file\x00/abs/path", map[string]any{"path": "src/main.go"})
	if arg != "src/main.go" {
		t.Errorf("friendlyArg = %q, want src/main.go", arg)
	}
	if a := friendlyArg("bash\x00git status", map[string]any{"command": "git status"}); a != "git status" {
		t.Errorf("friendlyArg = %q, want 'git status'", a)
	}
}

func TestRenderApprovalBoxShowsScopedToolName(t *testing.T) {
	req := &approvalRequestMsg{
		tool:  "write_file\x00/abs/new.txt",
		input: map[string]any{"path": "new.txt", "content": "x"},
	}
	box := RenderApprovalBox(req, 1, 0, 80)
	if !strings.Contains(box, "write_file") {
		t.Errorf("approval box should show the tool name, got:\n%s", box)
	}
	if strings.Contains(box, "\x00") {
		t.Errorf("approval box must not leak the session-key separator:\n%s", box)
	}
}

// The prompt names the file the engine resolved, whole: the model's raw
// path, cut to the width, read as docs/notes… while the write landed on the
// Makefile; a symlink shows both of its names.
func TestApprovalNamesTheResolvedFileUncut(t *testing.T) {
	long := "docs/" + strings.Repeat("n", 180) + ".md"
	req := &approvalRequestMsg{
		tool:  "write_file\x00/p/Makefile",
		input: map[string]any{"path": long + "/../../Makefile", "content": "x", "_path": "Makefile"},
	}
	out := ansi.Strip(RenderApprovalBox(req, 1, 0, 60))
	if !strings.Contains(out, "write Makefile") || strings.Contains(out, "nnnn") {
		t.Errorf("the prompt does not name the Makefile:\n%s", out)
	}
	deep := "src/" + strings.Repeat("deep/", 30) + "main.go"
	req.input = map[string]any{"path": "notes.txt", "content": "x", "_path": deep, "_via": "notes.txt"}
	out = ansi.Strip(RenderApprovalBox(req, 1, 0, 60))
	joined := strings.NewReplacer("\n", "", " ", "").Replace(out)
	if !strings.Contains(joined, "writenotes.txt→"+deep+"(symlink)") {
		t.Errorf("the prompt cut or hid the symlink's target:\n%s", out)
	}
}
