// Package doc is the "doc" totem: it generates a code-verified client document
// using the harness, then renders a PDF via headless Chrome. The template is
// embedded so ouija has no runtime dependency on external doc projects.
package doc

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/registry"
	"github.com/discorgento/ouija/schema"
	"github.com/discorgento/ouija/totems/internal/seance"
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

// docAspects are the fact-gathering angles for a document (vs. task's analysis dimensions).
var docAspects = []string{"configuration", "behavior", "integration"}

// Config is the doc totem's section of the project config, `totems.doc`.
type Config struct {
	Passes int `json:"passes"` // independent fact-gathering passes per aspect
	// The cover and signature: facts no code holds, so they come from here
	// (or `--set client=…`), never from the model.
	Client  string `json:"client"`  // who the document is for
	Author  string `json:"author"`  // "prepared by" and the signature
	Contact string `json:"contact"` // the signature's contact line
	Brand   string `json:"brand"`   // logo text, brand tag and cover footer
	Locale  string `json:"locale"`  // the document's language tag, default "en"
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
	facts, err := seance.Passes(ctx, eng, "fact passes", jobs, func(f factResult) bool { return f.JSON != "" })
	if err != nil {
		return nil, err
	}

	var fb strings.Builder
	for _, f := range facts {
		fmt.Fprintf(&fb, "{\"aspect\":%q,\"facts\":%s}\n", f.Aspect, f.JSON)
	}

	// 3) Reconcile into a verified fact sheet (only facts stable across passes).
	recSystem := "You reconcile independent fact-gathering passes for a document. Project rules:\n" + rules
	recPrompt := fmt.Sprintf(
		"DOC REQUEST: %s\n\nSCOUT CONTEXT:\n%s\n\nFACT PASSES:\n%s\n\n"+
			"Keep only facts that are consistent across passes and verified in code; drop or flag the rest. "+
			"Produce title, subtitle, docType (manual|assessment|report), the document sections (heading + points), "+
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
	// The cover's placeholders are filled here, not by the model: it would
	// invent a client, an author and a date, or leave {{CLIENT_NAME}} in a
	// client-facing PDF.
	composeSystem := "You write professional technical documentation. Complete the template below — keep its structure, cover, CSS and design-system components exactly; " +
		"write the body sections. VERIFIED FACTS ONLY. Output ONLY the complete self-contained HTML, from <!DOCTYPE html> to </html>.\n\n" +
		"TEMPLATE:\n" + fillCover(templateHTML, &sheet, dc, args.Params, time.Now())
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
	page, ok := extractHTML(htmlRes.Text)
	if !ok {
		return nil, fmt.Errorf("compose did not produce a complete HTML document (got %d bytes, no </html> — cut off by the output limit?) — rerun or use a model with a larger output", len(htmlRes.Text))
	}
	htmlRes.Text = page

	// 5) Write the trio and render the PDF. Every output path goes through the
	// sandbox (symlink-aware): `scope` is attacker-influenceable (CLI flag /
	// MCP arg), and a docs/ or <slug>.* symlink planted in the repo must not
	// redirect a write outside the project.
	slug := freeSlug(cfg.ProjectRoot, cmp.Or(seance.Slugify(sheet.Title), "document"))
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
	if _, err := engine.SafeWriteFile(ctx, cfg.ProjectRoot, filepath.Join("docs", slug+".html"), []byte(withCSP(sanitizeHTML(htmlRes.Text))), 0o644); err != nil {
		return nil, fmt.Errorf("cannot write html: %w", err)
	}
	if _, err := engine.SafeWriteFile(ctx, cfg.ProjectRoot, filepath.Join("docs", slug+".md"), []byte(technicalMD(&sheet)), 0o644); err != nil {
		return nil, fmt.Errorf("cannot write md: %w", err)
	}
	res.PDF, res.PDFNote = renderPDF(ctx, res.HTML, pdfPath)
	res.OpenQuestions = sheet.OpenQuestions
	if left := placeholderRe.FindAllString(page, -1); len(left) > 0 {
		res.Warnings = append(res.Warnings, "template placeholders left unfilled: "+strings.Join(slices.Compact(slices.Sorted(slices.Values(left))), ", "))
	}
	return res, nil
}

// freeSlug returns slug, or slug-2, slug-3… when docs/ already holds a file
// of that name that a previous doc run did not write: the title comes from
// the model, and "Architecture" must not replace a hand-written
// docs/architecture.md (or "Knowledge" the `ouija learn` store). A trio this
// totem wrote — its HTML carries docCSP — is regenerated in place.
func freeSlug(root, slug string) string {
	for i := 1; ; i++ {
		s := slug
		if i > 1 {
			s = fmt.Sprintf("%s-%d", slug, i)
		}
		if ownsSlug(root, s) || i > 99 {
			return s
		}
	}
}

// ownsSlug reports whether docs/<s>.* is free to write: no such files, or
// ones a previous doc run produced.
func ownsSlug(root, s string) bool {
	base := filepath.Join(root, "docs", s)
	if b, err := os.ReadFile(base + ".html"); err == nil {
		return strings.Contains(string(b), docCSP)
	}
	for _, ext := range []string{".md", ".pdf"} {
		if _, err := os.Lstat(base + ext); err == nil {
			return false
		}
	}
	return true
}

// --- PDF: headless Chrome print-to-pdf ---

// docCSP locks the model-written HTML down to what the template needs: inline
// styles, Google Fonts and data: images. No scripts, frames, remote images or
// requests — content injected through the repo (prompt injection) can't turn
// the PDF render, or a later look at the HTML, into an exfiltration channel.
// A CSP cannot stop navigation (<meta http-equiv="refresh">) or resource
// hints (<link rel="preconnect">, "dns-prefetch"…): sanitizeHTML removes
// those, and renderPDF cuts Chrome off the network. frame-ancestors is left
// out: browsers ignore it in a <meta> policy.
const docCSP = "default-src 'none'; style-src 'unsafe-inline' https://fonts.googleapis.com; " +
	"font-src https://fonts.gstatic.com data:; img-src data:; base-uri 'none'; form-action 'none'"

// noDNSPrefetch turns off Chrome's speculative DNS lookups of the page's
// links, which a CSP does not cover either.
const noDNSPrefetch = `<meta http-equiv="x-dns-prefetch-control" content="off">`

// safeLinkRels are the <link rel> values kept in model-written HTML: the ones
// that fetch nothing, or fetch under the CSP (a stylesheet). Every other
// value — preconnect, dns-prefetch, prefetch, prerender, preload,
// modulepreload, next, and the icons (a favicon may be fetched outside the
// page's CSP; the template uses none) — makes the link go.
var safeLinkRels = map[string]bool{
	"stylesheet": true, "alternate": true,
	"author": true, "canonical": true, "help": true, "license": true,
}

// docCharset declares the encoding withCSP writes first: the browser's
// encoding prescan takes the first declaration it meets, so the model's own
// (<meta charset="iso-2022-jp">, where an ESC sequence inside "<me\x1b(Bta"
// decodes to nothing and rebuilds a tag the byte scan never saw) is ignored —
// and sanitizeHTML removes it anyway.
const docCharset = `<meta charset="utf-8">`

// sanitizeHTML removes from model-written HTML the tags that reach the
// network or navigate in spite of the CSP: every <meta http-equiv> (a
// refresh to an attacker URL with the data in the query, or a policy of the
// model's own), every <meta charset> (the document is UTF-8: withCSP says so
// first), and every <link> with a rel outside safeLinkRels. Before that the
// text is made valid UTF-8 and loses its control characters but tab, LF, FF
// and CR, so no byte the scan reads past (an ESC that starts an encoding
// shift) can hide a tag from it.
//
// The scan is context-free on purpose: it finds each "<meta"/"<link" the way
// the browser's tokenizer would read a tag starting there, including inside
// comments, <style> or <svg> (where a <meta> breaks out into the document),
// and reads the attributes as the tokenizer does — any case, any quoting,
// entities decoded in values. Removing a tag the browser would have read as
// text costs nothing. It repeats until nothing changes, so a removal cannot
// splice the halves of a tag together ("<me<meta …>ta …>").
func sanitizeHTML(s string) string {
	s = strings.Map(func(r rune) rune {
		if (r < 0x20 && !isHTMLSpace(byte(r))) || r == 0x7f {
			return -1
		}
		return r
	}, strings.ToValidUTF8(s, "\uFFFD"))
	for {
		out := stripUnsafeTags(s)
		if out == s {
			return out
		}
		s = out
	}
}

func stripUnsafeTags(s string) string {
	type span struct{ from, to int }
	var cut []span
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		name := tagNameAt(s, i)
		if name == "" {
			continue
		}
		end, attrs := scanAttrs(s, i+1+len(name))
		if !unsafeTag(name, attrs) {
			continue
		}
		// Tags are tried at every "<", so a span may start inside the last
		// one (a decoy quoted value swallowing a real tag): merge them.
		if n := len(cut); n > 0 && i <= cut[n-1].to {
			cut[n-1].to = max(cut[n-1].to, end)
			continue
		}
		cut = append(cut, span{i, end})
	}
	if len(cut) == 0 {
		return s
	}
	var b strings.Builder
	last := 0
	for _, c := range cut {
		b.WriteString(s[last:c.from])
		last = c.to
	}
	b.WriteString(s[last:])
	return b.String()
}

