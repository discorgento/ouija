package task

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/discorgento/ouija/internal/engine"
	"github.com/discorgento/ouija/internal/knowledge"
	"github.com/discorgento/ouija/internal/registry"
	"github.com/discorgento/ouija/internal/schema"
)

func init() { registry.Register(&Totem{}) }

// Totem is the "task" séance: scout → N passes × dimensions → reconcile.
type Totem struct{}

func (t *Totem) Name() string { return "task" }
func (t *Totem) Summary() string {
	return "Analyze a task: scout -> N passes x dimensions -> reconcile -> plan"
}
func (t *Totem) Init(_ context.Context, _ *engine.Engine, _ *engine.Config) error { return nil }
func (t *Totem) Cleanup() error                                                   { return nil }

type passResult struct {
	Pass      int
	Dimension string
	JSON      string
}

func (t *Totem) Run(ctx context.Context, eng *engine.Engine, args registry.Args) (any, error) {
	if strings.TrimSpace(args.Task) == "" {
		return nil, fmt.Errorf("empty task: pass the description as the first argument")
	}
	cfg := eng.Config()
	rules := "- " + strings.Join(cfg.Rules, "\n- ")
	dims := cfg.Dimensions
	if len(dims) == 0 {
		dims = []string{"correctness", "best-practices", "security", "performance"}
	}

	// 1) Scout — map the current reality with read-only tools.
	scoutSystem := "You are doing code reconnaissance (scout). Project rules:\n" + rules
	if known := knowledge.Load("."); known != "" {
		scoutSystem += "\n\n## Known project facts (from previous analyses)\n" + known
	}
	scoutPrompt := fmt.Sprintf(
		"TASK: %s\n\nRead the relevant code with the tools (read_file/grep/glob/list_dir) "+
			"and map the current reality. Do NOT propose a solution. When done, call submit_result.",
		args.Task)
	scoutResult, err := eng.Run(ctx, engine.RunRequest{
		Label: "scout", System: scoutSystem, Prompt: scoutPrompt,
		UseTools: true, Schema: scoutSchema(), MaxTokens: 4096,
	})
	if err != nil {
		return nil, fmt.Errorf("scout failed: %w", err)
	}
	scoutJSON := scoutResult.Text

	// Stable, cacheable prefix shared by every pass.
	sharedSystem := scoutSystem + "\n\nSCOUT CONTEXT:\n" + scoutJSON

	// 2) N passes × dimensions, all independent (self-consistency).
	var jobs []func() (passResult, error)
	for p := 1; p <= cfg.Passes; p++ {
		for _, d := range dims {
			p, d := p, d
			jobs = append(jobs, func() (passResult, error) {
				prompt := fmt.Sprintf(
					"PASS %d (INDEPENDENT analysis). TASK: %s\n\nAnalyze along the %q dimension. "+
						"Confirm claims in the code before asserting (use the tools). When done, call submit_result.",
					p, args.Task, d)
				pr, rerr := eng.Run(ctx, engine.RunRequest{
					Label: fmt.Sprintf("p%d:%s", p, d), System: sharedSystem, Prompt: prompt,
					UseTools: true, Schema: dimensionSchema(), MaxTokens: 4096,
				})
				if rerr != nil {
					return passResult{}, rerr
				}
				return passResult{Pass: p, Dimension: d, JSON: pr.Text}, nil
			})
		}
	}
	results, pErr := engine.Parallel(ctx, 8, jobs)

	// Bail out if every pass failed (bad key, budget ceiling, etc.).
	ok := 0
	for _, r := range results {
		if r.JSON != "" {
			ok++
		}
	}
	if ok == 0 {
		if pErr != nil {
			return nil, fmt.Errorf("all %d analysis passes failed: %w", len(jobs), pErr)
		}
		return nil, fmt.Errorf("all %d analysis passes failed (check provider/key/budget)", len(jobs))
	}
	if pErr != nil {
		fmt.Fprintf(os.Stderr, "[ouija] warning: %d/%d passes failed (%v)\n", len(jobs)-ok, len(jobs), pErr)
	}

	// 3) Reconcile — the séance consensus across passes.
	var sb strings.Builder
	for _, r := range results {
		if r.JSON == "" {
			continue
		}
		fmt.Fprintf(&sb, "{\"pass\":%d,\"dimension\":%q,\"analysis\":%s}\n", r.Pass, r.Dimension, r.JSON)
	}
	recSystem := "You reconcile independent analyses (self-consistency). Project rules:\n" + rules
	recPrompt := fmt.Sprintf(
		"TASK: %s\n\nSCOUT CONTEXT:\n%s\n\nANALYSES (%d passes x dimensions):\n%s\n\n"+
			"What is consistent across the passes goes to highConfidence; what diverges goes to needsHumanDecision. "+
			"Deliver taskDescription + checklist + a step-by-step plan with files and a validation check per step.\n\n"+
			"First, call the write_plan tool with the rendered plan markdown to save it to docs/task-plan.md. "+
			"Then call submit_result with the structured JSON result.",
		args.Task, scoutJSON, cfg.Passes, sb.String())
	recResult, err := eng.Run(ctx, engine.RunRequest{
		Label: "reconcile", System: recSystem, Prompt: recPrompt,
		ExtraTools: []engine.CustomTool{{Def: writePlanDef, Fn: writePlanFn}},
		Schema:     finalSchema(), MaxTokens: 8192,
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile failed: %w", err)
	}

	var res taskResult
	if err := json.Unmarshal([]byte(recResult.Text), &res); err != nil {
		return recResult.Text, nil
	}

	// Fallback: only when the model didn't call write_plan itself.
	if !slices.Contains(recResult.Tools, "write_plan") {
		writePlan(".", &res)
	}

	return &res, nil
}

// writePlan saves the rendered plan as docs/task-plan.md in the given directory.
func writePlan(dir string, res *taskResult) {
	content := res.Render()
	docsDir := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		return
	}
	dst := filepath.Join(docsDir, "task-plan.md")
	if err := os.WriteFile(dst, []byte(content), 0o644); err == nil {
		fmt.Fprintf(os.Stderr, "[ouija] plan saved to %s\n", dst)
	}
}

