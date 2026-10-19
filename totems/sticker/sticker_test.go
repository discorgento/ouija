package sticker

import (
	"encoding/xml"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "update golden files")

func TestParseScout(t *testing.T) {
	t.Run("valid json", func(t *testing.T) {
		d := parseScout(`{"projectName":"MyApp","projectDescription":"An app","techTags":["Go","React"]}`)
		if d.ProjectName != "MyApp" {
			t.Fatalf("got %q", d.ProjectName)
		}
		if d.Tagline != "An app" {
			t.Fatalf("got %q", d.Tagline)
		}
		if len(d.TechTags) != 2 || d.TechTags[0] != "Go" {
			t.Fatalf("got %v", d.TechTags)
		}
	})

	t.Run("placeholder values become empty", func(t *testing.T) {
		d := parseScout(`{"projectName":"N/A","projectDescription":"EMPTY","techTags":["none","Go (not found)"]}`)
		if d.ProjectName != "Project" { // empty → default
			t.Fatalf("got %q", d.ProjectName)
		}
		if d.Tagline != "" {
			t.Fatalf("got %q", d.Tagline)
		}
		if len(d.TechTags) != 1 || d.TechTags[0] != "Go" {
			t.Fatalf("got %v", d.TechTags)
		}
	})

	t.Run("invalid json returns defaults", func(t *testing.T) {
		d := parseScout(`not json`)
		if d.ProjectName != "Project" {
			t.Fatalf("got %q", d.ProjectName)
		}
		if d.Tagline != "" {
			t.Fatalf("got %q", d.Tagline)
		}
	})
}

func TestBuildBadges(t *testing.T) {
	t.Run("version badges first, then tech tags", func(t *testing.T) {
		d := &scoutData{
			PlatformVersion: "2.4.7",
			PHPVersion:      "8.3",
			ThemeVersion:    "1.2.0",
			TechTags:        []string{"Redis", "Varnish", "MySQL", "Docker"},
		}
		b := buildBadges(d)
		if len(b) != 6 {
			t.Fatalf("expected 6 badges, got %d: %v", len(b), b)
		}
		if b[0] != "Platform 2.4.7" {
			t.Fatalf("badge[0]=%q", b[0])
		}
		if b[1] != "PHP 8.3" {
			t.Fatalf("badge[1]=%q", b[1])
		}
		if b[2] != "Theme 1.2.0" {
			t.Fatalf("badge[2]=%q", b[2])
		}
	})

	t.Run("no versions fills with tech tags and fallbacks", func(t *testing.T) {
		d := &scoutData{TechTags: []string{"Go", "React"}}
		b := buildBadges(d)
		if len(b) != 6 {
			t.Fatalf("expected 6, got %d: %v", len(b), b)
		}
		if b[0] != "Go" || b[1] != "React" {
			t.Fatalf("first two badges should be tech tags, got %v", b[:2])
		}
	})

	t.Run("PHP tag is skipped when PHP badge is present", func(t *testing.T) {
		d := &scoutData{PHPVersion: "8.3", TechTags: []string{"PHP", "Laravel"}}
		b := buildBadges(d)
		if b[0] != "PHP 8.3" {
			t.Fatalf("badge[0]=%q", b[0])
		}
		// "PHP" tech tag should be skipped since PHP badge already present
		if b[1] != "Laravel" {
			t.Fatalf("badge[1]=%q", b[1])
		}
	})
}

