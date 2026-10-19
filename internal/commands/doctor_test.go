package commands

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/discorgento/ouija/internal/ouijaconfig"

	_ "github.com/discorgento/ouija/totems" // doctor reports the registered totems
)

func TestReport_Counters(t *testing.T) {
	var buf bytes.Buffer
	r := &report{w: &buf}
	r.ok("a", "fine")
	r.warn("b", "careful")
	r.fail("c", "broken")

	if len(r.warnings) != 1 || len(r.problems) != 1 {
		t.Fatalf("warnings = %d, problems = %d; want 1 and 1", len(r.warnings), len(r.problems))
	}
	out := buf.String()
	for _, want := range []string{"✓ a", "! b", "✗ c"} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestReport_Finish(t *testing.T) {
	var buf bytes.Buffer
	r := &report{w: &buf}

	if err := r.finish(); err != nil {
		t.Errorf("finish() with nothing = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "all good") {
		t.Errorf("finish() output = %q, want 'all good'", buf.String())
	}

	// A warning must not fail the run.
	buf.Reset()
	r.warn("x", "just a warning")
	if err := r.finish(); err != nil {
		t.Errorf("finish() with warnings = %v, want nil", err)
	}
	if !strings.Contains(buf.String(), "1 warning") {
		t.Errorf("finish() output = %q, want the warning count", buf.String())
	}

	// A problem must fail the run.
	buf.Reset()
	r.fail("p", "really broken")
	if err := r.finish(); err == nil {
		t.Error("finish() with a problem = nil, want an error")
	}
	if !strings.Contains(buf.String(), "1 problem") {
		t.Errorf("finish() output = %q, want the problem count", buf.String())
	}
}

func TestKeyNames(t *testing.T) {
	c := &ouijaconfig.Config{APIKeys: map[string]string{"openai": "x", "anthropic": "y", "empty": ""}}

	got := keyNames(c)
	if len(got) != 2 || got[0] != "anthropic" || got[1] != "openai" {
		t.Errorf("keyNames = %v, want [anthropic openai] (sorted, blank dropped)", got)
	}
}

// isolateDoctor points every path doctor reads (home, config, PATH) at temp
// dirs and chdirs somewhere config-free, so the checks never touch the real
// machine and never discover this repo's .ouija/config.json.
func isolateDoctor(t *testing.T) (configDir string) {
	t.Helper()

	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("AppData", xdg)
	t.Setenv("PATH", t.TempDir()) // hide `claude` so no CLI is spawned
	t.Chdir(t.TempDir())

	return filepath.Join(xdg, "ouija")
}

func TestDoctor_Configured(t *testing.T) {
	configDir := isolateDoctor(t)

	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	cfg := `{"apiKeys":{"opencode":"sk-test"},"defaults":{"provider":"opencode","model":"big-pickle"}}`
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	var buf bytes.Buffer
	if err := Doctor(&buf); err != nil {
		t.Fatalf("Doctor() = %v, want nil\n%s", err, buf.String())
	}

	out := buf.String()
	for _, want := range []string{"opencode (key set)", "totems", "valid", "no problems"} {
		if !strings.Contains(out, want) {
			t.Errorf("report is missing %q:\n%s", want, out)
		}
	}
}

func TestDoctor_NothingConfigured(t *testing.T) {
	isolateDoctor(t) // no config written

	var buf bytes.Buffer
	err := Doctor(&buf)
	if err == nil {
		t.Fatalf("Doctor() = nil, want an error when no key is configured\n%s", buf.String())
	}

	out := buf.String()
	if !strings.Contains(out, "ouija setup") {
		t.Errorf("report should point at `ouija setup`:\n%s", out)
	}
	if !strings.Contains(out, "problem") {
		t.Errorf("report should mention the problem:\n%s", out)
	}
}

func TestDoctor_ReportsHarnesses(t *testing.T) {
	isolateDoctor(t)

	var buf bytes.Buffer
	_ = Doctor(&buf)

	if !strings.Contains(buf.String(), "harnesses") {
		t.Errorf("report is missing the harness section:\n%s", buf.String())
	}
}
