package registry

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"

	"github.com/discorgento/ouija/engine"
)

// Args is a totem's input.
type Args struct {
	// Task is the free-text request: the CLI's first argument, MCP's "task".
	Task string
	// Params are the named inputs — CLI `--set key=value`, the MCP arguments —
	// shaped by the totem's InputSchema. "task" is in Params too when set.
	Params map[string]any
}

// Decode copies Params into v, a pointer to a struct with json tags, so a
// totem reads its inputs typed: `var in struct{ Depth int `json:"depth"` }`.
func (a Args) Decode(v any) error {
	b, err := json.Marshal(a.Params)
	if err != nil {
		return fmt.Errorf("params: %w", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("params: %w", err)
	}
	return nil
}

// Totem is one focused autonomous workflow harness that runs on top of the
// engine. Run it through Invoke, which also drives the optional Initializer
// and Cleaner hooks.
type Totem interface {
	Name() string
	Summary() string
	Run(ctx context.Context, eng *engine.Engine, args Args) (any, error)
}

// Initializer is implemented by a totem that needs setup before Run (the
// project config is eng.Config()). An Init error aborts the run.
type Initializer interface {
	Init(ctx context.Context, eng *engine.Engine) error
}

// Cleaner is implemented by a totem that needs teardown. Cleanup runs after
// Run, and after a failed Init too.
type Cleaner interface {
	Cleanup() error
}

// InputSchemer is implemented by a totem with inputs beyond the free-text
// task. InputSchema returns the JSON Schema object of its arguments: the MCP
// tool's inputSchema, and what Invoke checks required inputs against. A
// "task" string property maps to Args.Task.
type InputSchemer interface {
	InputSchema() map[string]any
}

// Hints describe what a totem does to the project, for an MCP host deciding
// whether to ask before calling it (the MCP tool annotations). They are hints
// for the host, not a security boundary: the engine's gate still decides what
// each tool call may do, whatever a totem claims.
type Hints struct {
	// ReadOnly: the totem changes nothing in the project (it may still call
	// a model provider over the network).
	ReadOnly bool
}

// Hinter is implemented by a totem that describes itself to MCP hosts. A
// totem without it gets no annotations, which MCP hosts read as "may change
// things": the cautious default.
type Hinter interface {
	Hints() Hints
}

// DefaultInputSchema is the input of a totem without InputSchema: a required
// free-text task.
func DefaultInputSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{"type": "string", "description": "the task/request to work on"},
		},
		"required": []string{"task"},
	}
}

// InputSchema returns t's argument schema: its own, or DefaultInputSchema.
func InputSchema(t Totem) map[string]any {
	if s, ok := t.(InputSchemer); ok {
		if schema := s.InputSchema(); schema != nil {
			return schema
		}
	}
	return DefaultInputSchema()
}

// Invoke runs t through its lifecycle: it checks the required inputs of its
// InputSchema, then Init (if t is an Initializer), Run, and Cleanup (if t is
// a Cleaner — always, also after a failed Init). Hosts run totems through it
// instead of calling Run, so every entrypoint behaves the same.
//
// One registered value serves every call — the MCP server runs calls
// concurrently. A totem with Init or Cleanup keeps per-run state in its
// struct, so its invocations are serialized (overlapping, one call closed
// another's file); a totem without them runs concurrently and must not keep
// per-run state. A panic in the totem is returned as an error, so one bad
// call cannot take a long-running host down.
func Invoke(ctx context.Context, eng *engine.Engine, t Totem, args Args) (result any, err error) {
	// A "task" given as a parameter (--set task=…, an MCP argument) is the
	// task: the required check accepted it, but Run saw an empty Args.Task.
	if s, ok := args.Params["task"].(string); ok && strings.TrimSpace(args.Task) == "" {
		args.Task = s
	}
	if err := checkRequired(InputSchema(t), args); err != nil {
		return nil, fmt.Errorf("%s: %w", t.Name(), err)
	}
	_, hasInit := t.(Initializer)
	_, hasCleanup := t.(Cleaner)
	if hasInit || hasCleanup {
		mu := stateLock(t.Name())
		mu.Lock()
		defer mu.Unlock()
	}
	defer func() {
		if r := recover(); r != nil {
			result, err = nil, fmt.Errorf("%s: panic: %v", t.Name(), r)
		}
	}()
	if c, ok := t.(Cleaner); ok {
		defer func() {
			if cerr := c.Cleanup(); cerr != nil && err == nil {
				err = fmt.Errorf("cleanup: %w", cerr)
			}
		}()
	}
	if in, ok := t.(Initializer); ok {
		if err := in.Init(ctx, eng); err != nil {
			return nil, fmt.Errorf("init: %w", err)
		}
	}
	return t.Run(ctx, eng, args)
}

var (
	stateLocksMu sync.Mutex
	stateLocks   = map[string]*sync.Mutex{}
)

// stateLock is the mutex serializing a stateful totem's invocations.
func stateLock(name string) *sync.Mutex {
	stateLocksMu.Lock()
	defer stateLocksMu.Unlock()
	if stateLocks[name] == nil {
		stateLocks[name] = &sync.Mutex{}
	}
	return stateLocks[name]
}

