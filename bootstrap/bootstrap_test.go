package bootstrap

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig drops a minimal project config and returns its path.
func writeConfig(t *testing.T, cfg map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSetup_BuildsEngineFromConfig(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"provider":    "ollama",
		"model":       "llama3",
		"projectRoot": t.TempDir(),
	})

	app, err := Setup(context.Background(), WithConfigPath(path))
	if err != nil {
		t.Fatalf("Setup() = %v", err)
	}
	if app.Engine == nil {
		t.Fatal("Setup() returned a nil engine")
	}
	if app.Config.Provider != "ollama" {
		t.Errorf("Config.Provider = %q, want ollama", app.Config.Provider)
	}
	if app.ConfigPath != path {
		t.Errorf("ConfigPath = %q, want %q", app.ConfigPath, path)
	}
	app.Engine.Close()
}

func TestSetup_InvalidConfigFails(t *testing.T) {
	path := writeConfig(t, map[string]any{
		"provider": "no-such-provider",
	})
	if _, err := Setup(context.Background(), WithConfigPath(path)); err == nil {
		t.Fatal("Setup() with an unknown provider = nil, want an error")
	}
}

func TestSetup_MissingFileFails(t *testing.T) {
	if _, err := Setup(context.Background(), WithConfigPath(filepath.Join(t.TempDir(), "absent.json"))); err == nil {
		t.Fatal("Setup() with a missing config file = nil, want an error")
	}
}

// The MCP identity must default to the embedder's choice, never leak "ouija".
func TestServeMCP_ReportsCustomName(t *testing.T) {
	app, err := Setup(context.Background(), WithName("meu-agente"))
	if err != nil {
		// Without any config layer the defaults still resolve a provider, so
		// Setup should succeed; skip rather than fake a passing assertion if
		// the environment has no HOME (config dir lookup fails).
		t.Skipf("environment cannot resolve a default config: %v", err)
	}
	defer app.Engine.Close()

	in := strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}` + "\n")
	var out strings.Builder
	if err := app.ServeMCP(context.Background(), in, &out); err != nil {
		t.Fatalf("ServeMCP() = %v", err)
	}
	if !strings.Contains(out.String(), `"name":"meu-agente"`) {
		t.Errorf("initialize result does not report the custom name:\n%s", out.String())
	}
	if strings.Contains(out.String(), `"name":"ouija"`) {
		t.Errorf("initialize result leaks the default ouija identity:\n%s", out.String())
	}
}
