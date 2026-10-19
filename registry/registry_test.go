package registry

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/discorgento/ouija/engine"
)

type fakeTotem struct{ name, summary string }

func (f *fakeTotem) Name() string                                           { return f.name }
func (f *fakeTotem) Summary() string                                        { return f.summary }
func (f *fakeTotem) Run(context.Context, *engine.Engine, Args) (any, error) { return nil, nil }

func TestRegisterAndGet(t *testing.T) {
	const name = "test-register-and-get"
	Register(&fakeTotem{name: name, summary: "a test totem"})

	got, ok := Get(name)
	if !ok {
		t.Fatalf("Get(%q) = false after Register", name)
	}
	if got.Summary() != "a test totem" {
		t.Errorf("Summary() = %q, want a test totem", got.Summary())
	}

	if _, ok := Get("test-does-not-exist"); ok {
		t.Error("Get(unknown) = true, want false")
	}
}

// Registering the same name twice must not duplicate it in List(); the name
// order is user-visible through Help().
func TestRegister_DuplicateKeepsSingleEntry(t *testing.T) {
	const name = "test-duplicate"
	Register(&fakeTotem{name: name, summary: "first"})
	Register(&fakeTotem{name: name, summary: "second"})

	count := 0
	for _, tt := range List() {
		if tt.Name() == name {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("%q appears %d times in List(), want 1", name, count)
	}
	got, ok := Get(name)
	if !ok {
		t.Fatalf("Get(%q) = false", name)
	}
	if got.Summary() != "second" {
		t.Errorf("Get() returned the stale totem: summary = %q, want second", got.Summary())
	}
}

func TestList_SortedByName(t *testing.T) {
	Register(&fakeTotem{name: "zzz-test-sort", summary: "z"})
	Register(&fakeTotem{name: "aaa-test-sort", summary: "a"})

	list := List()
	for i := 1; i < len(list); i++ {
		if list[i-1].Name() > list[i].Name() {
			t.Fatalf("List() not sorted: %q before %q", list[i-1].Name(), list[i].Name())
		}
	}
}

// Help() is the binary's front door: it must list every registered totem and
// advertise the real flag names (regression: it used to advertise --trace,
// which does not exist — the flag is --trace-file).
func TestHelp_ListsTotemsAndFlags(t *testing.T) {
	Register(&fakeTotem{name: "test-help-listing", summary: "shows up in help"})

	help := Help()
	if !strings.Contains(help, "test-help-listing") {
		t.Error("Help() does not list a registered totem")
	}
	if !strings.Contains(help, "shows up in help") {
		t.Error("Help() does not show the totem summary")
	}
	if !strings.Contains(help, "◆ Totems") {
		t.Error("Help() should include the Totems section header")
	}
	if strings.Contains(help, "--trace ") {
		t.Error("Help() advertises --trace, which is not a real flag")
	}
}

// lifecycleTotem records which hooks Invoke drives, in order.
type lifecycleTotem struct {
	calls   []string
	initErr error
	runErr  error
	schema  map[string]any
}

func (l *lifecycleTotem) Name() string    { return "lifecycle" }
func (l *lifecycleTotem) Summary() string { return "" }
func (l *lifecycleTotem) Init(context.Context, *engine.Engine) error {
	l.calls = append(l.calls, "init")
	return l.initErr
}
func (l *lifecycleTotem) Run(_ context.Context, _ *engine.Engine, a Args) (any, error) {
	l.calls = append(l.calls, "run:"+a.Task)
	return "result", l.runErr
}
func (l *lifecycleTotem) Cleanup() error {
	l.calls = append(l.calls, "cleanup")
	return nil
}
func (l *lifecycleTotem) InputSchema() map[string]any { return l.schema }

func TestInvokeLifecycle(t *testing.T) {
	ok := &lifecycleTotem{}
	res, err := Invoke(context.Background(), nil, ok, Args{Task: "x"})
	if err != nil || res != "result" || strings.Join(ok.calls, ",") != "init,run:x,cleanup" {
		t.Fatalf("calls=%v res=%v err=%v", ok.calls, res, err)
	}

	failInit := &lifecycleTotem{initErr: errors.New("boom")}
	if _, err := Invoke(context.Background(), nil, failInit, Args{Task: "x"}); err == nil || !strings.Contains(err.Error(), "init: boom") {
		t.Fatalf("init error = %v", err)
	}
	if strings.Join(failInit.calls, ",") != "init,cleanup" {
		t.Fatalf("a failed Init must skip Run and still clean up: %v", failInit.calls)
	}

	// A totem with neither hook just runs.
	if _, err := Invoke(context.Background(), nil, &fakeTotem{name: "plain"}, Args{Task: "x"}); err != nil {
		t.Fatal(err)
	}
}

func TestInvokeChecksRequiredInputs(t *testing.T) {
	if _, err := Invoke(context.Background(), nil, &fakeTotem{name: "plain"}, Args{}); err == nil ||
		!strings.Contains(err.Error(), "missing the task") {
		t.Fatalf("default schema must require a task, got %v", err)
	}
	l := &lifecycleTotem{schema: map[string]any{"type": "object", "required": []any{"depth"}}}
	if _, err := Invoke(context.Background(), nil, l, Args{}); err == nil || !strings.Contains(err.Error(), `"depth"`) {
		t.Fatalf("missing depth: err = %v", err)
	}
	if len(l.calls) != 0 {
		t.Fatalf("hooks ran despite missing inputs: %v", l.calls)
	}
	if _, err := Invoke(context.Background(), nil, l, Args{Params: map[string]any{"depth": 2}}); err != nil {
		t.Fatal(err)
	}
	optional := &lifecycleTotem{schema: map[string]any{"type": "object", "required": []string{}}}
	if _, err := Invoke(context.Background(), nil, optional, Args{}); err != nil {
		t.Fatalf("a schema without required inputs must run with none: %v", err)
	}
}

func TestArgsDecode(t *testing.T) {
	var in struct {
		Depth int      `json:"depth"`
		Tags  []string `json:"tags"`
	}
	a := Args{Params: map[string]any{"depth": float64(3), "tags": []any{"a", "b"}}}
	if err := a.Decode(&in); err != nil || in.Depth != 3 || len(in.Tags) != 2 {
		t.Fatalf("Decode = %+v, %v", in, err)
	}
	if err := (Args{Params: map[string]any{"depth": "deep"}}).Decode(&in); err == nil {
		t.Fatal("a mistyped param must fail to decode")
	}
}

func TestParamsFlag(t *testing.T) {
	p := ParamsFlag{}
	for _, s := range []string{"depth=3", "dry=true", `tags=["a","b"]`, "name=hello world", "empty="} {
		if err := p.Set(s); err != nil {
			t.Fatalf("Set(%q): %v", s, err)
		}
	}
	if p["depth"] != float64(3) || p["dry"] != true || p["name"] != "hello world" || p["empty"] != "" {
		t.Fatalf("parsed = %#v", p)
	}
	if tags, _ := p["tags"].([]any); len(tags) != 2 {
		t.Fatalf("tags = %#v", p["tags"])
	}
	if err := p.Set("novalue"); err == nil {
		t.Fatal("a value without = must be rejected")
	}
}

type fakeRenderer struct{ text string }

func (f fakeRenderer) Render() string { return f.text }

func TestRender(t *testing.T) {
	if got := Render(fakeRenderer{text: "custom"}); got != "custom" {
		t.Errorf("Render(Renderer) = %q, want the Render() output", got)
	}
	if got := Render(map[string]any{"a": 1}); !strings.Contains(got, `"a": 1`) {
		t.Errorf("Render(non-renderer) = %q, want indented JSON", got)
	}
}

func TestInputSchemaDefault(t *testing.T) {
	s := InputSchema(&fakeTotem{name: "plain"})
	if r, _ := s["required"].([]string); len(r) != 1 || r[0] != "task" {
		t.Fatalf("default schema = %v", s)
	}
}

type legacyTotem struct{ fakeTotem }

func (legacyTotem) Init(context.Context, *engine.Engine, *engine.Config) error { return nil }

// A totem still on the old Init signature must fail at registration, not
// silently lose its setup.
func TestRegisterRejectsLegacyInit(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil || !strings.Contains(fmt.Sprint(r), "old signature") {
			t.Fatalf("Register(legacy) recovered %v; want a panic naming the old signature", r)
		}
	}()
	Register(&legacyTotem{fakeTotem{name: "legacy"}})
}

