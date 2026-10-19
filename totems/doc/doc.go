// Package doc is the "doc" totem: it generates a code-verified client document
// using the harness, then renders a PDF via headless Chrome. The template is
// embedded so ouija has no runtime dependency on external doc projects.
package doc

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/registry"
	"github.com/discorgento/ouija/schema"
)

//go:embed template.html
var templateHTML string

func init() { registry.Register(&Totem{}) }

// Totem turns a doc request into a verified .html (+ .pdf) + technical .md.
type Totem struct{}

func (t *Totem) Name() string { return "doc" }
func (t *Totem) Summary() string {
	return "Generate a code-verified client document: scout -> verify -> compose -> .html/.pdf/.md"
}
func (t *Totem) Init(_ context.Context, _ *engine.Engine, _ *engine.Config) error { return nil }
func (t *Totem) Cleanup() error                                                   { return nil }

// docAspects are the fact-gathering angles for a document (vs. task's analysis dimensions).
var docAspects = []string{"configuration", "behavior", "integration"}

// Config is the doc totem's section of the project config, `totems.doc`.
type Config struct {
	Passes int `json:"passes"` // independent fact-gathering passes per aspect
}

// loadConfig reads `totems.doc`; an absent or non-positive passes means 3.
func loadConfig(cfg *engine.Config) (Config, error) {
	c := Config{Passes: 3}
	if err := cfg.TotemConfig("doc", &c); err != nil {
		return Config{}, err
	}
	if c.Passes <= 0 {
		c.Passes = 3
	}
	return c, nil
}

