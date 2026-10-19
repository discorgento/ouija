package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/discorgento/ouija/internal/atomicfile"
)

// binName is the command harnesses launch; serverArgs is the subcommand that
// starts the server, so a harness runs `ouija mcp`.
const binName = "ouija"

var serverArgs = []string{"mcp"}

// harness is one MCP-capable agent we know how to configure. Fields are funcs so
// the detection and registration can be faked in tests.
type harness struct {
	name   string
	detect func() bool          // is this agent installed on the machine?
	check  func() (bool, error) // is ouija already registered with it?
	apply  func() error         // register ouija (idempotent)
}

// harnesses returns the agents we support, with paths derived from home.
func harnesses(home string) []harness {
	ocPath := opencodeConfig(home)
	curPath := cursorConfig(home)

	return []harness{
		{
			name:   "OpenCode",
			detect: func() bool { return dirExists(filepath.Dir(ocPath)) },
			check:  func() (bool, error) { return jsonHasServer(ocPath, "mcp", defaultName) },
			apply:  func() error { return jsonAddServer(ocPath, "mcp", defaultName, opencodeEntry()) },
		},
		{
			name:   "Claude Code",
			detect: func() bool { _, err := exec.LookPath("claude"); return err == nil },
			check:  func() (bool, error) { return claudeHasServer(), nil },
			apply:  claudeAdd,
		},
		{
			name:   "Cursor",
			detect: func() bool { return dirExists(filepath.Dir(curPath)) },
			check:  func() (bool, error) { return jsonHasServer(curPath, "mcpServers", defaultName) },
			apply:  func() error { return jsonAddServer(curPath, "mcpServers", defaultName, cursorEntry()) },
		},
	}
}

// opencodeConfig returns OpenCode's global config path, honouring XDG_CONFIG_HOME.
func opencodeConfig(home string) string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "opencode", "opencode.json")
}

// cursorConfig returns Cursor's global MCP config path.
func cursorConfig(home string) string {
	return filepath.Join(home, ".cursor", "mcp.json")
}

// opencodeEntry is the value OpenCode stores unter its "mcp" key.
func opencodeEntry() map[string]any {
	return map[string]any{
		"type":    "local",
		"command": append([]string{binName}, serverArgs...),
		"enabled": true,
	}
}

// cursorEntry is the value Cursor stores under "mcpServers".
func cursorEntry() map[string]any {
	return map[string]any{
		"command": binName,
		"args":    serverArgs,
	}
}

// Install registers ouija with every harness found on this machine and prints a
// per-harness report. Absent or already-configured harnesses are left untouched.
func Install(w io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}

	fmt.Fprintln(w, "🔌  connecting ouija to your agent harnesses")
	fmt.Fprintln(w)

	if !runInstall(w, harnesses(home)) {
		fmt.Fprintln(w, "  (no known harness found — see `ouija mcp status` for manual setup)")
	}

	fmt.Fprintln(w, "\nrestart the harness to pick up the new tools.")
	return nil
}

// HarnessState is the diagnosed state of one MCP-capable harness.
type HarnessState struct {
	Name      string
	Installed bool // is the harness present on this machine?
	Connected bool // is ouija registered with it?
	Err       error
}

// Check reports the state of every supported harness, in a stable order; nil
// when the home directory (where every harness keeps its config) is unknown.
func Check() []HarnessState {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	return checkIn(home)
}

func checkIn(home string) []HarnessState {
	hs := harnesses(home)
	states := make([]HarnessState, 0, len(hs))
	for _, h := range hs {
		st := HarnessState{Name: h.name, Installed: h.detect()}
		if st.Installed {
			st.Connected, st.Err = h.check()
		}
		states = append(states, st)
	}
	return states
}

