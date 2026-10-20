package review

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/enginetest"
	"github.com/discorgento/ouija/registry"
)

var update = flag.Bool("update", false, "update golden files")

// firstUser is the request's first user message (the run's prompt).
func firstUser(req engine.ChatRequest) string {
	for _, m := range req.Messages {
		if m.Role == engine.RoleUser {
			return m.Content
		}
	}
	return ""
}

// run is git in dir, failing the test on error. The user's own git config is
// left out, so a test sees the same git everywhere.
func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// repo is a git repository with calc.go committed on main, then changed on
// a feature branch (committed) and in the working tree (not committed).
func repo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	root := t.TempDir()
	write := func(body string) {
		if err := os.WriteFile(filepath.Join(root, "calc.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(t, root, "init", "-q", "-b", "main")
	write("package calc\n\nfunc Div(a, b int) int { return 0 }\n")
	run(t, root, "add", ".")
	run(t, root, "commit", "-q", "-m", "init")
	run(t, root, "switch", "-q", "-c", "feature")
	write("package calc\n\nfunc Div(a, b int) int { return a / b }\n")
	run(t, root, "commit", "-q", "-am", "divide")
	write("package calc\n\nfunc Div(a, b int) int { return a / b }\n\nfunc Avg(xs []int) int { return Div(sum(xs), len(xs)) }\n")
	return root
}

// The whole séance on a scripted model: the diff reaches the find step, each
// finding gets a skeptic of its own, and only the confirmed one is reported.
func TestRunVerifiesEachFinding(t *testing.T) {
	root := repo(t)
	p := &enginetest.Provider{Respond: func(req engine.ChatRequest) enginetest.Reply {
		prompt := firstUser(req)
		switch {
		case strings.HasPrefix(req.System, "You review"):
			for _, want := range []string{"FOCUS: division", "return a / b", "func Avg"} {
				if !strings.Contains(prompt, want) {
					t.Errorf("find prompt lacks %q:\n%s", want, prompt)
				}
			}
			return enginetest.Submit(map[string]any{"findings": []any{
				map[string]any{"file": "calc.go", "line": "return a / b", "problem": "divide by zero when b is 0", "severity": "high"},
				map[string]any{"file": "calc.go", "line": "func Div(a, b int) int", "problem": "Div should be named Divide", "severity": "low"},
			}})
		case strings.HasPrefix(req.System, "You are a skeptical"):
			if strings.Contains(prompt, "divide by zero") {
				return enginetest.Submit(map[string]any{"real": "yes", "reason": "nothing guards b == 0"})
			}
			return enginetest.Submit(map[string]any{"real": "no", "reason": "a naming preference, not a bug"})
		}
		t.Errorf("unexpected request: system=%.60q", req.System)
		return enginetest.Reply{Err: errors.New("unexpected")}
	}}

	out, err := registry.Invoke(context.Background(), enginetest.NewEngine(p, root), &Totem{},
		registry.Args{Task: "division", Params: map[string]any{"task": "division"}})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(*reviewResult)
	if res.Base != "main" || len(res.Confirmed) != 1 || len(res.Dropped) != 1 {
		t.Fatalf("result = %+v", res)
	}
	if res.Confirmed[0].Severity != "high" || res.Dropped[0].Reason != "a naming preference, not a bug" {
		t.Fatalf("result = %+v", res)
	}
	if n := len(p.Requests()); n != 3 {
		t.Errorf("%d model calls, want find + 2 verify", n)
	}
}

// No changes since base: the model is never called.
func TestRunNothingToReview(t *testing.T) {
	root := repo(t)
	run(t, root, "switch", "-q", "--discard-changes", "main")
	p := &enginetest.Provider{Respond: func(engine.ChatRequest) enginetest.Reply {
		t.Error("the model was called")
		return enginetest.Reply{Err: errors.New("unexpected")}
	}}
	out, err := registry.Invoke(context.Background(), enginetest.NewEngine(p, root), &Totem{}, registry.Args{})
	if err != nil {
		t.Fatal(err)
	}
	if r := out.(*reviewResult); len(r.Confirmed)+len(r.Dropped) != 0 || !strings.Contains(r.Render(), "Nothing to report") {
		t.Fatalf("result = %+v", r)
	}
}

// A base is a branch or a commit, never a git option, and must exist.
func TestBase(t *testing.T) {
	root := repo(t)
	p := &enginetest.Provider{Respond: func(engine.ChatRequest) enginetest.Reply {
		return enginetest.Reply{Err: errors.New("unexpected")}
	}}
	for base, want := range map[string]string{
		"--output=/tmp/x": "not a branch or commit",
		"-p":              "not a branch or commit",
		"main;rm -rf /":   "not a branch or commit",
		"nope":            "not found",
	} {
		_, err := registry.Invoke(context.Background(), enginetest.NewEngine(p, root), &Totem{},
			registry.Args{Params: map[string]any{"base": base}})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("base %q: err = %v, want %q", base, err, want)
		}
	}
}

// The repository under review may be someone else's: its config cannot make
// the review run a command (an external diff, a textconv, an fsmonitor hook).
func TestRepoConfigRunsNothing(t *testing.T) {
	root := repo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	cmd := "sh -c 'touch " + filepath.ToSlash(marker) + "'"
	run(t, root, "config", "diff.external", cmd)
	run(t, root, "config", "core.fsmonitor", cmd)
	run(t, root, "config", "diff.go.textconv", cmd)
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.go diff=go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &enginetest.Provider{Respond: func(engine.ChatRequest) enginetest.Reply {
		return enginetest.Submit(map[string]any{"findings": []any{}})
	}}
	if _, err := registry.Invoke(context.Background(), enginetest.NewEngine(p, root), &Totem{}, registry.Args{}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the repository's git config ran a command")
	}
}

// The terminal summary: worst first, the dropped ones with the skeptic's word.
func TestRenderGolden(t *testing.T) {
	r := &reviewResult{Base: "main", Focus: "division",
		Confirmed: []finding{
			{File: "stats.go", Line: "return total / n", Problem: "n is 0 for an empty slice", Severity: "medium"},
			{File: "calc.go", Line: "return a / b", Problem: "divide by zero when b is 0", Severity: "high"},
		},
		Dropped: []finding{
			{File: "calc.go", Line: "func Div(a, b int) int", Problem: "Div should be named Divide", Severity: "low", Reason: "a naming preference, not a bug"},
		},
		Unverified: 1,
	}
	got := r.Render()
	golden := filepath.Join("testdata", "review_golden.md")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden file %s: %v (run with -update to create it)", golden, err)
	}
	// A Windows checkout may have turned the golden's line ends into CRLF.
	if got != strings.ReplaceAll(string(want), "\r\n", "\n") {
		t.Fatalf("output differs from golden file %s (run with -update to refresh):\n%s", golden, got)
	}
}