func (t *Totem) Run(ctx context.Context, eng *engine.Engine, args registry.Args) (any, error) {
	if strings.TrimSpace(args.Task) == "" {
		return nil, fmt.Errorf("empty request: pass what to document as the first argument")
	}
	cfg := eng.Config()
	dc, err := loadConfig(cfg)
	if err != nil {
		return nil, err
	}
	rules := "- " + strings.Join(cfg.Rules, "\n- ")

	// 1) Scout — read the scope and map what exists (read-only tools).
	scoutSystem := "You scout code to document it. Project rules:\n" + rules
	scoutPrompt := fmt.Sprintf(
		"DOC REQUEST: %s\n\nRead the relevant code/config with the tools and map what exists "+
			"(config paths, ACL, defaults, admin fields, flows, consumers). Do NOT write the doc yet. When done, call submit_result.",
		args.Task)
	scoutRes, err := eng.Run(ctx, engine.RunRequest{
		Label: "scout", System: scoutSystem, Prompt: scoutPrompt,
		UseTools: true, Schema: scoutSchema(), MaxTokens: 4096,
	})
	if err != nil {
		return nil, fmt.Errorf("scout failed: %w", err)
	}
	sharedSystem := scoutSystem + "\n\nSCOUT CONTEXT:\n" + scoutRes.Text

	// 2) Verify facts: passes × aspects, independent (self-consistency on facts).
	var jobs []func() (factResult, error)
	for p := 1; p <= dc.Passes; p++ {
		for _, a := range docAspects {
			p, a := p, a
			jobs = append(jobs, func() (factResult, error) {
				prompt := fmt.Sprintf(
					"PASS %d (INDEPENDENT). DOC REQUEST: %s\n\nGather the documentable facts for the %q aspect. "+
						"Confirm every fact in the code before stating it (use the tools); mark verified=false if you could not. "+
						"When done, call submit_result.",
					p, args.Task, a)
				pr, rerr := eng.Run(ctx, engine.RunRequest{
					Label: fmt.Sprintf("p%d:%s", p, a), System: sharedSystem, Prompt: prompt,
					UseTools: true, Schema: factSchema(), MaxTokens: 4096,
				})
				if rerr != nil {
					return factResult{}, rerr
				}
				return factResult{Aspect: a, JSON: pr.Text}, nil
			})
		}
	}
	facts, pErr := engine.Parallel(ctx, 8, jobs)

	okFacts := 0
	for _, f := range facts {
		if f.JSON != "" {
			okFacts++
		}
	}
	if okFacts == 0 {
		if pErr != nil {
			return nil, fmt.Errorf("all %d fact passes failed: %w", len(jobs), pErr)
		}
		return nil, fmt.Errorf("all %d fact passes failed (check provider/key/budget)", len(jobs))
	}
	if pErr != nil {
		fmt.Fprintf(os.Stderr, "[ouija] warning: %d/%d fact passes failed (%v)\n", len(jobs)-okFacts, len(jobs), pErr)
	}

	var fb strings.Builder
	for _, f := range facts {
		if f.JSON == "" {
			continue
		}
		fmt.Fprintf(&fb, "{\"aspect\":%q,\"facts\":%s}\n", f.Aspect, f.JSON)
	}

	// 3) Reconcile into a verified fact sheet (only facts stable across passes).
	recSystem := "You reconcile independent fact-gathering passes for a document. Project rules:\n" + rules
	recPrompt := fmt.Sprintf(
		"DOC REQUEST: %s\n\nSCOUT CONTEXT:\n%s\n\nFACT PASSES:\n%s\n\n"+
			"Keep only facts that are consistent across passes and verified in code; drop or flag the rest. "+
			"Produce title, subtitle, docType (manual|parecer|relatorio), the document sections (heading + points), "+
			"and openQuestions. When done, call submit_result.",
		args.Task, scoutRes.Text, fb.String())
	sheetRes, err := eng.Run(ctx, engine.RunRequest{
		Label: "reconcile", System: recSystem, Prompt: recPrompt,
		Schema: sheetSchema(), MaxTokens: 6144,
	})
	if err != nil {
		return nil, fmt.Errorf("reconcile failed: %w", err)
	}
	var sheet factSheet
	if err := json.Unmarshal([]byte(sheetRes.Text), &sheet); err != nil {
		return nil, fmt.Errorf("invalid fact sheet: %w", err)
	}

	// 4) Compose the final HTML from the embedded template + verified facts.
	composeSystem := "You write professional technical documentation. Fill the template below — keep its structure, cover, CSS and design-system components; replace the {{...}} " +
		"placeholders and write the body sections. VERIFIED FACTS ONLY. Output ONLY the complete self-contained HTML.\n\n" +
		"TEMPLATE:\n" + templateHTML
	composePrompt := fmt.Sprintf(
		"DOC REQUEST: %s\n\nVERIFIED FACT SHEET (JSON):\n%s\n\n"+
			"Write the full HTML document now.",
		args.Task, sheetRes.Text)
	htmlRes, err := eng.Run(ctx, engine.RunRequest{
		Label: "compose", System: composeSystem, Prompt: composePrompt, MaxTokens: 16384,
	})
	if err != nil {
		return nil, fmt.Errorf("compose failed: %w", err)
	}
	htmlRes.Text = stripFence(htmlRes.Text)
	if !strings.Contains(strings.ToLower(htmlRes.Text), "<html") {
		return nil, fmt.Errorf("compose did not produce HTML (got %d bytes) — rerun or check the model", len(htmlRes.Text))
	}

	// 5) Write the trio and render the PDF. Every output path goes through the
	// sandbox (symlink-aware): `scope` is attacker-influenceable (CLI flag /
	// MCP arg), and a docs/ or <slug>.* symlink planted in the repo must not
	// redirect a write outside the project.
	slug := slugify(sheet.Title)
	if slug == "" {
		slug = "document"
	}
	res := &docResult{}
	var pdfPath string
	for _, out := range []struct {
		dst *string
		ext string
	}{{&res.HTML, ".html"}, {&res.MD, ".md"}, {&pdfPath, ".pdf"}} {
		p, err := engine.SafeResolve(ctx, cfg.ProjectRoot, filepath.Join("docs", slug+out.ext))
		if err != nil {
			return nil, fmt.Errorf("invalid output path: %w", err)
		}
		*out.dst = p
	}
	if err := os.MkdirAll(filepath.Dir(res.HTML), 0o755); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", filepath.Dir(res.HTML), err)
	}

	if err := os.WriteFile(res.HTML, []byte(withCSP(htmlRes.Text)), 0o644); err != nil {
		return nil, fmt.Errorf("cannot write html: %w", err)
	}
	if err := os.WriteFile(res.MD, []byte(technicalMD(&sheet)), 0o644); err != nil {
		return nil, fmt.Errorf("cannot write md: %w", err)
	}
	res.PDF, res.PDFNote = renderPDF(res.HTML, pdfPath)
	res.OpenQuestions = sheet.OpenQuestions
	return res, nil
}

// --- PDF: headless Chrome print-to-pdf ---

// docCSP locks the model-written HTML down to what the template needs: inline
// styles, Google Fonts and data: images. No scripts, frames, remote images or
// requests — content injected through the repo (prompt injection) can't turn
// the PDF render, or a later look at the HTML, into an exfiltration channel.
const docCSP = "default-src 'none'; style-src 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com data:; img-src data:; base-uri 'none'; form-action 'none'"

var headTag = regexp.MustCompile(`(?i)<head(\s[^>]*)?>`)
var htmlTag = regexp.MustCompile(`(?i)<html(\s[^>]*)?>`)

// withCSP puts the docCSP meta tag first in <head> (a CSP only covers what
// comes after it), creating the head when the model left it out. A second
// CSP in the document can only narrow it further: browsers enforce both.
func withCSP(html string) string {
	meta := `<meta http-equiv="Content-Security-Policy" content="` + docCSP + `">`
	if loc := headTag.FindStringIndex(html); loc != nil {
		return html[:loc[1]] + meta + html[loc[1]:]
	}
	if loc := htmlTag.FindStringIndex(html); loc != nil {
		return html[:loc[1]] + "<head>" + meta + "</head>" + html[loc[1]:]
	}
	return "<head>" + meta + "</head>" + html
}