// statefulTotem keeps per-run state from Init to Cleanup.
type statefulTotem struct {
	active, overlaps int32
}

func (s *statefulTotem) Name() string    { return "stateful" }
func (s *statefulTotem) Summary() string { return "" }
func (s *statefulTotem) Init(context.Context, *engine.Engine) error {
	if atomic.AddInt32(&s.active, 1) > 1 {
		atomic.AddInt32(&s.overlaps, 1)
	}
	return nil
}
func (s *statefulTotem) Run(context.Context, *engine.Engine, Args) (any, error) {
	time.Sleep(time.Millisecond)
	return "ok", nil
}
func (s *statefulTotem) Cleanup() error { atomic.AddInt32(&s.active, -1); return nil }

// A totem with Init/Cleanup never runs two invocations at once (the MCP
// server calls one registered value concurrently).
func TestInvokeSerializesStatefulTotems(t *testing.T) {
	s := &statefulTotem{}
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() { _, _ = Invoke(context.Background(), nil, s, Args{}) })
	}
	wg.Wait()
	if s.overlaps != 0 {
		t.Fatalf("%d overlapping invocations", s.overlaps)
	}
}

type panicTotem struct{}

func (panicTotem) Name() string    { return "boom" }
func (panicTotem) Summary() string { return "" }
func (panicTotem) Run(context.Context, *engine.Engine, Args) (any, error) {
	panic("boom")
}

