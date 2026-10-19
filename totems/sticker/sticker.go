// Package sticker is the "sticker" totem: it scouts a project's metadata and
// generates a laptop-sticker SVG (ready to print at 5x5 cm) + optional PDF.
//
// Unlike task and doc, the compose step does NOT use the LLM — the SVG is a
// pre-designed template filled with real data from the scout. This gives
// consistent visual quality regardless of model, and cuts cost/tokens in half.
package sticker

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"html"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/registry"
	"github.com/discorgento/ouija/schema"
	"github.com/discorgento/ouija/totems/internal/seance"
)

func init() { registry.Register(&Totem{}) }

// Totem scouts the project and composes a laptop sticker with real metrics.
type Totem struct{}

func (t *Totem) Name() string { return "sticker" }
func (t *Totem) Summary() string {
	return "Generate a laptop sticker with real project metrics"
}

// InputSchema makes the free-text request optional: `ouija sticker` alone
// scouts the project, while the MCP tool and the CLI still accept a hint.
func (t *Totem) InputSchema() map[string]any {
	return schema.Obj(map[string]any{
		"task": schema.Str("optional hint about what to highlight"),
	})
}

//go:embed template.svg
var templateSVG string

func (t *Totem) Run(ctx context.Context, eng *engine.Engine, args registry.Args) (any, error) {

	cfg := eng.Config()

	// The optional hint (InputSchema's task) steers what to highlight.
	hint := ""
	if h := strings.TrimSpace(args.Task); h != "" {
		hint = "User's hint about what to highlight: " + h + "\n\n"
	}

	// 1) Scout -- read project files and extract metadata with read-only tools.
	scoutRes, err := eng.Run(ctx, engine.RunRequest{
		Label: "scout",
		System: "You scout a project to extract its tech metadata for a laptop sticker. " +
			"You MUST use the tools (read_file, grep, glob) to examine the code before calling submit_result. " +
			"If a field has no evidence use empty string or empty array. " +
			"Do NOT guess, infer, or repeat example values.",
		Prompt: hint +
			"Generate a sticker for this project.\n\n" +
			"Read the project files before calling submit_result.\n\n" +
			"Files to read:\n" +
			"1. The project manifest or README -> projectName and projectDescription\n" +
			"   (whichever exists: package.json, composer.json, go.mod, Cargo.toml,\n" +
			"    pyproject.toml, pom.xml, build.gradle, Gemfile, README.md — any layout)\n" +
			"2. Identify tech stack -> techTags (list of technologies: frameworks, languages, databases, tools)\n\n" +
			"VERSIONS AND MODULE COUNT ARE HANDLED AUTOMATICALLY.\n\n" +
			"CRITICAL RULES:\n" +
			"- Use empty string \"\" for fields you cannot find evidence for\n" +
			"- Do NOT write 'EMPTY', 'N/A', 'none' or any placeholder text\n" +
			"- Do NOT make up values\n" +
			"- Read files first, then call submit_result with the schema",
		UseTools:  true,
		Schema:    scoutSchema(),
		MaxTokens: 4096,
	})
	if err != nil {
		return nil, fmt.Errorf("scout failed: %w", err)
	}

	// Parse scout data, falling back to reasonable defaults.
	score := parseScout(scoutRes.Text)

	// 1b) Override factual fields with deterministic data from files.
	// LLM hallucinates versions and counts; Go reads the actual files.
	// Validated like the model's values: composer allows "*" or "dev-main".
	det := detectVersions(cfg.ProjectRoot)
	if v := validVersion(det.platform); v != "" {
		score.PlatformVersion = v
	}
	if v := validPHP(det.php); v != "" {
		score.PHPVersion = v
	}
	if v := validVersion(det.theme); v != "" {
		score.ThemeVersion = v
	}
	if det.modules > 0 {
		score.CustomModules = fmt.Sprintf("%d", det.modules)
	} else if score.CustomModules == "" {
		score.CustomModules = "0"
	}

	// 2) Fill the SVG template with real project data.
	svg := fillTemplate(score)

	// 3) Write SVG inside the scope docs/sticker/ directory.
	// SafeResolve (symlink-aware), not SafeJoin: a docs/ symlink planted in
	// the repo must not redirect the write outside the project.
	slug := slugFrom(score)
	svgPath, err := engine.SafeResolve(ctx, cfg.ProjectRoot, filepath.Join("docs", "sticker", slug+".sticker.svg"))
	if err != nil {
		return nil, fmt.Errorf("invalid output path: %w", err)
	}
	outDir := filepath.Dir(svgPath)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", outDir, err)
	}
	if err := os.WriteFile(svgPath, []byte(svg), 0o644); err != nil {
		return nil, fmt.Errorf("cannot write svg: %w", err)
	}

	return &stickerResult{SVG: svgPath}, nil
}

