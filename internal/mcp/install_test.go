package mcp

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestOpencodeEntry(t *testing.T) {
	e := opencodeEntry()

	if e["type"] != "local" {
		t.Errorf("type = %v, want local", e["type"])
	}
	if e["enabled"] != true {
		t.Errorf("enabled = %v, want true", e["enabled"])
	}
	cmd, ok := e["command"].([]string)
	if !ok || len(cmd) != 2 || cmd[0] != "ouija" || cmd[1] != "mcp" {
		t.Errorf("command = %#v, want [ouija mcp]", e["command"])
	}
}

func TestCursorEntry(t *testing.T) {
	e := cursorEntry()
	if e["command"] != "ouija" {
		t.Errorf("command = %v, want ouija", e["command"])
	}
	args, ok := e["args"].([]string)
	if !ok || len(args) != 1 || args[0] != "mcp" {
		t.Errorf("args = %#v, want [mcp]", e["args"])
	}
}

func TestJSONAddServer_CreatesFileAndContainer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "opencode.json")

	if err := jsonAddServer(path, "mcp", "ouija", opencodeEntry()); err != nil {
		t.Fatalf("jsonAddServer: %v", err)
	}

	root := readJSON(t, path)
	container, ok := root["mcp"].(map[string]any)
	if !ok {
		t.Fatalf("mcp = %#v, want an object", root["mcp"])
	}
	entry, ok := container["ouija"].(map[string]any)
	if !ok {
		t.Fatalf("mcp.ouija = %#v, want an object", container["ouija"])
	}
	if entry["type"] != "local" {
		t.Errorf("mcp.ouija.type = %v, want local", entry["type"])
	}
}

