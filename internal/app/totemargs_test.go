package app

import (
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseTotemArgs(t *testing.T) {
	args, flags, err := ParseTotemArgs("task", []string{"fix it", "--set", "depth=2", "--json", "--config", "c.json", "--trace-file", "t.json"}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if args.Task != "fix it" || args.Params["task"] != "fix it" || args.Params["depth"] != float64(2) {
		t.Fatalf("args = %+v", args)
	}
	if !flags.JSON || flags.ConfigPath != "c.json" || flags.TraceFile != "t.json" {
		t.Fatalf("flags = %+v", flags)
	}

	// A stray argument is a usage error, never silently dropped
	// (`ouija task "a" "b"` used to lose "b").
	var stderr strings.Builder
	if _, _, err := ParseTotemArgs("task", []string{"a", "b"}, &stderr); !errors.Is(err, ErrUsage) ||
		!strings.Contains(stderr.String(), `"b"`) {
		t.Fatalf("err = %v, stderr = %q; want a usage error naming the stray argument", err, stderr.String())
	}
	if _, _, err := ParseTotemArgs("task", []string{"--nope"}, io.Discard); !errors.Is(err, ErrUsage) {
		t.Fatalf("unknown flag: err = %v; want ErrUsage", err)
	}
	if args, _, err := ParseTotemArgs("sticker", nil, io.Discard); err != nil || args.Task != "" {
		t.Fatalf("no arguments: %+v, %v", args, err)
	}
}

// A broken discovered config is an error, not a silent fallback to the
// parent's or the global config.
func TestBrokenProjectConfigIsAnError(t *testing.T) {
	isolateGlobalConfig(t)
	dir := t.TempDir()
	writeProjectConfig(t, filepath.Join(dir, ".ouija", "config.json"), `{"provider": "ollama",`)
	t.Chdir(dir)
	if _, _, err := DiscoverConfig(""); err == nil || !strings.Contains(err.Error(), "config.json") {
		t.Fatalf("DiscoverConfig with a broken config = %v; want an error naming the file", err)
	}
}