// --- scout data model ---

// scoutData holds the parsed, validated project metadata for the sticker template.
type scoutData struct {
	ProjectName     string   `json:"projectName"`
	Tagline         string   `json:"projectDescription"`
	PlatformVersion string   `json:"platformVersion"`
	PHPVersion      string   `json:"phpVersion"`
	ThemeVersion    string   `json:"themeVersion"`
	CustomModules   string   `json:"customModules"`
	TechTags        []string `json:"techTags"`
}

// parseScout unmarshals the scout JSON into a struct, cleaning up fields.
func parseScout(raw string) *scoutData {
	var data scoutData
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return &scoutData{ProjectName: "Project"}
	}
	// Sanitize string fields: treat placeholder values as empty.
	data.ProjectName = cleanProjectName(emptyIfPlaceholder(data.ProjectName))
	if data.ProjectName == "" {
		data.ProjectName = "Project"
	}
	data.Tagline = emptyIfPlaceholder(data.Tagline)
	data.PlatformVersion = validVersion(emptyIfPlaceholder(data.PlatformVersion))
	data.PHPVersion = validPHP(emptyIfPlaceholder(data.PHPVersion))
	data.ThemeVersion = validVersion(emptyIfPlaceholder(data.ThemeVersion))
	data.CustomModules = emptyIfPlaceholder(data.CustomModules)
	// Clean up tech tags: remove "(none)", "(not found)" and similar noise.
	clean := make([]string, 0, len(data.TechTags))
	for _, tag := range data.TechTags {
		tag = emptyIfPlaceholder(strings.TrimSpace(tag))
		if tag == "" {
			continue
		}
		if idx := strings.Index(tag, " ("); idx > 0 {
			tag = tag[:idx]
		}
		tag = strings.TrimSpace(tag)
		if tag != "" {
			clean = append(clean, tag)
		}
	}
	data.TechTags = clean
	return &data
}

// emptyIfPlaceholder returns "" for common model placeholder values.
func emptyIfPlaceholder(s string) string {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "", "EMPTY", "N/A", "NONE", "NULL", "TBD", "TODO", "UNKNOWN", "NOT FOUND", "NOT AVAILABLE":
		return ""
	}
	return s
}

// versionPattern matches a major.minor or major.minor.patch version number.
var versionPattern = regexp.MustCompile(`^(\d+)\.(\d+)(\.\d+)?(-p\d+)?$`)

// validPHP returns "" if the version doesn't look like a real PHP version.
// This catches LLM hallucinations like "PHP 7.5" — PHP 7.x maxed at 7.4.
func validPHP(v string) string {
	if v == "" {
		return ""
	}
	m := versionPattern.FindStringSubmatch(v)
	if m == nil {
		return ""
	}
	major, minor := m[1], m[2]
	switch major {
	case "5":
		if minor != "3" && minor != "4" && minor != "5" && minor != "6" {
			return ""
		}
	case "7":
		if minor != "0" && minor != "1" && minor != "2" && minor != "3" && minor != "4" {
			return ""
		}
	case "8":
		// 8.0 through 8.x (currently up to 8.4)
		// Accept any minor for forward compatibility.
	default:
		return ""
	}
	return v
}

// validVersion returns "" if the version string doesn't look like a real
// semver. This rejects things like "0.1" or "none" from hallucinating models.
func validVersion(v string) string {
	if v == "" {
		return ""
	}
	if versionPattern.MatchString(v) {
		return v
	}
	return ""
}

// cleanProjectName turns a manifest name into a short display name. A path
// (go.mod's github.com/acme/shop, composer's acme/shop, npm's @acme/shop)
// gives its last segment — the vendor or the host named every project the
// same, and the same file name. A long multi-word name keeps its first word
// when that word says something on its own ("Laravel Framework"), else stays
// whole for adaptName to shorten ("The Ouija Harness").
func cleanProjectName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndex(name, "/"); i >= 0 && i < len(name)-1 {
		name = upperFirst(name[i+1:])
	}
	if words := strings.Fields(name); len(words) > 1 && utf8.RuneCountInString(name) > 9 &&
		utf8.RuneCountInString(words[0]) >= 4 {
		return words[0]
	}
	return name
}

// upperFirst upper-cases the first character (a rune: by bytes, "école"
// became invalid UTF-8 and broke the SVG).
func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[n:]
}

// --- deterministic version detection (no LLM) ---