// checkRequired reports the first input the schema requires that args lacks.
func checkRequired(schema map[string]any, args Args) error {
	var required []string
	switch r := schema["required"].(type) {
	case []string:
		required = r
	case []any:
		for _, v := range r {
			if s, ok := v.(string); ok {
				required = append(required, s)
			}
		}
	}
	for _, name := range required {
		if name == "task" && strings.TrimSpace(args.Task) != "" {
			continue
		}
		if v, ok := args.Params[name]; ok && v != nil && v != "" {
			continue
		}
		if name == "task" {
			return errors.New("missing the task (first argument)")
		}
		return fmt.Errorf("missing required input %q (--set %s=…)", name, name)
	}
	return nil
}

// ParamsFlag is a flag.Value that collects repeated `--set key=value` flags
// into Params. A value that parses as JSON (3, true, ["a","b"]) is kept as
// that JSON value; anything else is a string.
type ParamsFlag map[string]any

func (p ParamsFlag) String() string {
	parts := make([]string, 0, len(p))
	for k, v := range p {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}
	slices.Sort(parts)
	return strings.Join(parts, ",")
}

// Set parses one key=value.
func (p ParamsFlag) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok || strings.TrimSpace(k) == "" {
		return fmt.Errorf("want key=value, got %q", s)
	}
	var parsed any
	if err := json.Unmarshal([]byte(v), &parsed); err == nil {
		p[k] = parsed
	} else {
		p[k] = v
	}
	return nil
}

// TypedParams collects `--set key=value` like ParamsFlag, typing each value by
// the totem's InputSchema: a property declared "string" keeps the text as
// typed — `--set ticket=1234` became the number 1234, which a string field
// then refused to decode, so the same totem worked over MCP and failed on the
// CLI. Other or undeclared keys are read as JSON, as ParamsFlag does.
type TypedParams struct {
	Values ParamsFlag
	Schema map[string]any
}

func (p *TypedParams) String() string { return p.Values.String() }

// Set parses one key=value.
func (p *TypedParams) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if ok && propertyType(p.Schema, k) == "string" {
		p.Values[k] = v
		return nil
	}
	return p.Values.Set(s)
}

// propertyType is the "type" a schema declares for a top-level property.
func propertyType(schema map[string]any, key string) string {
	props, _ := schema["properties"].(map[string]any)
	prop, _ := props[key].(map[string]any)
	t, _ := prop["type"].(string)
	return t
}

// Renderer is an optional interface a totem result can implement to control
// its human-facing terminal output (the CLI falls back to JSON otherwise).
type Renderer interface {
	Render() string
}

// Render turns a totem result into text: its Render() when it is a Renderer,
// else indented JSON. A result JSON cannot encode (a NaN, a channel) is shown
// with the reason, never as an empty string; Format returns that error.
func Render(v any) string {
	s, err := Format(v)
	if err != nil {
		return fmt.Sprintf("(result not encodable as JSON: %v)\n%+v", err, v)
	}
	return s
}

// Format is Render that reports a result JSON cannot encode, so a host can
// fail instead of printing nothing and exiting 0.
func Format(v any) (string, error) {
	if r, ok := v.(Renderer); ok {
		return r.Render(), nil
	}
	b, err := json.MarshalIndent(v, "", "  ")
	return string(b), err
}

var totems = map[string]Totem{}

// legacyInitializer is the Init signature from before Initializer: a totem
// that still has it would compile and then silently skip its setup.
type legacyInitializer interface {
	Init(context.Context, *engine.Engine, *engine.Config) error
}

// Register adds a totem to the registry (called from each totem's init()).
// It panics on a totem whose Init has the old (ctx, eng, cfg) signature, so the
// migration fails loudly at startup instead of silently skipping setup.
func Register(t Totem) {
	if _, ok := t.(legacyInitializer); ok {
		panic(fmt.Sprintf("registry: totem %q: Init(ctx, eng, cfg) is the old signature — "+
			"use Init(ctx context.Context, eng *engine.Engine) error (the config is eng.Config())", t.Name()))
	}
	totems[t.Name()] = t
}

// Get resolves a totem by name.
func Get(name string) (Totem, bool) {
	t, ok := totems[name]
	return t, ok
}

// List returns all registered totems, sorted by name.
func List() []Totem {
	names := slices.Sorted(maps.Keys(totems))
	out := make([]Totem, 0, len(names))
	for _, n := range names {
		out = append(out, totems[n])
	}
	return out
}

// Help renders the list of registered totems ("summons the available
// spirits") — one line per totem, name + summary. It is deliberately generic:
// a host that embeds its own totems gets its own list, with no ouija CLI
// assumptions baked in.
func Help() string {
	var b strings.Builder
	b.WriteString("◆ Totems\n")
	for _, n := range slices.Sorted(maps.Keys(totems)) {
		fmt.Fprintf(&b, "      %-8s %s\n", n, totems[n].Summary())
	}
	return b.String()
}
