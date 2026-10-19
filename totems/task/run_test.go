package task

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/enginetest"
	"github.com/discorgento/ouija/registry"
)

// firstUser is the request's first user message (the run's prompt).
func firstUser(req engine.ChatRequest) string {
	for _, m := range req.Messages {
		if m.Role == engine.RoleUser {
			return m.Content
		}
	}
	return ""
}

// The whole séance on a scripted model: scout, every pass × dimension in
// parallel (one failing), reconcile with the surviving analyses, and the plan
// saved when the model does not call write_plan itself.
func TestRunOrchestration(t *testing.T) {
	root := t.TempDir()
	var failed atomic.Bool
	p := &enginetest.Provider{Respond: func(req engine.ChatRequest) enginetest.Reply {
		prompt := firstUser(req)
		switch {
		case strings.Contains(req.System, "reconnaissance") && !strings.Contains(req.System, "SCOUT CONTEXT"):
			return enginetest.Submit(map[string]any{"summary": "calc pkg", "conventions": []any{}, "relevantCoreAreas": []any{},
				"filesOfInterest": []any{"calc.go"}, "openQuestions": []any{}})
		case strings.HasPrefix(prompt, "PASS"):
			if failed.CompareAndSwap(false, true) {
				return enginetest.Reply{Err: errors.New("one pass fails")}
			}
			return enginetest.Submit(map[string]any{"dimension": "d", "findings": []any{}, "steps": []any{"guard empty slice"}, "risks": []any{}})
		case strings.HasPrefix(req.System, "You reconcile"):
			if !strings.Contains(prompt, "guard empty slice") {
				t.Errorf("reconcile did not get the analyses:\n%s", prompt)
			}
			return enginetest.Submit(map[string]any{"taskDescription": "Make Avg safe",
				"checklist":      []any{map[string]any{"item": "handle empty", "category": "core"}},
				"plan":           []any{map[string]any{"step": "guard", "detail": "calc.go: return 0, err"}},
				"highConfidence": []any{}, "needsHumanDecision": []any{}, "openQuestions": []any{}})
		}
		t.Errorf("unexpected request: system=%.60q prompt=%.60q", req.System, prompt)
		return enginetest.Reply{Err: errors.New("unexpected")}
	}}
	eng := enginetest.NewEngine(p, root)

	out, err := registry.Invoke(context.Background(), eng, &Totem{}, registry.Args{Task: "Avg panics on empty input"})
	if err != nil {
		t.Fatal(err)
	}
	res, ok := out.(*taskResult)
	if !ok || res.TaskDescription != "Make Avg safe" || len(res.Checklist) != 1 {
		t.Fatalf("result = %#v", out)
	}
	if n := len(p.Requests()); n != 1+3*4+1 {
		t.Errorf("%d model calls, want scout + 12 passes + reconcile", n)
	}
	plan, err := os.ReadFile(filepath.Join(root, "docs", "task-plan.md"))
	if err != nil || !strings.Contains(string(plan), "Make Avg safe") {
		t.Fatalf("plan not saved by the fallback: %v", err)
	}
}

// Every pass failing is an error, not an empty plan.
func TestRunAllPassesFail(t *testing.T) {
	p := &enginetest.Provider{Respond: func(req engine.ChatRequest) enginetest.Reply {
		if strings.HasPrefix(firstUser(req), "PASS") {
			return enginetest.Reply{Err: errors.New("no key")}
		}
		return enginetest.Submit(map[string]any{"summary": "x", "conventions": []any{}, "relevantCoreAreas": []any{},
			"filesOfInterest": []any{}, "openQuestions": []any{}})
	}}
	_, err := registry.Invoke(context.Background(), enginetest.NewEngine(p, t.TempDir()), &Totem{}, registry.Args{Task: "x"})
	if err == nil || !strings.Contains(err.Error(), "all 12 analysis passes failed") {
		t.Fatalf("err = %v", err)
	}
}

// A write_plan that fails (empty content) does not count as saved: the
// fallback writes the plan.
func TestRunFallbackAfterFailedWritePlan(t *testing.T) {
	root := t.TempDir()
	p := &enginetest.Provider{Respond: func(req engine.ChatRequest) enginetest.Reply {
		switch {
		case strings.HasPrefix(firstUser(req), "PASS"):
			return enginetest.Submit(map[string]any{"dimension": "d", "findings": []any{}, "steps": []any{}, "risks": []any{}})
		case strings.HasPrefix(req.System, "You reconcile"):
			return enginetest.Reply{ToolCalls: []engine.ToolCall{
				{ID: "w", Name: "write_plan", Input: map[string]any{}}, // fails: no content
				{ID: "s", Name: engine.SubmitTool, Input: map[string]any{"taskDescription": "Make Avg safe",
					"checklist": []any{}, "plan": []any{}, "highConfidence": []any{}, "needsHumanDecision": []any{}, "openQuestions": []any{}}},
			}}
		}
		return enginetest.Submit(map[string]any{"summary": "x", "conventions": []any{}, "relevantCoreAreas": []any{},
			"filesOfInterest": []any{}, "openQuestions": []any{}})
	}}
	if _, err := registry.Invoke(context.Background(), enginetest.NewEngine(p, root), &Totem{}, registry.Args{Task: "x"}); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(filepath.Join(root, "docs", "task-plan.md")); err != nil || !strings.Contains(string(b), "Make Avg safe") {
		t.Fatalf("plan not saved after a failed write_plan: %v", err)
	}
}