// tagNameAt returns "meta" or "link" when s[i:] opens such a tag (the name
// ends at whitespace, "/", ">" or the end of input), else "".
func tagNameAt(s string, i int) string {
	for _, name := range []string{"meta", "link"} {
		j := i + 1 + len(name)
		if j > len(s) || asciiLower(s[i+1:j]) != name {
			continue
		}
		if j == len(s) || isHTMLSpace(s[j]) || s[j] == '/' || s[j] == '>' {
			return name
		}
	}
	return ""
}

type htmlAttr struct{ name, value string }

// scanAttrs reads a start tag's attributes from s[i:] by the HTML tokenizer's
// rules and returns the index just past the tag's ">" (or len(s): a tag cut
// off by the end of input is dropped whole) and the attributes, names in
// lower case and values with their character references decoded.
func scanAttrs(s string, i int) (int, []htmlAttr) {
	var attrs []htmlAttr
	for {
		// Before an attribute name: whitespace and "/" separate attributes.
		for i < len(s) && (isHTMLSpace(s[i]) || s[i] == '/') {
			i++
		}
		if i >= len(s) {
			return len(s), attrs
		}
		if s[i] == '>' {
			return i + 1, attrs
		}
		// The name: a leading "=" belongs to it, then up to the next
		// whitespace, "/", ">" or "=".
		start := i
		i++
		for i < len(s) && !isHTMLSpace(s[i]) && s[i] != '/' && s[i] != '>' && s[i] != '=' {
			i++
		}
		attr := htmlAttr{name: asciiLower(s[start:i])}
		for i < len(s) && isHTMLSpace(s[i]) {
			i++
		}
		if i < len(s) && s[i] == '=' {
			i++
			for i < len(s) && isHTMLSpace(s[i]) {
				i++
			}
			switch {
			case i >= len(s):
			case s[i] == '"' || s[i] == '\'':
				q := s[i]
				end := strings.IndexByte(s[i+1:], q)
				if end < 0 {
					return len(s), append(attrs, attr)
				}
				attr.value = html.UnescapeString(s[i+1 : i+1+end])
				i += end + 2
			case s[i] == '>':
			default:
				start := i
				for i < len(s) && !isHTMLSpace(s[i]) && s[i] != '>' {
					i++
				}
				attr.value = html.UnescapeString(s[start:i])
			}
		}
		attrs = append(attrs, attr)
	}
}

