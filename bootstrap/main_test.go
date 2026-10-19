package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/registry"
)

// echoTotem returns its inputs, so Main's parsing is observable.
type echoTotem struct{}

func (echoTotem) Name() string    { return "bootstrap-echo" }
func (echoTotem) Summary() string { return "echoes its inputs" }
func (echoTotem) Run(_ context.Context, _ *engine.Engine, a registry.Args) (any, error) {
	var in struct {
		Depth int `json:"depth"`
	}
	if err := a.Decode(&in); err != nil {
		return nil, err
	}
	if a.Task == "fail" {
		return nil, errors.New("asked to fail")
	}
	return map[string]any{"task": a.Task, "depth": in.Depth}, nil
}

func init() { registry.Register(echoTotem{}) }

func runMain(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	cfg := writeConfig(t, map[string]any{"provider": "ollama", "model": "llama3", "projectRoot": t.TempDir()})
	var stdout, stderr strings.Builder
	argv := append([]string{"my-totem"}, args...)
	if len(args) > 0 && args[0] != "mcp" && args[0] != "help" {
		argv = append(argv, "--config", cfg)
	}
	code := run(context.Background(), argv, strings.NewReader(""), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestMainRunsATotem(t *testing.T) {
	code, out, errOut := runMain(t, "bootstrap-echo", "the task", "--set", "depth=2", "--json")
	if code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut)
	}
	if !strings.Contains(out, `"task": "the task"`) || !strings.Contains(out, `"depth": 2`) {
		t.Fatalf("stdout = %s", out)
	}
}

func TestMainExitCodes(t *testing.T) {
	if code, out, _ := runMain(t); code != 0 || !strings.Contains(out, "bootstrap-echo") {
		t.Errorf("no args: exit %d, stdout %q; want the totem list", code, out)
	}
	if code, _, errOut := runMain(t, "no-such-totem"); code != 1 || !strings.Contains(errOut, "unknown totem") {
		t.Errorf("unknown totem: exit %d, %q", code, errOut)
	}
	if code, _, errOut := runMain(t, "bootstrap-echo", "a", "b"); code != 2 || !strings.Contains(errOut, "unexpected argument") {
		t.Errorf("stray argument: exit %d, %q", code, errOut)
	}
	if code, _, errOut := runMain(t, "bootstrap-echo"); code != 1 || !strings.Contains(errOut, "missing the task") {
		t.Errorf("missing task: exit %d, %q", code, errOut)
	}
	if code, _, errOut := runMain(t, "bootstrap-echo", "fail"); code != 1 || !strings.Contains(errOut, "asked to fail") {
		t.Errorf("failing totem: exit %d, %q", code, errOut)
	}
}

// `<bin> mcp` takes --config and refuses anything else (it ignored both).
func TestMCPArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run(context.Background(), []string{"bin", "mcp", "install"}, strings.NewReader(""), &stdout, &stderr); code != 2 {
		t.Fatalf("mcp install: exit %d, want 2 (usage)", code)
	}
	stderr.Reset()
	code := run(context.Background(), []string{"bin", "mcp", "--config", filepath.Join(t.TempDir(), "missing.json")}, strings.NewReader(""), &stdout, &stderr)
	if code != 1 || !strings.Contains(stderr.String(), "missing.json") {
		t.Fatalf("mcp --config <missing>: exit %d, stderr %q; want the path honoured (and refused)", code, stderr.String())
	}
}
