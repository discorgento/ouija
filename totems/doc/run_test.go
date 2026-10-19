package doc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/enginetest"
	"github.com/discorgento/ouija/registry"
)

func firstUser(req engine.ChatRequest) string {
	for _, m := range req.Messages {
		if m.Role == engine.RoleUser {
			return m.Content
		}
	}
	return ""
}

// The whole pipeline on a scripted model: scout, fact passes, reconcile into
// a fact sheet, compose; the HTML gets the CSP, the MD the facts, and with no
// browser the PDF is reported as not rendered.
func TestRunOrchestration(t *testing.T) {
	lookChrome = func() string { return "" }
	t.Cleanup(func() { lookChrome = findChrome })
	root := t.TempDir()
	sheet, _ := json.Marshal(map[string]any{"title": "Calc Manual", "subtitle": "s", "docType": "manual",
		"sections": []any{map[string]any{"heading": "Div", "points": []any{"returns an error on zero"}}}, "openQuestions": []any{"q?"}})
	p := &enginetest.Provider{Respond: func(req engine.ChatRequest) enginetest.Reply {
		prompt := firstUser(req)
		switch {
		case strings.HasPrefix(req.System, "You scout") && !strings.Contains(req.System, "SCOUT CONTEXT"):
			return enginetest.Submit(map[string]any{"summary": "calc", "configPaths": []any{}, "files": []any{"calc.go"}, "consumers": []any{}})
		case strings.HasPrefix(prompt, "PASS"):
			return enginetest.Submit(map[string]any{"aspect": "behavior", "facts": []any{}})
		case strings.HasPrefix(req.System, "You reconcile"):
			return enginetest.Reply{Text: string(sheet)}
		case strings.HasPrefix(req.System, "You write professional"):
			return enginetest.Reply{Text: "```html\n<html><head><title>x</title></head><body>Div</body></html>\n```"}
		}
		t.Errorf("unexpected request: %.60q / %.60q", req.System, prompt)
		return enginetest.Reply{Err: errors.New("unexpected")}
	}}

	out, err := registry.Invoke(context.Background(), enginetest.NewEngine(p, root), &Totem{}, registry.Args{Task: "manual for calc"})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(*docResult)
	html, _ := os.ReadFile(res.HTML)
	md, _ := os.ReadFile(res.MD)
	if !strings.Contains(string(html), docCSP) || strings.Contains(string(html), "```") {
		t.Errorf("html lacks the CSP or kept the fence:\n%s", html)
	}
	if !strings.Contains(string(md), "returns an error on zero") {
		t.Errorf("md lacks the facts:\n%s", md)
	}
	if res.PDF != "" || !strings.Contains(res.PDFNote, "not found") || len(res.OpenQuestions) != 1 {
		t.Errorf("result = %+v", res)
	}
	if !strings.HasSuffix(res.HTML, "calc-manual.html") {
		t.Errorf("html path = %s", res.HTML)
	}
}

func TestFileURL(t *testing.T) {
	for in, want := range map[string]string{
		"/home/u/my docs/a.html": "file:///home/u/my%20docs/a.html",
		`C:\Users\u\a.html`:      "file:///C:/Users/u/a.html",
	} {
		if os.PathSeparator == '/' && strings.Contains(in, `\`) {
			continue // a backslash is a file-name character on Unix
		}
		if got := fileURL(in); got != want {
			t.Errorf("fileURL(%q) = %q, want %q", in, got, want)
		}
	}
}