// detected holds versions read directly from the filesystem, bypassing the LLM
// entirely for fields where hallucination is common.
type detected struct {
	platform string
	php      string
	theme    string
	modules  int
}

// detectVersions reads composer.json and counts custom modules in Go.
// This is more reliable than asking the LLM to do it with tools.
func detectVersions(projectRoot string) detected {
	var d detected

	// Read composer.json from the project root.
	composerPath, err := engine.SafeJoin(projectRoot, "composer.json")
	if err != nil {
		return d
	}
	raw, err := os.ReadFile(composerPath)
	if err != nil {
		return d
	}

	// Parse the require section only.
	var root struct {
		Name        string            `json:"name"`
		Description string            `json:"description"`
		Require     map[string]string `json:"require"`
	}
	if err := json.Unmarshal(raw, &root); err != nil {
		return d
	}
	if root.Require == nil {
		return d
	}

	d.platform = stripVersionCaret(cmp.Or(root.Require["magento/product-community-edition"],
		root.Require["magento/product-enterprise-edition"])) // Open Source or Commerce
	d.php = stripVersionCaret(root.Require["php"])

	// The Hyvä theme version: the default theme package when required, else
	// the first hyva-themes/* package by name — not the first a map happens
	// to yield, which changed the sticker from run to run.
	if v := root.Require["hyva-themes/magento2-default-theme"]; v != "" {
		d.theme = stripVersionCaret(v)
	} else {
		for _, pkg := range slices.Sorted(maps.Keys(root.Require)) {
			if strings.HasPrefix(pkg, "hyva-themes/") {
				d.theme = stripVersionCaret(root.Require[pkg])
				break
			}
		}
	}

	// Count custom modules: app/code/*/*/registration.php
	codeDir, err := engine.SafeJoin(projectRoot, "app/code")
	if err != nil {
		return d
	}
	entries, err := os.ReadDir(codeDir)
	if err != nil {
		return d // no app/code directory
	}
	for _, vendor := range entries {
		if !vendor.IsDir() {
			continue
		}
		vendorPath := filepath.Join(codeDir, vendor.Name())
		modEntries, err := os.ReadDir(vendorPath)
		if err != nil {
			continue
		}
		for _, mod := range modEntries {
			if !mod.IsDir() {
				continue
			}
			regPath, err := engine.SafeJoin(projectRoot, filepath.Join("app/code", vendor.Name(), mod.Name(), "registration.php"))
			if err != nil {
				continue
			}
			if _, err := os.Stat(regPath); err == nil {
				d.modules++
			}
		}
	}

	return d
}

// stripVersionCaret removes ~, ^, >=, >, <=, < prefixes from version strings
// and returns just the major.minor.patch part.
//
//	">=8.2"   -> "8.2"
//	"^2.4.7" -> "2.4.7"
//	"~1.4.0" -> "1.4.0"
//	"2.4.7-p10" -> "2.4.7-p10"
func stripVersionCaret(v string) string {
	v = strings.TrimSpace(v)
	if idx := strings.IndexAny(v, "|,"); idx > 0 {
		v = strings.TrimSpace(v[:idx])
	}
	// Strip leading operators.
	for _, op := range []string{">=", "<=", "^", "~", ">", "<", "!"} {
		if strings.HasPrefix(v, op) {
			v = strings.TrimPrefix(v, op)
			v = strings.TrimSpace(v)
			break
		}
	}
	// The first alternative of a range ("~8.1.0||~8.2.0", "^1.3 | ^2", "8.1, 8.2"):
	// they are listed oldest first, and that is the version guaranteed.
	if idx := strings.IndexAny(v, " |,"); idx > 0 {
		v = v[:idx]
	}
	return v
}

// --- template filling ---

// fillTemplate substitutes {{PLACEHOLDERS}} in the embedded SVG with real
// data, XML-escaped: a tagline "Fast & simple" or a tag "R&D" would otherwise
// break the document, and text from the repo must not become markup. One
// pass, so a value holding "{{TAGLINE}}" is never substituted again.
func fillTemplate(d *scoutData) string {
	esc := html.EscapeString

	// Project name: truncate and adapt font-size to prevent overflow.
	name, size := adaptName(d.ProjectName)

	// Bottom row: module count + platform version (show only when meaningful).
	modulesText := ""
	if d.CustomModules != "" && d.CustomModules != "0" {
		modulesText = "Modules: " + d.CustomModules
	}

	pairs := []string{
		"{{PROJECT_NAME}}", esc(name),
		"{{NAME_SIZE}}", size,
		// Tagline: truncate to fit ~44 chars at 16px.
		"{{TAGLINE}}", esc(truncate(d.Tagline, 44)),
		"{{BOTTOM_LEFT}}", esc(modulesText),
		"{{BOTTOM_RIGHT}}", esc(d.PlatformVersion),
	}

	// Build badge values and truncate each to fit its pill slot.
	// Wide pills (cols 1,2,4,5): 204px at 16px font ~ 13 chars → max 12 + "..."
	// Narrow pills (cols 3,6): 120px at 16px font ~ 7 chars → max 5 + "..."
	pills := buildBadges(d)
	limits := []int{12, 12, 5, 12, 12, 5}
	for i := 0; i < 6 && i < len(pills); i++ {
		pairs = append(pairs, fmt.Sprintf("{{BADGE_%d}}", i+1), esc(truncate(pills[i], limits[i])))
	}
	return strings.NewReplacer(pairs...).Replace(templateSVG)
}

