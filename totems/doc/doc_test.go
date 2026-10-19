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

func TestSlugify(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"My Document", "my-document"},
		{"Configuration Manual", "configuration-manual"},
		{"  spaces  ", "spaces"},
		{"UPPERCASE", "uppercase"},
		{"special_chars!", "special-chars"},
	}
	for _, tc := range tests {
		got := slugify(tc.input)
		if got != tc.want {
			t.Fatalf("slugify(%q) = %q, want %q", tc.input, got, tc.want)
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

func TestDocSlugify(t *testing.T) {
	// Verify slugify is accessible (same function name in doc and sticker).
	got := slugify("My Document Title")
	want := "my-document-title"
	if got != want {
		t.Fatalf("slugify(%q) = %q, want %q", "My Document Title", got, want)
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
