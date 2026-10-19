package app

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/discorgento/ouija/registry"
)

// ErrUsage is a command-line mistake already reported on stderr (exit 2).
var ErrUsage = errors.New("usage error")

// TotemFlags are the flags every totem command line takes.
type TotemFlags struct {
	ConfigPath string // --config
	TraceFile  string // --trace-file
	JSON       bool   // --json
}

// ParseTotemArgs parses what follows the totem name — `[task] [--set
// key=value]… [--json] [--config file] [--trace-file file]` — for the ouija
// CLI and bootstrap.Main alike. The task is the first argument when it is not
// a flag; any other stray argument is a usage error rather than silently
// dropped (quote the task; pass inputs as --set key=value).
func ParseTotemArgs(name string, argv []string, stderr io.Writer) (registry.Args, TotemFlags, error) {
	task, rest := "", argv
	if len(rest) > 0 && !strings.HasPrefix(rest[0], "-") {
		task, rest = rest[0], rest[1:]
	}
	var f TotemFlags
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.StringVar(&f.ConfigPath, "config", "", "path to a JSON config")
	fs.StringVar(&f.TraceFile, "trace-file", "", "path to write a Perfetto JSON trace")
	fs.BoolVar(&f.JSON, "json", false, "print the raw JSON result")
	params := registry.ParamsFlag{}
	var schema map[string]any
	if t, ok := registry.Get(name); ok {
		schema = registry.InputSchema(t)
	}
	fs.Var(&registry.TypedParams{Values: params, Schema: schema}, "set", "named input key=value (repeatable; typed by the totem's inputs, JSON for the rest)")
	if err := fs.Parse(rest); err != nil {
		return registry.Args{}, f, ErrUsage // flag already printed why
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "unexpected argument %q (quote the task, pass inputs as --set key=value)\n", fs.Arg(0))
		return registry.Args{}, f, ErrUsage
	}
	if task != "" {
		params["task"] = task
	}
	return registry.Args{Task: task, Params: params}, f, nil
}
