package engine

import (
	"context"
	"iter"
	"sync"
	"testing"
)

// slowMock answers every call with plain text; it is safe for concurrent use.
type slowMock struct{ name string }

func (m *slowMock) Name() string               { return m.name }
func (m *slowMock) Capabilities() Capabilities { return Capabilities{} }
func (m *slowMock) Chat(ctx context.Context, req ChatRequest) iter.Seq2[StreamEvent, error] {
	return func(yield func(StreamEvent, error) bool) {
		if !yield(StreamEvent{Type: EventText, Text: "ok from " + req.Model}, nil) {
			return
		}
		yield(StreamEvent{Type: EventDone}, nil)
	}
}

// Setters may run while runs are in progress (the chat switches model and
// permissions mid-session; totems run passes concurrently). Run with -race.
func TestEngineSettersRaceFreeWithRuns(t *testing.T) {
	eng := newTestEngine(&slowMock{name: "a"}, t.TempDir())
	pc := NewDefaultPermissionConfig()
	eng.SetPermissionConfig(pc)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := eng.Run(context.Background(), RunRequest{Prompt: "hi", UseTools: true}); err != nil {
					t.Errorf("run %d: %v", i, err)
					return
				}
			}
		}()
	}
	for i := range 50 {
		eng.SetModel("m" + string(rune('a'+i%26)))
		eng.SetProvider(&slowMock{name: "b"})
		eng.SetTokenCallback(func(string) {})
		eng.SetToolCallback(func(string, map[string]any) {})
		eng.SetLogger(nil)
		pc.SetTool("bash", Level(i%3))
		pc.Levels()
		eng.AddAllowedCommand("git status")
		_ = eng.PermissionConfig()
	}
	wg.Wait()
}

// A run sees the state of when it started: SetModel during a run applies
// from the next run on.
func TestRunUsesSnapshotOfModel(t *testing.T) {
	eng := newTestEngine(&slowMock{name: "a"}, t.TempDir())
	eng.SetModel("first")
	res, err := eng.Run(context.Background(), RunRequest{Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "ok from first" {
		t.Fatalf("got %q", res.Text)
	}
	eng.SetModel("second")
	res, _ = eng.Run(context.Background(), RunRequest{Prompt: "hi"})
	if res.Text != "ok from second" {
		t.Fatalf("got %q after SetModel", res.Text)
	}
}

// recordingAgent is a custom loop strategy plugged in through EngineConfig.
type recordingAgent struct{ env RunEnv }

func (a *recordingAgent) Run(_ context.Context, env RunEnv, req RunRequest, msgs []Message) (*RunResult, []Message, error) {
	a.env = env
	if err := env.Budget.Add(Usage{InputTokens: 1, OutputTokens: 1}); err != nil {
		return nil, msgs, err
	}
	res := env.RunTool(context.Background(), req.Label, ToolCall{ID: "1", Name: "list_dir", Input: map[string]any{"path": "."}}, nil, nil)
	return &RunResult{Text: "custom:" + env.Model + ":" + res.String()[:0]}, msgs, nil
}

// EngineConfig.Agent replaces the loop strategy, and the agent gets a full
// RunEnv: provider, model, gated tools, the shared budget and a logger.
func TestCustomAgent(t *testing.T) {
	cfg := DefaultConfig()
	cfg.ProjectRoot = t.TempDir()
	cfg.Model = "m1"
	agent := &recordingAgent{}
	eng := New(EngineConfig{Provider: &slowMock{name: "p"}, Config: cfg, Tracer: NewTracer(false), Agent: agent})

	res, err := eng.Run(context.Background(), RunRequest{Prompt: "hi", Label: "x"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Text != "custom:m1:" {
		t.Fatalf("custom agent not used: %q", res.Text)
	}
	env := agent.env
	if env.Provider.Name() != "p" || env.Tools == nil || env.RunTool == nil || env.Logger == nil || env.Config != cfg {
		t.Fatalf("incomplete RunEnv: %+v", env)
	}
	if tokens, steps, _ := eng.Spent(); tokens != 2 || steps != 1 {
		t.Fatalf("budget not shared with the custom agent: tokens=%d steps=%d", tokens, steps)
	}
}