// adaptName truncates the project name and calculates the right font-size.
// Returns (displayName, fontSize).
func adaptName(name string) (string, string) {
	runes := []rune(name)
	n := len(runes)
	switch {
	case n <= 6:
		return name, "96" // short name: big and bold
	case n <= 9:
		return name, "72" // medium name: slightly smaller
	default:
		// Long name: truncate to 9 chars + "..." and use medium size.
		return string(runes[:9]) + "...", "72"
	}
}

// buildBadges maps the scout data into the 6 badge slots.
//
// Layout in the template (3 columns x 2 rows):
//
//	[ BADGE_1 (wide) ][ BADGE_2 (wide) ][ BADGE_3 (narrow) ]
//	[ BADGE_4 (wide) ][ BADGE_5 (wide) ][ BADGE_6 (narrow) ]
//
// Priority: version-specific badges first (platform/PHP/theme), then tech tags.
// Always returns exactly 6 items — remaining slots get generic labels.
func buildBadges(d *scoutData) []string {
	// Versions go in the wide pills (slots 1, 2, 4, 5): in a narrow one
	// "Theme 1.3.10" was cut to "Theme...". Labels stay short for the same
	// reason (M2 is the Magento platform, Hyvä its theme — what composer.json
	// detection reads).
	var versions []string
	if d.PlatformVersion != "" {
		versions = append(versions, "M2 "+d.PlatformVersion)
	}
	if d.PHPVersion != "" {
		versions = append(versions, "PHP "+d.PHPVersion)
	}
	if d.ThemeVersion != "" {
		versions = append(versions, "Hyvä "+d.ThemeVersion)
	}

	badges := make([]string, 6)
	wide, narrow := []int{0, 1, 3, 4}, []int{2, 5}
	for i, v := range versions { // at most 3, so a wide slot each
		badges[wide[i]] = v
	}
	// Tech tags fill the free slots in order; one too long for a narrow pill
	// skips to the next wide one. A PHP tag would repeat the PHP version.
	isNarrow := func(i int) bool { return slices.Contains(narrow, i) }
	for _, tag := range d.TechTags {
		if strings.Contains(strings.ToLower(tag), "php") {
			continue
		}
		for i := range badges {
			if badges[i] == "" && (!isNarrow(i) || utf8.RuneCountInString(tag) <= 5) {
				badges[i] = tag
				break
			}
		}
	}
	// Remaining slots get generic labels so no badge is ever empty.
	fallbacks := []string{"Dev", "CLI", "API", "Web", "Stack", "App"}
	for i := range badges {
		if badges[i] == "" {
			badges[i], fallbacks = fallbacks[0], fallbacks[1:]
		}
	}
	return badges
}

// truncate shortens s to at most n characters, appending "..." when cut.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}

// --- result ---

type stickerResult struct {
	SVG string `json:"svg"`
}

func (r *stickerResult) Render() string {
	var b strings.Builder
	b.WriteString("# Sticker generated\n\n")
	fmt.Fprintf(&b, "- SVG: %s\n", r.SVG)
	b.WriteString("\n  Print at 5x5 cm (2x2 in) on sticker paper.\n")
	return b.String()
}

// --- helpers ---

func slugFrom(d *scoutData) string {
	// A name with no ASCII letters or digits slugs to "": that would write
	// a hidden ".sticker.svg".
	return cmp.Or(seance.Slugify(d.ProjectName), "project-sticker")
}

// --- schemas ---

func scoutSchema() map[string]any {
	return schema.Obj(map[string]any{
		"projectName":        schema.Str("project name from the project manifest (package.json, composer.json, go.mod, …) or README"),
		"projectDescription": schema.Str("short one-line project description (tagline)"),
		"techTags":           schema.Arr("key technologies as compact tags, e.g. PHP, React, PostgreSQL, Redis"),
	}, "projectName", "techTags")
}
