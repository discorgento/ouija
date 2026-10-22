package review

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/knowledge"
	"github.com/discorgento/ouija/registry"
	"github.com/discorgento/ouija/schema"
	"github.com/discorgento/ouija/totems/internal/seance"
)

func init() { registry.Register(&Totem{}) }

// Totem is the "review" séance: the diff (read by Go) → find → a skeptic
// verifies each finding on its own → only what survives is reported.
type Totem struct{}

func (t *Totem) Name() string { return "review" }
func (t *Totem) Summary() string {
	return "Review the branch's changes: find problems -> verify each one -> report what holds"
}

// Hints tells MCP hosts that a review changes nothing: it reads the diff and
// the code, and only reports.
func (t *Totem) Hints() registry.Hints { return registry.Hints{ReadOnly: true} }

// InputSchema makes both inputs optional: `ouija review` alone reviews the
// branch against main.
func (t *Totem) InputSchema() map[string]any {
	return schema.Obj(map[string]any{
		"task": schema.Str("what to focus on, e.g. \"concurrency\" (optional)"),
		"base": schema.Str("branch or commit to compare against (default: main, else master)"),
	})
}

// maxDiff bounds the diff handed to the model; a bigger one is cut, and the
// model is told so (it can still read whole files with its tools).
const maxDiff = 120_000

// maxFindings bounds the verify passes: one model call each.
const maxFindings = 12

// finding is one problem the find step reports.
type finding struct {
	File     string `json:"file"`
	Line     string `json:"line"`
	Problem  string `json:"problem"`
	Severity string `json:"severity"`
	// Reason is the skeptic's verdict on it, filled by the verify step.
	Reason string `json:"reason,omitempty"`
}

func (t *Totem) Run(ctx context.Context, eng *engine.Engine, args registry.Args) (any, error) {
	var in struct {
		Base string `json:"base"`
	}
	if err := args.Decode(&in); err != nil {
		return nil, err
	}
	cfg := eng.Config()

	// 1) The changes — read by Go, not guessed by the model.
	base, err := resolveBase(ctx, cfg.ProjectRoot, in.Base)
	if err != nil {
		return nil, err
	}
	diff, err := branchDiff(ctx, cfg.ProjectRoot, base)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(diff) == "" {
		return &reviewResult{Base: base}, nil
	}
	if len(diff) > maxDiff {
		diff = diff[:maxDiff] + "\n\n[diff cut at 120 KB: read the remaining files with the tools]"
	}

	// The fixed instructions: how to review, the project's rules and facts.
	system := "You review code changes. Report only real problems a reviewer would block on: " +
		"bugs, unhandled errors, races, resource leaks, security holes, broken contracts. " +
		"Never style, naming or taste. Quote the exact line of each finding."
	if len(cfg.Rules) > 0 {
		system += "\n\nProject rules:\n- " + strings.Join(cfg.Rules, "\n- ")
	}
	if known := knowledge.Load(cfg.ProjectRoot); known != "" {
		system += "\n\n## Known project facts\n" + known
	}

	// 2) Find — the model reads the diff (and whole files, with the tools)
	// and answers in the findings schema, nothing else.
	focus := ""
	if f := strings.TrimSpace(args.Task); f != "" {
		focus = "FOCUS: " + f + "\n\n"
	}
	found, err := eng.Run(ctx, engine.RunRequest{
		Label: "find", System: system,
		Prompt: focus + "DIFF against " + base + ":\n" + diff +
			"\n\nRead the changed files where the diff is not enough. When done, call submit_result.",
		UseTools: true, Schema: findingsSchema(), MaxTokens: 4096,
	})
	if err != nil {
		return nil, fmt.Errorf("find failed: %w", err)
	}
	var out struct {
		Findings []finding `json:"findings"`
	}
	if err := json.Unmarshal([]byte(found.Text), &out); err != nil {
		return nil, fmt.Errorf("find: unreadable result: %w", err)
	}
	res := &reviewResult{Base: base, Focus: strings.TrimSpace(args.Task)}
	if len(out.Findings) == 0 {
		return res, nil
	}
	if len(out.Findings) > maxFindings {
		res.Unverified = len(out.Findings) - maxFindings
		out.Findings = out.Findings[:maxFindings]
	}

	// 3) Verify — every finding goes to a skeptic of its own, in parallel.
	// In a prompt this would be "please double-check"; here it always runs.
	type verdict struct {
		finding
		real bool
	}
	jobs := make([]func() (verdict, error), len(out.Findings))
	for i, f := range out.Findings {
		jobs[i] = func() (verdict, error) {
			v, err := eng.Run(ctx, engine.RunRequest{
				Label: fmt.Sprintf("verify %d", i+1),
				System: "You are a skeptical reviewer. A colleague reported the problem below. " +
					"Read the code with the tools and decide whether it is real. " +
					"Say no when it is a style point, when the code already handles it, or when you cannot confirm it.",
				Prompt: fmt.Sprintf("FILE: %s\nLINE: %s\nPROBLEM: %s\n\nWhen done, call submit_result.",
					f.File, f.Line, f.Problem),
				UseTools: true, Schema: verdictSchema(), MaxTokens: 2048,
			})
			if err != nil {
				return verdict{}, err
			}
			var r struct {
				Real   string `json:"real"`
				Reason string `json:"reason"`
			}
			if err := json.Unmarshal([]byte(v.Text), &r); err != nil {
				return verdict{}, fmt.Errorf("verify: unreadable result: %w", err)
			}
			f.Reason = r.Reason
			return verdict{finding: f, real: r.Real == "yes"}, nil
		}
	}
	verdicts, err := seance.Passes(ctx, eng, "verify passes", jobs, func(v verdict) bool { return v.File != "" })
	if err != nil {
		return nil, err
	}

	// 4) The decision is Go's: confirmed findings stay, the rest is listed
	// as dropped so the reader sees what the skeptic threw out.
	for _, v := range verdicts {
		if v.real {
			res.Confirmed = append(res.Confirmed, v.finding)
		} else {
			res.Dropped = append(res.Dropped, v.finding)
		}
	}
	res.Unverified += len(out.Findings) - len(verdicts)
	return res, nil
}