func TestJSONAddServer_PreservesKeysAndBacksUp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	original := `{"$schema":"https://x","theme":"dark","mcp":{"other":{"type":"local"}}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := jsonAddServer(path, "mcp", "ouija", opencodeEntry()); err != nil {
		t.Fatalf("jsonAddServer: %v", err)
	}

	root := readJSON(t, path)
	if root["$schema"] != "https://x" || root["theme"] != "dark" {
		t.Errorf("unrelated keys were lost: %#v", root)
	}
	container, _ := root["mcp"].(map[string]any)
	if _, ok := container["other"]; !ok {
		t.Error("existing server 'other' was lost")
	}
	if _, ok := container["ouija"]; !ok {
		t.Error("server 'ouija' was not added")
	}

	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("backup was not written: %v", err)
	}
	if string(bak) != original {
		t.Errorf("backup = %q, want the original content", bak)
	}
}

func TestJSONAddServer_IsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")

	for i := 0; i < 2; i++ {
		if err := jsonAddServer(path, "mcp", "ouija", opencodeEntry()); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
	}

	root := readJSON(t, path)
	container, _ := root["mcp"].(map[string]any)
	if len(container) != 1 {
		t.Errorf("mcp = %#v, want a single entry", container)
	}
}

func TestJSONHasServer(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "opencode.json")
	if err := os.WriteFile(path, []byte(`{"mcp":{"ouija":{"type":"local"}}}`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if ok, err := jsonHasServer(path, "mcp", "ouija"); err != nil || !ok {
		t.Errorf("jsonHasServer(ouija) = %v, %v; want true, nil", ok, err)
	}
	if ok, err := jsonHasServer(path, "mcp", "nope"); err != nil || ok {
		t.Errorf("jsonHasServer(nope) = %v, %v; want false, nil", ok, err)
	}
	if ok, err := jsonHasServer(path, "mcpServers", "ouija"); err != nil || ok {
		t.Errorf("jsonHasServer on a missing container = %v, %v; want false, nil", ok, err)
	}
	if ok, err := jsonHasServer(filepath.Join(dir, "absent.json"), "mcp", "ouija"); err != nil || ok {
		t.Errorf("jsonHasServer on a missing file = %v, %v; want false, nil", ok, err)
	}
}

func TestJSONAddServer_RejectsMalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, []byte(`{"mcp":`), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := jsonAddServer(path, "mcp", "ouija", opencodeEntry()); err == nil {
		t.Error("jsonAddServer on malformed JSON = nil, want an error")
	}
}

func TestRunInstall(t *testing.T) {
	var buf bytes.Buffer
	applied := 0

	hs := []harness{
		{name: "Absent", detect: func() bool { return false }},
		{name: "Connected", detect: func() bool { return true }, check: func() (bool, error) { return true, nil }},
		{name: "Fresh", detect: func() bool { return true }, check: func() (bool, error) { return false, nil }, apply: func() error { applied++; return nil }},
		{name: "Broken", detect: func() bool { return true }, check: func() (bool, error) { return false, nil }, apply: func() error { return os.ErrPermission }},
	}

	if !runInstall(&buf, hs) {
		t.Error("runInstall = false, want true (some harness was detected)")
	}
	if applied != 1 {
		t.Errorf("applied = %d, want 1 (only the fresh harness)", applied)
	}

	out := buf.String()
	for _, want := range []string{"Absent", "not found", "Connected", "already connected", "Fresh", "✓ connected", "Broken", "permission"} {
		if !strings.Contains(out, want) {
			t.Errorf("install output missing %q:\n%s", want, out)
		}
	}
}

func TestRunInstall_NoHarnessDetected(t *testing.T) {
	var buf bytes.Buffer
	hs := []harness{{name: "Absent", detect: func() bool { return false }}}

	if runInstall(&buf, hs) {
		t.Error("runInstall = true, want false when nothing is detected")
	}
}

func TestHarnesses_OpenCodeRoundTrip(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)

	// Simulate an installed OpenCode with no config yet.
	ocDir := filepath.Join(xdg, "opencode")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	oc := harnesses(home)[0]
	if !oc.detect() {
		t.Fatal("OpenCode was not detected although its config dir exists")
	}
	if ok, _ := oc.check(); ok {
		t.Fatal("check = true before install")
	}
	if err := oc.apply(); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if ok, err := oc.check(); err != nil || !ok {
		t.Errorf("check after apply = %v, %v; want true, nil", ok, err)
	}
	if _, err := os.Stat(filepath.Join(ocDir, "opencode.json")); err != nil {
		t.Errorf("opencode.json was not created: %v", err)
	}
}

func TestStatus(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("PATH", t.TempDir()) // hide `claude` so no CLI is shelled out to

	var buf bytes.Buffer
	if err := Status(&buf); err != nil {
		t.Fatalf("Status: %v", err)
	}

	out := buf.String()
	for _, want := range []string{"OpenCode", "Claude Code", "Cursor", "not installed"} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

// TestCheck_ReturnsStructuredStates exercises the Check() function with a
// controlled HOME/XDG so no real harnesses are detected.
func TestCheck_ReturnsStructuredStates(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("PATH", t.TempDir())

	states := Check()
	if states == nil {
		t.Fatal("Check() = nil, want a slice")
	}
	if len(states) != 3 {
		t.Errorf("Check() returned %d states, want 3 (OpenCode, Claude Code, Cursor)", len(states))
	}
	for _, st := range states {
		if st.Name == "" {
			t.Error("state with empty name")
		}
		if st.Installed {
			t.Errorf("%s: Installed = true, want false (temp dir has no harness)", st.Name)
		}
		if st.Connected {
			t.Errorf("%s: Connected = true, want false", st.Name)
		}
		if st.Err != nil {
			t.Errorf("%s: Err = %v, want nil when not installed", st.Name, st.Err)
		}
	}
}

// TestCheck_DetectsInstalledOpenCode simulates an installed OpenCode harness
// by creating its config dir and verifies Check() marks it installed.
func TestCheck_DetectsInstalledOpenCode(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("PATH", t.TempDir())

	// Create OpenCode's config directory (detect = true).
	ocDir := filepath.Join(xdg, "opencode")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	states := Check()
	if len(states) != 3 {
		t.Fatalf("Check() returned %d states, want 3", len(states))
	}
	for _, st := range states {
		if st.Name == "OpenCode" {
			if !st.Installed {
				t.Error("OpenCode: Installed = false, want true (dir exists)")
			}
			if st.Connected {
				t.Error("OpenCode: Connected = true, want false (no mcp.ouija entry)")
			}
			return
		}
	}
	t.Error("OpenCode not found in Check() states")
}

// readJSON parses a JSON file into a generic map for assertions.
func readJSON(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var v map[string]any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("parse %s: %v\n%s", path, err, raw)
	}
	return v
}

// `"mcp": null` in a harness config used to panic (assignment to a nil map).
func TestJSONAddServerNullContainer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "opencode.json")
	if err := os.WriteFile(path, []byte(`{"mcp": null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := jsonAddServer(path, "mcp", "ouija", map[string]any{"type": "local"}); err != nil {
		t.Fatal(err)
	}
	if ok, err := jsonHasServer(path, "mcp", "ouija"); err != nil || !ok {
		t.Fatalf("server not added: %v %v", ok, err)
	}
}