func renderPDF(htmlPath, pdf string) (string, string) {
	chrome := findChrome()
	if chrome == "" {
		return "", "Chrome/Chromium not found — open the HTML and print to PDF: " + htmlPath
	}
	abs, _ := filepath.Abs(htmlPath)
	flags := []string{"--headless", "--disable-gpu", "--no-pdf-header-footer"}
	// Isolate from the user's real Chrome profile with a throwaway user-data-dir.
	if profile, err := os.MkdirTemp("", "ouija-chrome-"); err == nil {
		defer os.RemoveAll(profile)
		flags = append(flags, "--user-data-dir="+profile)
	}
	flags = append(flags, "--print-to-pdf="+pdf, "file://"+abs)
	if err := exec.Command(chrome, flags...).Run(); err != nil {
		return "", "PDF render failed (" + err.Error() + ") — open the HTML and print to PDF: " + htmlPath
	}
	return pdf, ""
}

func findChrome() string {
	for _, c := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	return ""
}

// --- result ---

type docResult struct {
	HTML          string   `json:"html"`
	PDF           string   `json:"pdf,omitempty"`
	MD            string   `json:"md"`
	PDFNote       string   `json:"pdfNote,omitempty"`
	OpenQuestions []string `json:"openQuestions,omitempty"`
}

func (r *docResult) Render() string {
	var b strings.Builder
	b.WriteString("# Document generated\n\n")
	fmt.Fprintf(&b, "- HTML: %s\n", r.HTML)
	if r.PDF != "" {
		fmt.Fprintf(&b, "- PDF:  %s\n", r.PDF)
	} else if r.PDFNote != "" {
		fmt.Fprintf(&b, "- PDF:  (not rendered) %s\n", r.PDFNote)
	}
	fmt.Fprintf(&b, "- MD:   %s\n", r.MD)
	if len(r.OpenQuestions) > 0 {
		b.WriteString("\n## ⚠️  Open questions (verify before delivering)\n\n")
		for _, q := range r.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", q)
		}
	}
	return b.String()
}

// --- helpers ---

var fenceRe = regexp.MustCompile("(?s)^```[a-zA-Z]*\\n(.*)\\n```\\s*$")

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if m := fenceRe.FindStringSubmatch(s); m != nil {
		return m[1]
	}
	return s
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonSlug.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

func technicalMD(s *factSheet) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s — technical notes\n\n", s.Title)
	if s.Subtitle != "" {
		fmt.Fprintf(&b, "%s\n\n", s.Subtitle)
	}
	for _, sec := range s.Sections {
		fmt.Fprintf(&b, "## %s\n\n", sec.Heading)
		for _, p := range sec.Points {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
	}
	if len(s.OpenQuestions) > 0 {
		b.WriteString("## Open questions\n\n")
		for _, q := range s.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", q)
		}
	}
	return b.String()
}

// --- types & schemas ---

type factResult struct {
	Aspect string
	JSON   string
}

type factSheet struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle"`
	DocType  string `json:"docType"`
	Sections []struct {
		Heading string   `json:"heading"`
		Points  []string `json:"points"`
	} `json:"sections"`
	OpenQuestions []string `json:"openQuestions"`
}

func scoutSchema() map[string]any {
	return schema.Obj(map[string]any{
		"summary":     schema.Str("what the scope does today"),
		"configPaths": schema.Arr("config paths / admin fields / ACL"),
		"files":       schema.Arr("relevant files"),
		"consumers":   schema.Arr("consumers / integration points"),
	}, "summary", "configPaths", "files", "consumers")
}

func factSchema() map[string]any {
	fact := schema.Obj(map[string]any{
		"claim":    schema.Str("the documentable fact"),
		"source":   schema.Str("file:line or class that proves it"),
		"verified": map[string]any{"type": "boolean", "description": "confirmed in code"},
	}, "claim", "source", "verified")
	return schema.Obj(map[string]any{
		"aspect": schema.Str("aspect name"),
		"facts":  map[string]any{"type": "array", "items": fact},
	}, "aspect", "facts")
}

func sheetSchema() map[string]any {
	section := schema.Obj(map[string]any{
		"heading": schema.Str("section heading"),
		"points":  schema.Arr("verified points for this section"),
	}, "heading", "points")
	return schema.Obj(map[string]any{
		"title":         schema.Str("document title"),
		"subtitle":      schema.Str("one-line subtitle"),
		"docType":       map[string]any{"type": "string", "enum": []string{"manual", "assessment", "report"}},
		"sections":      map[string]any{"type": "array", "items": section},
		"openQuestions": schema.Arr("facts that could not be verified / need confirmation"),
	}, "title", "subtitle", "docType", "sections", "openQuestions")
}