// baseRE is what a branch or commit given as base may look like: no leading
// dash, so it can never be read as a git option.
var baseRE = regexp.MustCompile(`^[A-Za-z0-9._/@{}^~][A-Za-z0-9._/@{}^~-]*$`)

// resolveBase checks the base the caller named, or picks main, else master.
func resolveBase(ctx context.Context, root, base string) (string, error) {
	if base = strings.TrimSpace(base); base != "" {
		if !baseRE.MatchString(base) {
			return "", fmt.Errorf("base %q is not a branch or commit name", base)
		}
		if _, err := git(ctx, root, "rev-parse", "--verify", "--quiet", base+"^{commit}"); err != nil {
			return "", fmt.Errorf("base %q not found in this repository", base)
		}
		return base, nil
	}
	for _, b := range []string{"main", "master"} {
		if _, err := git(ctx, root, "rev-parse", "--verify", "--quiet", b+"^{commit}"); err == nil {
			return b, nil
		}
	}
	return "", errors.New("no main or master branch: pass the base to compare against (--set base=<branch>)")
}

// branchDiff is everything the branch changes since it left base, committed
// or not.
func branchDiff(ctx context.Context, root, base string) (string, error) {
	out, err := git(ctx, root, "diff", "--no-ext-diff", "--no-textconv", "--no-color",
		"--ignore-submodules=dirty", "--merge-base", base, "--")
	if err != nil {
		return "", fmt.Errorf("git diff: %w", err)
	}
	return out, nil
}

// git runs git in root. A repository's own config can make git run commands
// (an external diff, a textconv filter, an fsmonitor hook, a clean/smudge
// filter driver its .gitattributes names), and a review is pointed at code
// someone else wrote: those are turned off. The diff itself turns off the
// external diff and textconv, and skips the work trees of submodules (where
// another config would apply); every filter driver any config defines is
// emptied here, so git reads files as they are.
func git(ctx context.Context, root string, args ...string) (string, error) {
	env, err := noFilters(ctx, root)
	if err != nil {
		return "", err
	}
	return runGit(ctx, root, env, args...)
}