// taskResult is the typed reconcile output. It implements registry.Renderer.
type taskResult struct {
	TaskDescription string `json:"taskDescription"`
	Checklist       []struct {
		Item     string `json:"item"`
		Category string `json:"category"`
	} `json:"checklist"`
	Plan []struct {
		Step   string `json:"step"`
		Detail string `json:"detail"`
		Risk   string `json:"risk"`
	} `json:"plan"`
	HighConfidence     []string `json:"highConfidence"`
	NeedsHumanDecision []string `json:"needsHumanDecision"`
	OpenQuestions      []string `json:"openQuestions"`
}

// Render produces a readable markdown summary for the terminal.
func (r *taskResult) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Task\n\n%s\n\n", r.TaskDescription)

	if len(r.Checklist) > 0 {
		b.WriteString("## Checklist\n\n")
		for _, c := range r.Checklist {
			fmt.Fprintf(&b, "- [ ] (%s) %s\n", c.Category, c.Item)
		}
		b.WriteString("\n")
	}
	if len(r.Plan) > 0 {
		b.WriteString("## Plan\n\n")
		for i, p := range r.Plan {
			fmt.Fprintf(&b, "%d. **%s** — %s\n", i+1, p.Step, p.Detail)
			if strings.TrimSpace(p.Risk) != "" {
				fmt.Fprintf(&b, "   ⚠️  %s\n", p.Risk)
			}
		}
		b.WriteString("\n")
	}
	if len(r.HighConfidence) > 0 {
		b.WriteString("## High confidence (stable across passes)\n\n")
		for _, s := range r.HighConfidence {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("\n")
	}
	if len(r.NeedsHumanDecision) > 0 {
		b.WriteString("## ⚠️  Needs a developer decision\n\n")
		for _, s := range r.NeedsHumanDecision {
			fmt.Fprintf(&b, "- %s\n", s)
		}
		b.WriteString("\n")
	}
	if len(r.OpenQuestions) > 0 {
		b.WriteString("## Open questions\n\n")
		for _, s := range r.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", s)
		}
	}
	return b.String()
}

