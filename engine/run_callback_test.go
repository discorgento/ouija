package engine_test

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/enginetest"
)

// The run callback hears each Run start and end, with the label and how it
// ended — what the CLI draws a totem's progress from.
func TestRunCallback(t *testing.T) {
	fail := errors.New("boom")
	p := &enginetest.Provider{Respond: func(req engine.ChatRequest) enginetest.Reply {
		if len(req.Messages) > 0 && req.Messages[0].Content == "fail" {
			return enginetest.Reply{Err: fail}
		}
		return enginetest.Reply{Text: "ok"}
	}}
	eng := enginetest.NewEngine(p, t.TempDir())
	var mu sync.Mutex
	var events []engine.RunEvent
	eng.SetRunCallback(func(ev engine.RunEvent) {
		mu.Lock()
		events = append(events, ev)
		mu.Unlock()
	})

	if _, err := eng.Run(context.Background(), engine.RunRequest{Label: "scout", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Run(context.Background(), engine.RunRequest{Label: "p1", Prompt: "fail"}); err == nil {
		t.Fatal("the failing run succeeded")
	}
	if len(events) != 4 {
		t.Fatalf("events = %+v, want start+end of two runs", events)
	}
	if events[0].Label != "scout" || events[0].Done || !events[1].Done || events[1].Err != nil {
		t.Errorf("scout events = %+v %+v", events[0], events[1])
	}
	if events[3].Label != "p1" || !events[3].Done || events[3].Err == nil {
		t.Errorf("p1 end = %+v, want done with the error", events[3])
	}
}