// noFilters returns the environment with every configured filter driver
// overridden by an empty one: no clean, smudge or process command, and not
// required (a required filter that does not run is an error). The overrides
// go through GIT_CONFIG_COUNT/KEY/VALUE rather than "-c", which cannot carry
// a driver name holding "=".
func noFilters(ctx context.Context, root string) ([]string, error) {
	env := os.Environ()
	// Listing config runs no driver. Exit status 1 is "no such key".
	out, err := runGit(ctx, root, env, "config", "-z", "--name-only", "--get-regexp", `^filter\.`)
	if err != nil {
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return env, nil
		}
		return nil, fmt.Errorf("git config: %w", err)
	}
	seen := map[string]bool{}
	n, _ := strconv.Atoi(os.Getenv("GIT_CONFIG_COUNT"))
	for _, key := range strings.Split(out, "\x00") {
		// filter.<driver>.<setting>: the driver name may itself hold dots.
		i := strings.LastIndexByte(key, '.')
		if !strings.HasPrefix(key, "filter.") || i <= len("filter.") {
			continue
		}
		driver := key[:i]
		if seen[driver] {
			continue
		}
		seen[driver] = true
		for _, kv := range [][2]string{{"clean", ""}, {"smudge", ""}, {"process", ""}, {"required", "false"}} {
			env = append(env,
				fmt.Sprintf("GIT_CONFIG_KEY_%d=%s.%s", n, driver, kv[0]),
				fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", n, kv[1]))
			n++
		}
	}
	return append(env, "GIT_CONFIG_COUNT="+strconv.Itoa(n)), nil
}

func runGit(ctx context.Context, root string, env []string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", append([]string{
		"-c", "core.fsmonitor=false", "-c", "core.pager=cat",
	}, args...)...)
	cmd.Dir = root
	cmd.Env = env
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return "", err
	}
	return string(out), nil
}

// reviewResult is what the review reports. It implements registry.Renderer.
type reviewResult struct {
	Base       string    `json:"base"`
	Focus      string    `json:"focus,omitempty"`
	Confirmed  []finding `json:"confirmed"`
	Dropped    []finding `json:"dropped"`
	Unverified int       `json:"unverified,omitempty"`
}

// bySeverity returns the findings worst first, keeping the model's order
// within a severity.
func bySeverity(fs []finding) []finding {
	rank := func(s string) int {
		switch s {
		case "high":
			return 0
		case "medium":
			return 1
		case "low":
			return 2
		}
		return 3
	}
	out := slices.Clone(fs)
	slices.SortStableFunc(out, func(a, b finding) int { return rank(a.Severity) - rank(b.Severity) })
	return out
}

// Render produces a readable markdown summary for the terminal.
func (r *reviewResult) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Review against %s\n\n", r.Base)
	if r.Focus != "" {
		fmt.Fprintf(&b, "Focus: %s\n\n", r.Focus)
	}
	if len(r.Confirmed) == 0 && len(r.Dropped) == 0 {
		b.WriteString("Nothing to report.\n")
		return b.String()
	}
	fmt.Fprintf(&b, "%d confirmed · %d dropped by the skeptic\n\n", len(r.Confirmed), len(r.Dropped))
	if len(r.Confirmed) > 0 {
		b.WriteString("## Confirmed\n\n")
		for _, f := range bySeverity(r.Confirmed) {
			fmt.Fprintf(&b, "- **%s** `%s` — %s\n  `%s`\n", f.Severity, f.File, f.Problem, f.Line)
		}
		b.WriteString("\n")
	}
	if len(r.Dropped) > 0 {
		b.WriteString("## Dropped\n\n")
		for _, f := range r.Dropped {
			fmt.Fprintf(&b, "- `%s` — %s\n  skeptic: %s\n", f.File, f.Problem, f.Reason)
		}
		b.WriteString("\n")
	}
	if r.Unverified > 0 {
		fmt.Fprintf(&b, "%d more finding(s) were not verified (the cap is %d, or a verify pass failed).\n", r.Unverified, maxFindings)
	}
	return b.String()
}

// ─── Schemas ─────────────────────────────────────────────────────────────

func findingsSchema() map[string]any {
	item := schema.Obj(map[string]any{
		"file":     schema.Str("path of the file, as in the diff"),
		"line":     schema.Str("the offending line, quoted exactly"),
		"problem":  schema.Str("what is wrong and what it breaks"),
		"severity": schema.Enum("how bad it is", "high", "medium", "low"),
	}, "file", "line", "problem", "severity")
	return schema.Obj(map[string]any{
		"findings": map[string]any{"type": "array", "items": item},
	}, "findings")
}

func verdictSchema() map[string]any {
	return schema.Obj(map[string]any{
		"real":   schema.Enum("whether the problem is real", "yes", "no"),
		"reason": schema.Str("one sentence: why"),
	}, "real", "reason")
}
