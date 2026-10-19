package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/discorgento/ouija/internal/engine"
	"github.com/discorgento/ouija/internal/knowledge"
	"github.com/discorgento/ouija/internal/version"
)

// isolateConfig points the global (~/.config/ouija) lookup at a temp dir so the
// CLI tests never read the developer's real config or API keys.
func isolateConfig(t *testing.T) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir)
	t.Setenv("AppData", dir)
}

// runCLI invokes run() with buffers and returns exit code, stdout, stderr.
func runCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(append([]string{"ouija"}, args...), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func TestRun_HelpVariants(t *testing.T) {
	for _, args := range [][]string{{}, {"help"}, {"-h"}, {"--help"}} {
		code, stdout, _ := runCLI(t, args...)
		if code != 0 {
			t.Errorf("run(%v) = %d, want 0", args, code)
		}
		for _, want := range []string{"ouija init", "ouija chat", "--trace-file"} {
			if !strings.Contains(stdout, want) {
				t.Errorf("run(%v) stdout is missing %q", args, want)
			}
		}
	}
}

func TestRun_VersionVariants(t *testing.T) {
	want := "ouija " + version.Version

	for _, flag := range []string{"version", "-v", "--version"} {
		code, stdout, _ := runCLI(t, flag)
		if code != 0 {
			t.Errorf("run(%s) = %d, want 0", flag, code)
		}
		if got := strings.TrimSpace(stdout); got != want {
			t.Errorf("run(%s) stdout = %q, want %q", flag, got, want)
		}
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	code, _, stderr := runCLI(t, "definitely-not-a-totem")

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "unknown totem") {
		t.Errorf("stderr = %q, want it to name the unknown totem", stderr)
	}
}

// A bad flag on a real totem must exit 2 (catching flag.Parse) without ever
// reaching the engine, so this needs no network.
func TestRun_BadFlagOnTotem(t *testing.T) {
	isolateConfig(t)

	code, _, _ := runCLI(t, "task", "--not-a-real-flag")
	if code != 2 {
		t.Errorf("exit code = %d, want 2 for a bad flag", code)
	}
}

func TestRun_LearnWritesKnowledge(t *testing.T) {
	isolateConfig(t)
	t.Chdir(t.TempDir())

	code, _, _ := runCLI(t, "learn", "uses", "Go", "modules")
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := knowledge.Load("."); !strings.Contains(got, "uses Go modules") {
		t.Errorf("knowledge file = %q, want the learned fact", got)
	}
}

func TestRun_GenerateUsageError(t *testing.T) {
	isolateConfig(t)

	code, _, stderr := runCLI(t, "generate")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "usage") {
		t.Errorf("stderr = %q, want a usage error", stderr)
	}
}

// init needs a global config and must fail before opening any interactive form.
func TestRun_InitWithoutGlobalConfig(t *testing.T) {
	isolateConfig(t)

	code, _, stderr := runCLI(t, "init")
	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr, "ouija setup") {
		t.Errorf("stderr = %q, want it to point at `ouija setup`", stderr)
	}
}

func TestCmdChat_MissingConfigReturnsError(t *testing.T) {
	isolateConfig(t)

	var stderr bytes.Buffer
	code := cmdChat([]string{"--config", filepath.Join(t.TempDir(), "nope.json")}, &stderr)

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "error") {
		t.Errorf("stderr = %q, want an error message", stderr.String())
	}
}

func TestRunCommand_MapsErrorToExitCode(t *testing.T) {
	var stderr bytes.Buffer

	if code := runCommand(func([]string) error { return nil }, nil, &stderr); code != 0 {
		t.Errorf("runCommand(success) = %d, want 0", code)
	}

	code := runCommand(func([]string) error { return errors.New("boom") }, nil, &stderr)
	if code != 1 {
		t.Errorf("runCommand(failure) = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "boom") {
		t.Errorf("stderr = %q, want the error text", stderr.String())
	}
}

func TestErrorHint(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"auth", engine.ErrAuth("no key"), "ouija setup"},
		{"config", engine.ErrConfig("bad"), "ouija doctor"},
		{"provider", engine.ErrProvider("unknown"), "ouija doctor"},
		{"budget", engine.ErrBudget("over"), "budget.maxTokens"},
		{"plain", errors.New("boom"), ""},
		{"wrapped auth", fmt.Errorf("run: %w", engine.ErrAuth("no key")), "ouija setup"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := errorHint(tt.err)
			if tt.want == "" {
				if got != "" {
					t.Errorf("errorHint() = %q, want empty", got)
				}
				return
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("errorHint() = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

func TestRunCommand_PrintsHintForTypedError(t *testing.T) {
	var stderr bytes.Buffer
	code := runCommand(func([]string) error { return engine.ErrAuth("no key") }, nil, &stderr)

	if code != 1 {
		t.Errorf("runCommand() = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "hint:") || !strings.Contains(stderr.String(), "ouija setup") {
		t.Errorf("stderr = %q, want an actionable hint", stderr.String())
	}
}

func TestCmdMcp_UnknownSubcommand(t *testing.T) {
	code, _, stderr := runCLI(t, "mcp", "bogus")

	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "unknown mcp subcommand") {
		t.Errorf("stderr = %q, want it to list the subcommands", stderr)
	}
}

// status and install must work with a controlled HOME: they detect harnesses,
// report, and (for install) write the config, without shelling out to any CLI.
func TestCmdMcp_InstallThenStatus(t *testing.T) {
	home := t.TempDir()
	xdg := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", xdg)
	t.Setenv("PATH", t.TempDir()) // hide `claude` so no real CLI is spawned

	// Pretend OpenCode is installed (its config dir exists) but unconfigured.
	ocDir := filepath.Join(xdg, "opencode")
	if err := os.MkdirAll(ocDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	code, stdout, stderr := runCLI(t, "mcp", "status")
	if code != 0 {
		t.Fatalf("status exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "OpenCode") || !strings.Contains(stdout, "not connected") {
		t.Errorf("status stdout = %q, want OpenCode reported as not connected", stdout)
	}

	code, stdout, stderr = runCLI(t, "mcp", "install")
	if code != 0 {
		t.Fatalf("install exit code = %d, want 0 (stderr: %s)", code, stderr)
	}
	if !strings.Contains(stdout, "connected") {
		t.Errorf("install stdout = %q, want a connection report", stdout)
	}
	if _, err := os.Stat(filepath.Join(ocDir, "opencode.json")); err != nil {
		t.Errorf("install did not write the OpenCode config: %v", err)
	}

	_, stdout, _ = runCLI(t, "mcp", "status")
	if !strings.Contains(stdout, "✓ connected") {
		t.Errorf("status after install = %q, want OpenCode connected", stdout)
	}
}

// doctor must run with a controlled HOME and exit non-zero when nothing is
// configured, without shelling out to any CLI.
func TestRun_Doctor(t *testing.T) {
	isolateConfig(t)
	t.Setenv("PATH", t.TempDir()) // hide `claude`
	t.Chdir(t.TempDir())          // no project config to discover

	code, stdout, _ := runCLI(t, "doctor")
	if code != 1 {
		t.Errorf("exit code = %d, want 1 when no API key is configured", code)
	}
	if !strings.Contains(stdout, "ouija doctor") {
		t.Errorf("stdout = %q, want the doctor report", stdout)
	}
	if !strings.Contains(stdout, "ouija setup") {
		t.Errorf("stdout = %q, want it to point at `ouija setup`", stdout)
	}
}