func TestAdaptName(t *testing.T) {
	tests := []struct {
		name     string
		wantSize string
		wantLen  int // max rune length of returned name
	}{
		{"MyApp", "96", 5},
		{"MyProject", "72", 9},
		{"SuperLongProjectName", "72", 12}, // 9 chars + "..."
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, size := adaptName(tc.name)
			if size != tc.wantSize {
				t.Fatalf("adaptName(%q) size=%q, want %q", tc.name, size, tc.wantSize)
			}
			if len([]rune(got)) > tc.wantLen {
				t.Fatalf("adaptName(%q) = %q (len=%d), want max len %d", tc.name, got, len([]rune(got)), tc.wantLen)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input string
		n     int
		want  string
	}{
		{"hello", 10, "hello"},
		{"hello world", 5, "hello..."},
		{"", 5, ""},
		{"abc", 3, "abc"},
	}
	for _, tc := range tests {
		got := truncate(tc.input, tc.n)
		if got != tc.want {
			t.Fatalf("truncate(%q, %d) = %q, want %q", tc.input, tc.n, got, tc.want)
		}
	}
}

func TestCleanProjectName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"acme/my-module", "Acme"},
		{"My Project", "My"},
		{"SingleWord", "SingleWord"},
		{"", ""},
		{"  spaced  ", "spaced"}, // cleanProjectName does not capitalize
	}
	for _, tc := range tests {
		got := cleanProjectName(tc.input)
		if got != tc.want {
			t.Fatalf("cleanProjectName(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestFillTemplateGolden(t *testing.T) {
	data := &scoutData{
		ProjectName:     "MyApp",
		Tagline:         "A great application",
		PlatformVersion: "2.4.7",
		PHPVersion:      "8.3",
		ThemeVersion:    "1.2.0",
		CustomModules:   "42",
		TechTags:        []string{"Go", "React", "PostgreSQL", "Redis", "Docker", "GraphQL"},
	}
	got := fillTemplate(data)

	// Verify essential substitutions — the SVG should not contain raw placeholders.
	for _, ph := range []string{"{{PROJECT_NAME}}", "{{TAGLINE}}", "{{BADGE_", "{{BOTTOM_LEFT}}", "{{BOTTOM_RIGHT}}"} {
		if strings.Contains(got, ph) {
			t.Fatalf("output still contains placeholder %q", ph)
		}
	}

	// Verify key content made it in.
	if !strings.Contains(got, "MyApp") {
		t.Fatalf("output missing project name")
	}
	if !strings.Contains(got, "2.4.7") {
		t.Fatalf("output missing platform version")
	}
	if !strings.Contains(got, "Go") {
		t.Fatalf("output missing tech tag")
	}

	// Golden file test — skip in short mode.
	if testing.Short() {
		return
	}

	golden := filepath.Join("testdata", "sticker_golden.svg")
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

func TestEmptyIfPlaceholder(t *testing.T) {
	tests := []string{"", "EMPTY", "N/A", "NONE", "NULL", "TBD", "TODO", "UNKNOWN", "NOT FOUND", "NOT AVAILABLE"}
	for _, tc := range tests {
		if got := emptyIfPlaceholder(tc); got != "" {
			t.Fatalf("emptyIfPlaceholder(%q) = %q, want \"\"", tc, got)
		}
	}
	if got := emptyIfPlaceholder("real value"); got != "real value" {
		t.Fatalf("emptyIfPlaceholder(%q) = %q, want %q", "real value", got, "real value")
	}
}

func TestValidVersion(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"1.2.3", "1.2.3"},
		{"2.4.7-p10", "2.4.7-p10"},
		{"1.0", "1.0"},
		{"", ""},
		{"none", ""},
		{"abc", ""},
		{"v1.2.3", ""},
	}
	for _, tc := range tests {
		got := validVersion(tc.input)
		if got != tc.want {
			t.Fatalf("validVersion(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestValidPHP(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"8.3", "8.3"},
		{"8.2", "8.2"},
		{"8.4", "8.4"},
		{"7.4", "7.4"},
		{"7.0", "7.0"},
		{"7.5", ""}, // php 7.x max was 7.4
		{"5.6", "5.6"},
		{"5.7", ""}, // php 5.x max was 5.6
		{"9.0", ""}, // unknown major
		{"", ""},
	}
	for _, tc := range tests {
		got := validPHP(tc.input)
		if got != tc.want {
			t.Fatalf("validPHP(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestStripVersionCaret(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"^2.4.7", "2.4.7"},
		{"~1.4.0", "1.4.0"},
		{">=8.2", "8.2"},
		{"^2.4.7-p10", "2.4.7-p10"},
		{"2.4.7", "2.4.7"},
		{">=8.2 || ^8.3", "8.2"},
		{"", ""},
	}
	for _, tc := range tests {
		got := stripVersionCaret(tc.input)
		if got != tc.want {
			t.Fatalf("stripVersionCaret(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// Values are XML-escaped: "&" or "<" in a tagline or tag used to produce a
// sticker no browser could open.
func TestFillTemplateEscapes(t *testing.T) {
	svg := fillTemplate(&scoutData{ProjectName: "A&B", Tagline: "Fast & <simple>", TechTags: []string{"R&D"}})
	if strings.Contains(svg, "Fast & ") || strings.Contains(svg, "<simple>") || !strings.Contains(svg, "Fast &amp; &lt;simple&gt;") {
		t.Fatalf("unescaped values in the SVG")
	}
	if err := xml.Unmarshal([]byte(svg), new(struct{})); err != nil {
		t.Fatalf("SVG is not well-formed XML: %v", err)
	}
}

func TestSlugFromNonASCII(t *testing.T) {
	if got := slugFrom(&scoutData{ProjectName: "日本語ツール"}); got != "project-sticker" {
		t.Fatalf("slugFrom = %q, want the fallback (not a hidden file)", got)
	}
}