// unsafeTag reports whether a scanned <meta>/<link> must go.
func unsafeTag(name string, attrs []htmlAttr) bool {
	for _, a := range attrs {
		switch {
		case name == "meta" && (a.name == "http-equiv" || a.name == "charset"):
			return true
		case name == "link" && a.name == "rel":
			for _, rel := range strings.FieldsFunc(asciiLower(a.value), func(r rune) bool { return r < 0x80 && isHTMLSpace(byte(r)) }) {
				if !safeLinkRels[rel] {
					return true
				}
			}
		}
	}
	return false
}

// isHTMLSpace is HTML's ASCII whitespace (tab, LF, FF, CR, space).
func isHTMLSpace(c byte) bool {
	return c == '\t' || c == '\n' || c == '\f' || c == '\r' || c == ' '
}

// asciiLower lowercases ASCII letters only, as HTML does for tag and
// attribute names: strings.ToLower would also fold "K" (Kelvin) to "k".
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

var doctype = regexp.MustCompile(`(?is)^\s*(<!--.*?-->\s*)*<!doctype[^>]*>`)

// withCSP puts the docCSP meta tag before anything the model wrote (after
// the doctype). The HTML parser takes a <meta> seen before <html> into an
// implied <head>, and folds the model's own <html>/<head> tags into those, so
// the policy is the first thing in <head> however the rest looks — inserting
// after the first "<head>" the text contains let a decoy in a comment
// (<!-- <head> -->) carry the CSP away while a real <script> ran. A second CSP
// in the document can only narrow it further: browsers enforce both. The
// charset declaration goes before even the CSP, so it is the first one the
// encoding prescan meets.
func withCSP(html string) string {
	meta := `<meta http-equiv="Content-Security-Policy" content="` + docCSP + `">`
	rest := html
	if loc := doctype.FindStringIndex(html); loc != nil {
		rest = html[loc[1]:]
	}
	return "<!DOCTYPE html>\n" + docCharset + "\n" + meta + "\n" + noDNSPrefetch + "\n" + rest
}