// ─── write_plan tool ─────────────────────────────────────────────────────

var writePlanDef = engine.ToolDef{
	Name:        "write_plan",
	Description: "Save the rendered task plan as markdown to docs/task-plan.md",
	Schema:      writePlanSchema(),
	Risk:        engine.RiskReadOnly, // sandboxado — SafeJoin impede escrita fora do root
}

var writePlanFn engine.ToolFn = func(_ context.Context, root string, input map[string]any) engine.ToolResult {
	content, _ := input["content"].(string)
	if content == "" {
		return engine.ToolResult{Error: &engine.ToolError{Code: "INVALID_INPUT", Message: "write_plan: content is required"}}
	}
	dst, err := engine.SafeJoin(root, "docs/task-plan.md")
	if err != nil {
		return engine.ToolResult{Error: &engine.ToolError{Code: "INVALID_INPUT", Message: err.Error()}}
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return engine.ToolResult{Error: &engine.ToolError{Code: "INTERNAL", Message: err.Error()}}
	}
	if err := os.WriteFile(dst, []byte(content), 0o644); err != nil {
		return engine.ToolResult{Error: &engine.ToolError{Code: "INTERNAL", Message: err.Error()}}
	}
	fmt.Fprintf(os.Stderr, "[ouija] plan saved to %s\n", dst)
	return engine.ToolResult{Data: "plan saved to " + dst}
}

func writePlanSchema() map[string]any {
	return schema.Obj(map[string]any{
		"content": schema.Str("the full rendered plan in markdown"),
	}, "content")
}

// ─── Schemas ─────────────────────────────────────────────────────────────

func scoutSchema() map[string]any {
	return schema.Obj(map[string]any{
		"summary":           schema.Str("what the scope does today"),
		"conventions":       schema.Arr("conventions/patterns already used"),
		"relevantCoreAreas": schema.Arr("relevant core areas"),
		"filesOfInterest":   schema.Arr("files the task likely touches"),
		"openQuestions":     schema.Arr("ambiguities needing a human decision"),
	}, "summary", "conventions", "relevantCoreAreas", "filesOfInterest", "openQuestions")
}

func dimensionSchema() map[string]any {
	finding := schema.Obj(map[string]any{
		"point":     schema.Str("the finding"),
		"rationale": schema.Str("why it matters; cite a file/class"),
		"severity":  map[string]any{"type": "string", "enum": []string{"info", "low", "medium", "high"}},
	}, "point", "rationale", "severity")
	return schema.Obj(map[string]any{
		"dimension": schema.Str("dimension name"),
		"findings":  map[string]any{"type": "array", "items": finding},
		"steps":     schema.Arr("step-by-step implementation"),
		"risks":     schema.Arr("risks"),
	}, "dimension", "findings", "steps", "risks")
}

func finalSchema() map[string]any {
	checkItem := schema.Obj(map[string]any{
		"item":     schema.Str("actionable item"),
		"category": map[string]any{"type": "string", "enum": []string{"core", "best-practices", "security", "performance", "general"}},
	}, "item", "category")
	planStep := schema.Obj(map[string]any{
		"step":   schema.Str("the step"),
		"detail": schema.Str("what to do, in which files, and the validation check"),
		"risk":   schema.Str("risk (optional)"),
	}, "step", "detail")
	return schema.Obj(map[string]any{
		"taskDescription":    schema.Str("rewritten, scoped description"),
		"checklist":          map[string]any{"type": "array", "items": checkItem},
		"plan":               map[string]any{"type": "array", "items": planStep},
		"highConfidence":     schema.Arr("points stable across passes"),
		"needsHumanDecision": schema.Arr("divergent points / developer decision"),
		"openQuestions":      schema.Arr("open questions"),
	}, "taskDescription", "checklist", "plan", "highConfidence", "needsHumanDecision", "openQuestions")
}
