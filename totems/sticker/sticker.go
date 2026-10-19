// Package sticker is the "sticker" totem: it scouts a project's metadata and
// generates a laptop-sticker SVG (ready to print at 5x5 cm) + optional PDF.
//
// Unlike task and doc, the compose step does NOT use the LLM — the SVG is a
// pre-designed template filled with real data from the scout. This gives
// consistent visual quality regardless of model, and cuts cost/tokens in half.
package sticker

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/discorgento/ouija/internal/engine"
	"github.com/discorgento/ouija/internal/registry"
	"github.com/discorgento/ouija/internal/schema"
)

func init() { registry.Register(&Totem{}) }

// Totem scouts the project and composes a laptop sticker with real metrics.
type Totem struct{}

func (t *Totem) Name() string { return "sticker" }
func (t *Totem) Summary() string {
	return "Generate a laptop sticker with real project metrics"
}
func (t *Totem) Init(_ context.Context, _ *engine.Engine, _ *engine.Config) error { return nil }
func (t *Totem) Cleanup() error                                                   { return nil }

//go:embed template.svg
var templateSVG string

func (t *Totem) Run(ctx context.Context, eng *engine.Engine, args registry.Args) (any, error) {

	cfg := eng.Config()

	// Build a description of what the project does, for the LLM prompt.
	taskDesc := strings.TrimSpace(args.Task)
	if taskDesc == "" {
		taskDesc = "Generate a sticker for this project"
	}

	// 1) Scout -- read project files and extract metadata with read-only tools.
	scoutRes, err := eng.Run(ctx, engine.RunRequest{
		Label: "scout",
		System: "You scout a project to extract its tech metadata for a laptop sticker. " +
			"You MUST use the tools (read_file, grep, glob) to examine the code before calling submit_result. " +
			"If a field has no evidence use empty string or empty array. " +
			"Do NOT guess, infer, or repeat example values.",
		Prompt: fmt.Sprintf(
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
				"- Read files first, then call submit_result with the schema"),
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
	det := detectVersions(cfg.ProjectRoot)
	if det.platform != "" {
		score.PlatformVersion = det.platform
	}
	if det.php != "" {
		score.PHPVersion = det.php
	}
	if det.theme != "" {
		score.ThemeVersion = det.theme
	}
	if det.modules > 0 {
		score.CustomModules = fmt.Sprintf("%d", det.modules)
	} else if score.CustomModules == "" {
		score.CustomModules = "0"
	}

	// 2) Fill the SVG template with real project data.
	svg := fillTemplate(score)

	// 3) Write SVG inside the scope docs/sticker/ directory.
	outDir, err := engine.SafeJoin(cfg.ProjectRoot, filepath.Join("docs", "sticker"))
	if err != nil {
		return nil, fmt.Errorf("invalid scope: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, fmt.Errorf("cannot create %s: %w", outDir, err)
	}

	slug := slugFrom(score)
	svgPath := filepath.Join(outDir, slug+".sticker.svg")
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
	ExtraInfo       []string `json:"extraInfo"`
	// Computed fields for template rendering.
	NameSize string // adaptive font-size injected into SVG
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

// cleanProjectName turns a composer package name or long name into a short
// display name.
func cleanProjectName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	// If it contains "/", it's a composer package name. Extract the vendor part.
	if idx := strings.Index(name, "/"); idx > 0 {
		vendor := name[:idx]
		// Capitalize first letter.
		if len(vendor) > 0 {
			vendor = strings.ToUpper(vendor[:1]) + vendor[1:]
		}
		return vendor
	}
	// If it contains spaces, take the first word.
	if idx := strings.Index(name, " "); idx > 0 {
		return name[:idx]
	}
	return name
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

	d.platform = stripVersionCaret(root.Require["magento/product-community-edition"])
	d.php = stripVersionCaret(root.Require["php"])

	// Check any hyva-themes/* package.
	for pkg, ver := range root.Require {
		if strings.HasPrefix(pkg, "hyva-themes/") || strings.HasPrefix(pkg, "hyva-themes ") {
			d.theme = stripVersionCaret(ver)
			break
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
	// Strip leading operators.
	for _, op := range []string{">=", "<=", "^", "~", ">", "<", "!"} {
		if strings.HasPrefix(v, op) {
			v = strings.TrimPrefix(v, op)
			v = strings.TrimSpace(v)
			break
		}
	}
	// Strip anything after a space (e.g. "|| 8.3").
	if idx := strings.Index(v, " "); idx > 0 {
		v = v[:idx]
	}
	return v
}

// --- template filling ---

// fillTemplate substitutes {{PLACEHOLDERS}} in the embedded SVG with real data.
func fillTemplate(d *scoutData) string {
	sv := templateSVG

	// Project name: truncate and adapt font-size to prevent overflow.
	name, size := adaptName(d.ProjectName)
	sv = strings.ReplaceAll(sv, "{{PROJECT_NAME}}", name)
	sv = strings.ReplaceAll(sv, "{{NAME_SIZE}}", size)

	// Tagline: truncate to fit ~44 chars at 16px.
	sv = strings.ReplaceAll(sv, "{{TAGLINE}}", truncate(d.Tagline, 44))

	// Bottom row: module count + platform version (show only when meaningful).
	modulesText := ""
	if d.CustomModules != "" && d.CustomModules != "0" {
		modulesText = "Modules: " + d.CustomModules
	}
	sv = strings.ReplaceAll(sv, "{{BOTTOM_LEFT}}", modulesText)
	sv = strings.ReplaceAll(sv, "{{BOTTOM_RIGHT}}", d.PlatformVersion)

	// Build badge values and truncate each to fit its pill slot.
	// Wide pills (cols 1,2,4,5): 204px at 16px font ~ 13 chars → max 12 + "..."
	// Narrow pills (cols 3,6): 120px at 16px font ~ 7 chars → max 5 + "..."
	pills := buildBadges(d)
	limits := []int{12, 12, 5, 12, 12, 5}
	for i := 0; i < 6 && i < len(pills); i++ {
		key := fmt.Sprintf("{{BADGE_%d}}", i+1)
		sv = strings.ReplaceAll(sv, key, truncate(pills[i], limits[i]))
	}

	return sv
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
	var badges []string

	// 1) Version-specific badges (only when data exists).
	if d.PlatformVersion != "" {
		badges = append(badges, "Platform "+d.PlatformVersion)
	}
	if d.PHPVersion != "" {
		badges = append(badges, "PHP "+d.PHPVersion)
	}
	if d.ThemeVersion != "" {
		badges = append(badges, "Theme "+d.ThemeVersion)
	}

	// 2) Fill with tech tags, skipping any that duplicate version labels.
	for _, tag := range d.TechTags {
		if len(badges) >= 6 {
			break
		}
		tagLower := strings.ToLower(tag)
		if strings.Contains(tagLower, "php") {
			continue
		}
		badges = append(badges, tag)
	}

	// 3) Remaining slots get generic labels so no badge is ever empty.
	fallbacks := []string{"Dev", "CLI", "API", "Web", "Stack", "App"}
	for _, fb := range fallbacks {
		if len(badges) >= 6 {
			break
		}
		badges = append(badges, fb)
	}

	return badges[:6]
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
	name := strings.TrimSpace(d.ProjectName)
	if name != "" {
		return slugify(name)
	}
	return "project-sticker"
}

var nonSlug = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = nonSlug.ReplaceAllString(s, "-")
	return strings.Trim(s, "-")
}

// --- schemas ---

func scoutSchema() map[string]any {
	return schema.Obj(map[string]any{
		"projectName":        schema.Str("project name from the project manifest (package.json, composer.json, go.mod, …) or README"),
		"projectDescription": schema.Str("short one-line project description (tagline)"),
		"techTags":           schema.Arr("key technologies as compact tags, e.g. PHP, React, PostgreSQL, Redis"),
	}, "projectName", "techTags")
}
