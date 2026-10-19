package doc

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func TestStripFence(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"```html\n<html></html>\n```", "<html></html>"},
		{"```\nplain code\n```", "plain code"},
		{"```go\npackage main\n```", "package main"},
		{"no fence here", "no fence here"},
		{"", ""},
		{"  spaced  ", "spaced"},
	}
	for _, tc := range tests {
		got := stripFence(tc.input)
		if got != tc.want {
			t.Fatalf("stripFence(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestTechnicalMDGolden(t *testing.T) {
	sheet := &factSheet{
		Title:    "Configuration Manual",
		Subtitle: "A detailed reference",
		Sections: []struct {
			Heading string   `json:"heading"`
			Points  []string `json:"points"`
		}{
			{Heading: "Overview", Points: []string{"System overview", "Architecture"}},
			{Heading: "Settings", Points: []string{"ACL configuration", "Default values"}},
		},
		OpenQuestions: []string{"Verify email template IDs"},
	}
	got := technicalMD(sheet)

	// Verify structure.
	if !strings.Contains(got, "Configuration Manual") {
		t.Fatalf("output missing title")
	}
	if !strings.Contains(got, "Overview") {
		t.Fatalf("output missing section heading")
	}
	if !strings.Contains(got, "ACL configuration") {
		t.Fatalf("output missing section point")
	}
	if !strings.Contains(got, "Open questions") {
		t.Fatalf("output missing open questions section")
	}

	// Golden file test.
	if testing.Short() {
		return
	}

	golden := filepath.Join("testdata", "technical_md_golden.md")
	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	expected, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden file %s: %v (run with -update to create it)", golden, err)
	}
	if got != string(expected) {
		t.Fatalf("output differs from golden file %s (run with -update to refresh)", golden)
	}
}

func TestDocResultRender(t *testing.T) {
	r := &docResult{
		HTML: "docs/output.html",
		PDF:  "docs/output.pdf",
		MD:   "docs/output.md",
	}
	got := r.Render()
	if !strings.Contains(got, "docs/output.html") {
		t.Fatalf("render missing HTML path")
	}
	if !strings.Contains(got, "docs/output.pdf") {
		t.Fatalf("render missing PDF path")
	}
	if !strings.Contains(got, "# Document generated") {
		t.Fatalf("render missing header")
	}
}

// The CSP must come first in <head> (it only covers what follows it) and be
// added even when the model left out <head> or <html>.
func TestWithCSP(t *testing.T) {
	cases := []string{
		`<!doctype html><html lang="pt"><HEAD><link href="x"></HEAD><body></body></html>`,
		`<html><body><img src="https://evil/x"></body></html>`,
		`<p>fragment</p>`,
	}
	for _, in := range cases {
		out := withCSP(in)
		i := strings.Index(out, `http-equiv="Content-Security-Policy"`)
		if i < 0 {
			t.Fatalf("no CSP in %q", out)
		}
		for _, later := range []string{"<link", "<img", "<p>", "<body"} {
			if j := strings.Index(out, later); j >= 0 && j < i {
				t.Errorf("%q comes before the CSP in %q", later, out)
			}
		}
	}
	for _, must := range []string{"default-src 'none'", "img-src data:"} {
		if !strings.Contains(docCSP, must) {
			t.Errorf("docCSP lost %q", must)
		}
	}
	if strings.Contains(docCSP, "script-src") {
		t.Error("docCSP must not allow scripts")
	}
}

// A model-chosen title never replaces a file the user wrote; a previous doc
// run's trio is regenerated in place.
func TestFreeSlug(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	if err := os.MkdirAll(docs, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := freeSlug(root, "fresh"); got != "fresh" {
		t.Fatalf("free name: got %q", got)
	}
	if err := os.WriteFile(filepath.Join(docs, "architecture.md"), []byte("# mine"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := freeSlug(root, "architecture"); got != "architecture-2" {
		t.Fatalf("user file: got %q, want architecture-2", got)
	}
	if err := os.WriteFile(filepath.Join(docs, "manual.html"), []byte(withCSP("<html><head></head></html>")), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := freeSlug(root, "manual"); got != "manual" {
		t.Fatalf("own trio: got %q, want manual (regenerate)", got)
	}
}