// Status reports, for every supported harness, whether it is installed and
// whether ouija is registered with it.
func Status(w io.Writer) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("locate home directory: %w", err)
	}

	fmt.Fprintln(w, "🔌  ouija mcp — harness status")
	fmt.Fprintln(w)

	for _, st := range checkIn(home) {
		switch {
		case !st.Installed:
			fmt.Fprintf(w, "  %-12s – not installed\n", st.Name)
		case st.Err != nil:
			fmt.Fprintf(w, "  %-12s ✗ %v\n", st.Name, st.Err)
		case st.Connected:
			fmt.Fprintf(w, "  %-12s ✓ connected\n", st.Name)
		default:
			fmt.Fprintf(w, "  %-12s – not connected  (run: ouija mcp install)\n", st.Name)
		}
	}
	return nil
}

// runInstall walks the harnesses and reports each outcome. It returns whether
// any harness was detected.
func runInstall(w io.Writer, hs []harness) bool {
	anyDetected := false
	for _, h := range hs {
		if !h.detect() {
			fmt.Fprintf(w, "  %-12s – not found, skipped\n", h.name)
			continue
		}
		anyDetected = true

		registered, err := h.check()
		if err != nil {
			fmt.Fprintf(w, "  %-12s ✗ %v\n", h.name, err)
			continue
		}
		if registered {
			fmt.Fprintf(w, "  %-12s ✓ already connected\n", h.name)
			continue
		}
		if err := h.apply(); err != nil {
			fmt.Fprintf(w, "  %-12s ✗ %v\n", h.name, err)
			continue
		}
		fmt.Fprintf(w, "  %-12s ✓ connected\n", h.name)
	}
	return anyDetected
}

// --- Claude Code (driven through its CLI, which owns ~/.claude.json) ---

func claudeHasServer() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := exec.CommandContext(ctx, "claude", "mcp", "list").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), defaultName+":") {
			return true
		}
	}
	return false
}

func claudeAdd() error {
	args := append([]string{"mcp", "add", "--scope", "user", defaultName, "--", binName}, serverArgs...)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if out, err := exec.CommandContext(ctx, "claude", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// --- JSON config editing (OpenCode, Cursor) ---

// jsonAddServer adds name→entry inside the container object of the JSON file at
// path, creating the file or the container when absent. Other keys are kept
// verbatim (through json.RawMessage, so numbers are not coerced). A .bak copy of
// the original is written before the first change. Adding an existing server is
// a no-op.
func jsonAddServer(path, container, name string, entry map[string]any) error {
	root := map[string]json.RawMessage{}

	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &root); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
	case os.IsNotExist(err):
		// Start from an empty document.
	default:
		return err
	}

	servers := map[string]json.RawMessage{}
	if c, ok := root[container]; ok {
		if err := json.Unmarshal(c, &servers); err != nil {
			return fmt.Errorf("parse %s: %q is not an object: %w", path, container, err)
		}
	}
	if _, exists := servers[name]; exists {
		return nil
	}

	entryJSON, err := json.Marshal(entry)
	if err != nil {
		return err
	}
	servers[name] = entryJSON

	containerJSON, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	root[container] = containerJSON

	compact, err := json.Marshal(root)
	if err != nil {
		return err
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		return err
	}
	pretty.WriteByte('\n')

	if err := backupFile(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Atomic, keeping the file's mode: this is another tool's config (often a
	// large one, like ~/.claude.json), and a crash mid-write must not corrupt it.
	return atomicfile.Write(path, pretty.Bytes(), 0o644, true)
}

// jsonHasServer reports whether name is present in the container object of the
// JSON file at path. A missing file or container simply means "not present".
func jsonHasServer(path, container, name string) (bool, error) {
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	var root map[string]json.RawMessage
	if err := json.Unmarshal(raw, &root); err != nil {
		return false, fmt.Errorf("parse %s: %w", path, err)
	}
	c, ok := root[container]
	if !ok {
		return false, nil
	}
	var servers map[string]json.RawMessage
	if err := json.Unmarshal(c, &servers); err != nil {
		return false, fmt.Errorf("parse %s: %q is not an object: %w", path, container, err)
	}
	_, ok = servers[name]
	return ok, nil
}

// backupFile copies path to path+".bak" once, so the pre-ouija original is kept.
func backupFile(path string) error {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	bak := path + ".bak"
	if _, err := os.Stat(bak); err == nil {
		return nil // keep the first backup untouched
	}
	return os.WriteFile(bak, data, 0o600)
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