// A panic in a totem is an error, not a crash of the host.
func TestInvokeRecoversPanics(t *testing.T) {
	if _, err := Invoke(context.Background(), nil, panicTotem{}, Args{Task: "x"}); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("err = %v", err)
	}
}

type echoTotem struct{}

func (echoTotem) Name() string                                                 { return "echo" }
func (echoTotem) Summary() string                                              { return "" }
func (echoTotem) Run(_ context.Context, _ *engine.Engine, a Args) (any, error) { return a.Task, nil }

// A task given as a parameter reaches Args.Task.
func TestInvokeTaskFromParams(t *testing.T) {
	out, err := Invoke(context.Background(), nil, echoTotem{}, Args{Params: map[string]any{"task": "hello"}})
	if err != nil || out != "hello" {
		t.Fatalf("out = %v, err = %v", out, err)
	}
}

// A string input stays a string on the command line; others are JSON.
func TestTypedParams(t *testing.T) {
	schema := map[string]any{"properties": map[string]any{
		"ticket": map[string]any{"type": "string"}, "passes": map[string]any{"type": "integer"},
	}}
	p := &TypedParams{Values: ParamsFlag{}, Schema: schema}
	for _, s := range []string{"ticket=1234", "passes=2", "other=true"} {
		if err := p.Set(s); err != nil {
			t.Fatal(err)
		}
	}
	if p.Values["ticket"] != "1234" || p.Values["passes"] != float64(2) || p.Values["other"] != true {
		t.Fatalf("values = %#v", p.Values)
	}
}

// A result JSON cannot encode is an error from Format, and Render still says
// something instead of returning "".
func TestFormatUnencodable(t *testing.T) {
	v := map[string]any{"score": math.NaN()}
	if _, err := Format(v); err == nil {
		t.Fatal("Format accepted a NaN")
	}
	if Render(v) == "" {
		t.Fatal("Render returned an empty string")
	}
}