// fontHosts serve the template's web fonts (Inter, JetBrains Mono). They are
// the one network destination the PDF render keeps: the document is meant to
// look the same as when the user opens the HTML, and the CSP limits what goes
// there to stylesheet and font requests.
var fontHosts = []string{"fonts.googleapis.com", "fonts.gstatic.com"}

// offlineFlags cut the PDF render off every host but fontHosts, in case
// something in the model's HTML slips past sanitizeHTML and the CSP: names
// do not resolve (DNS lookups are a channel too), and every connection goes
// to a proxy that refuses it, which also stops a URL with an IP address in
// place of a name. Chrome's own background traffic is off as well.
var offlineFlags = []string{
	"--host-resolver-rules=MAP * ~NOTFOUND , EXCLUDE " + strings.Join(fontHosts, " , EXCLUDE "),
	"--proxy-server=http://127.0.0.1:9",
	"--proxy-bypass-list=" + strings.Join(fontHosts, ";"),
	"--dns-prefetch-disable",
	"--disable-background-networking",
	"--no-first-run",
}

// pdfTimeout bounds the headless print: a Chrome that hangs (it happens, e.g.
// on a first-run dialog) would otherwise hold the totem forever.
const pdfTimeout = 2 * time.Minute

func renderPDF(ctx context.Context, htmlPath, pdf string) (string, string) {
	chrome := lookChrome()
	if chrome == "" {
		return "", "Chrome/Chromium not found — open the HTML and print to PDF: " + htmlPath
	}
	abs, _ := filepath.Abs(htmlPath)
	flags := append([]string{"--headless", "--disable-gpu", "--no-pdf-header-footer"}, offlineFlags...)
	// Isolate from the user's real Chrome profile with a throwaway user-data-dir.
	if profile, err := os.MkdirTemp("", "ouija-chrome-"); err == nil {
		defer os.RemoveAll(profile)
		flags = append(flags, "--user-data-dir="+profile)
	}
	flags = append(flags, "--print-to-pdf="+pdf, fileURL(abs))
	ctx, cancel := context.WithTimeout(ctx, pdfTimeout)
	defer cancel()
	if err := exec.CommandContext(ctx, chrome, flags...).Run(); err != nil {
		return "", "PDF render failed (" + err.Error() + ") — open the HTML and print to PDF: " + htmlPath
	}
	return pdf, ""
}

// fileURL is the file:// URL of an absolute path, escaped (a space in the
// project path broke the old "file://"+path) and with Windows' drive letter
// after a slash (file:///C:/…).
func fileURL(abs string) string {
	p := filepath.ToSlash(abs)
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	return (&url.URL{Scheme: "file", Path: p}).String()
}

// lookChrome finds a Chromium-family browser; a variable so tests never
// launch one.
var lookChrome = findChrome

// findChrome looks on PATH, then where macOS and Windows install the browsers
// (neither puts them on PATH, so the PDF was never rendered there).
func findChrome() string {
	for _, c := range []string{"google-chrome-stable", "google-chrome", "chromium", "chromium-browser", "chrome", "msedge"} {
		if p, err := exec.LookPath(c); err == nil {
			return p
		}
	}
	var candidates []string
	switch runtime.GOOS {
	case "darwin":
		candidates = []string{
			"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
			"/Applications/Chromium.app/Contents/MacOS/Chromium",
			"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		}
	case "windows":
		for _, base := range []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")} {
			if base != "" {
				candidates = append(candidates,
					filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
					filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"))
			}
		}
	}
	for _, c := range candidates {
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			return c
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
	Warnings      []string `json:"warnings,omitempty"`
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
	for _, w := range r.Warnings {
		fmt.Fprintf(&b, "\n⚠ %s\n", w)
	}
	if len(r.OpenQuestions) > 0 {
		b.WriteString("\n## ⚠️  Open questions (verify before delivering)\n\n")
		for _, q := range r.OpenQuestions {
			fmt.Fprintf(&b, "- %s\n", q)
		}
	}
	return b.String()
}

// --- helpers ---

var (
	htmlStart     = regexp.MustCompile(`(?i)<!doctype html|<html`)
	htmlEnd       = regexp.MustCompile(`(?i)</html\s*>`)
	placeholderRe = regexp.MustCompile(`\{\{[A-Z_]+\}\}`)
)

// extractHTML takes the document out of the compose reply: from the first
// <!doctype>/<html> to the last </html>, so prose or a code fence around it
// never ends up as visible text. ok is false without a closing </html> — a
// reply cut off by the output limit, which used to be written (and printed
// to PDF) as if complete.
func extractHTML(reply string) (string, bool) {
	start := htmlStart.FindStringIndex(reply)
	ends := htmlEnd.FindAllStringIndex(reply, -1)
	if start == nil || len(ends) == 0 || ends[len(ends)-1][1] <= start[0] {
		return "", false
	}
	return reply[start[0]:ends[len(ends)-1][1]], true
}

// fillCover fills the template's cover and signature placeholders: from the
// fact sheet, today's date, and `totems.doc` (overridden by --set params).
func fillCover(tpl string, sheet *factSheet, dc Config, params map[string]any, now time.Time) string {
	param := func(key, fallback string) string {
		if v, ok := params[key].(string); ok && v != "" {
			return v
		}
		return fallback
	}
	esc := html.EscapeString
	client, author, brand := param("client", dc.Client), param("author", dc.Author), param("brand", dc.Brand)
	return strings.NewReplacer(
		"{{SHORT_TITLE}}", esc(sheet.Title),
		"{{SUBJECT}}", esc(sheet.Subtitle),
		"{{DOC_TYPE}}", esc(sheet.DocType),
		"{{CLIENT_NAME}}", esc(client),
		"{{PREPARED_BY}}", esc(author),
		"{{SIGNATURE_NAME}}", esc(author),
		"{{SIGNATURE_CONTACT}}", esc(param("contact", dc.Contact)),
		"{{LOGO_TEXTO}}", esc(brand),
		"{{BRAND_TAG}}", esc(brand),
		"{{COVER_FOOTER}}", esc(brand),
		"{{DISTRIBUTION}}", esc(param("distribution", "")),
		"{{FULL_DATE}}", now.Format("2 January 2006"),
		"{{LOCALE}}", esc(param("locale", cmp.Or(dc.Locale, "en"))),
	).Replace(tpl)
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
		"docType":       schema.Enum("kind of document", "manual", "assessment", "report"),
		"sections":      map[string]any{"type": "array", "items": section},
		"openQuestions": schema.Arr("facts that could not be verified / need confirmation"),
	}, "title", "subtitle", "docType", "sections", "openQuestions")
}
