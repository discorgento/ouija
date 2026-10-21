package commands

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"charm.land/huh/v2"
	"charm.land/huh/v2/spinner"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/providers"
	"github.com/discorgento/ouija/internal/models"
	"github.com/discorgento/ouija/internal/ouijaconfig"
	"github.com/discorgento/ouija/internal/theme"
)

// providerInfo is what the wizard says about a provider: its name, one line
// on what it is, and where its API key comes from ("" when it has no page).
type providerInfo struct {
	name, blurb, keyURL, keyHint string
}

var providerInfos = map[string]providerInfo{
	"anthropic":  {"Anthropic", "Claude models", "https://console.anthropic.com/settings/keys", "sk-ant-…"},
	"openai":     {"OpenAI", "GPT models", "https://platform.openai.com/api-keys", "sk-…"},
	"openrouter": {"OpenRouter", "hundreds of models, some free", "https://openrouter.ai/keys", "sk-or-…"},
	"opencode":   {"OpenCode Zen", "paid models", "", ""},
	"ollama":     {"Ollama", "runs on your computer", "", ""},
}

// formWidth keeps every screen of the wizard the same width.
const formWidth = 76

// info is the wizard's description of provider, falling back to the
// catalog's label for a provider it does not know.
func info(provider, label string) providerInfo {
	if pi, ok := providerInfos[provider]; ok {
		return pi
	}
	return providerInfo{name: label}
}

// Setup is the global config wizard, in three steps: the provider, its API
// key (skipped for a provider that needs none) and the default model, listed
// from the provider's API with the key just typed. Saved to
// ~/.config/ouija/config.json.
func Setup(args []string) error {
	ms := models.Load()
	// Only providers that are actually registered can be configured — the
	// picker must not offer a name that BuildProvider would reject.
	provNames, provLabels := ms.ProvidersFor(providers.Names())

	// Pre-fill from existing config. One that does not parse stops here:
	// saving the form over it would lose every other key and trusted project.
	existing, err := ouijaconfig.Load()
	if err != nil {
		return fmt.Errorf("%w — fix the file (or move it away) and run setup again", err)
	}
	savedKey := func(provider string) string {
		if existing == nil {
			return ""
		}
		return existing.APIKeys[provider]
	}

	provider, model := provNames[0], ""
	if existing != nil && existing.DefaultProvider() != "" {
		provider, model = existing.DefaultProvider(), existing.DefaultModel()
	}
	label := func(p string) string {
		if i := slices.Index(provNames, p); i >= 0 {
			return info(p, provLabels[i]).name
		}
		return p
	}

	printBanner()
	out := colorprofile.NewWriter(os.Stdout, os.Environ())
	fmt.Fprintln(out, setupIntro(displayPath(ouijaconfig.Path())))

	// Step 1: the provider.
	if err := providerForm(provNames, provLabels, &provider).Run(); err != nil {
		return setupAborted(out, err)
	}

	// Step 2: its key, checked right away by listing the provider's models
	// with it — a rejected key is asked again here, not found in the chat.
	var (
		key     string
		list    []models.Model
		listErr error
	)
	fetch := func() {
		var p engine.Provider
		if key != "" || providers.Keyless(provider) {
			p, _ = providers.Build(provider, key)
		}
		list, listErr = ms.Fetch(context.Background(), provider, p)
	}
	check := func() {
		if err := spinner.New().Title(" Checking " + label(provider) + "…").Action(fetch).Run(); err != nil {
			fetch() // no terminal for the spinner: just fetch
		}
	}
	if providers.Keyless(provider) {
		check()
	} else {
		rejected := ""
		for attempt := 1; ; attempt++ {
			typed := ""
			help := keyHelp(provider, savedKey(provider))
			if rejected != "" {
				help = lipgloss.NewStyle().Foreground(theme.Red).Render("✗ "+rejected) + "\n" + help
			}
			if err := keyForm(label(provider), help, info(provider, "").keyHint, savedKey(provider) != "" && rejected == "", &typed).Run(); err != nil {
				return setupAborted(out, err)
			}
			key = strings.TrimSpace(typed)
			if key == "" {
				key = savedKey(provider)
			}
			check()
			if code, ok := engine.Code(listErr); !ok || code != engine.ErrCodeAuth {
				break
			}
			if attempt == 3 {
				return fmt.Errorf("%s rejected the key three times — check it at %s and run `ouija setup` again", label(provider), cmp.Or(info(provider, "").keyURL, "the provider's site"))
			}
			rejected = label(provider) + " rejected that key. Check it and paste it again."
		}
	}

	modelOpts := modelOptions(ms.Catalog(provider), list)
	if len(modelOpts) == 0 {
		// Nothing to pick from: a provider with no suggestions and no list.
		return setupModelByName(out, existing, provider, key, model, label(provider))
	}
	if model == "" || !slices.ContainsFunc(modelOpts, func(o huh.Option[string]) bool { return o.Value == model }) {
		model = modelOpts[0].Value
	}
	desc := "★ suggested · type / to search · ↑↓ to move"
	var warnings []string
	switch {
	case listErr != nil && providers.Keyless(provider):
		// Ollama not installed or not running: say how to get it going.
		desc = "Ollama did not answer, so these are the suggested models.\n" + desc
		warnings = append(warnings, "Ollama is not running: install it from https://ollama.com, then run `ollama pull "+model0(ms, provider)+"`")
	case listErr != nil:
		desc = "Could not list " + label(provider) + "'s models (" + firstLine(listErr.Error()) + "), so these are the suggested ones.\n" + desc
	}
	form := modelForm(modelOpts, desc, &model)
	if err := form.Run(); err != nil {
		return setupAborted(out, err)
	}

	cfg := buildGlobalConfig(existing, provider, key, model)
	if err := ouijaconfig.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	verb := "saved"
	if existing != nil {
		verb = "updated"
	}
	rows := [][2]string{{"provider", label(provider)}, {"model", model}}
	if key != "" {
		rows = append(rows, [2]string{"key", "saved (" + maskKey(key) + ")"})
	}
	rows = append(rows, [2]string{"file", displayPath(ouijaconfig.Path())})
	printDone("Config "+verb, rows, warnings, "cd into a project and run `ouija` (`ouija doctor` checks everything)")
	return nil
}

// model0 is provider's first suggested model, for "pull this one" hints.
func model0(ms *models.Store, provider string) string {
	if c := ms.Catalog(provider); len(c) > 0 {
		return c[0].ID
	}
	return "a model"
}

// providerForm is step 1: which provider runs the model.
func providerForm(names, labels []string, provider *string) *huh.Form {
	nameW, blurbW := 0, 0
	for i, name := range names {
		pi := info(name, labels[i])
		nameW, blurbW = max(nameW, lipgloss.Width(pi.name)), max(blurbW, lipgloss.Width(pi.blurb))
	}
	opts := make([]huh.Option[string], len(names))
	for i, name := range names {
		pi := info(name, labels[i])
		badge := "API key"
		if providers.Keyless(name) {
			badge = "free · no key"
		}
		// Plain text: the highlighted row is a band of color, and a color
		// inside the label would cut it short.
		opts[i] = huh.NewOption(fmt.Sprintf(" %-*s  %-*s  %s ", nameW, pi.name, blurbW, pi.blurb, badge), name)
	}
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Which provider runs the model?").
				Description("You can switch later: /provider in the chat, or `ouija setup` again.").
				Options(opts...).
				Value(provider),
		).Title(stepTitle(1)),
	).WithTheme(ouijaTheme{}).WithWidth(formWidth)
}

// keyForm is step 2: the picked provider's API key. With one saved, a blank
// field keeps it.
func keyForm(name, help, hint string, hasSaved bool, key *string) *huh.Form {
	if hint == "" {
		hint = "paste the key here"
	}
	return huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("Paste your "+name+" API key").
				Description(help).
				Prompt("› ").
				Placeholder(hint).
				EchoMode(huh.EchoModePassword).
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" && !hasSaved {
						return errors.New("paste a key (or Ctrl+C, run setup again and pick Ollama, which needs none)")
					}
					return nil
				}).
				Value(key),
		).Title(stepTitle(2)),
	).WithTheme(ouijaTheme{}).WithWidth(formWidth)
}

// modelForm is step 3: the default model, a few rows at a time.
func modelForm(opts []huh.Option[string], desc string, model *string) *huh.Form {
	return huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[string]().
				Title("Default model").
				Description(desc).
				Options(opts...).
				Height(12).
				Value(model),
		).Title(stepTitle(3)),
	).WithTheme(ouijaTheme{}).WithWidth(formWidth)
}

// stepTitle is the stepper above a step: the steps done are green and
// checked, the current one pink, the ones to come dim.
func stepTitle(n int) string {
	done := lipgloss.NewStyle().Foreground(theme.Green)
	cur := lipgloss.NewStyle().Foreground(theme.Pink).Bold(true)
	next := lipgloss.NewStyle().Foreground(theme.Muted)
	sep := next.Render("  ›  ")
	var parts []string
	for i, name := range []string{"Provider", "API key", "Model"} {
		switch {
		case i+1 < n:
			parts = append(parts, done.Render("✓ "+name))
		case i+1 == n:
			parts = append(parts, cur.Render(fmt.Sprintf("%d %s", i+1, name)))
		default:
			parts = append(parts, next.Render(fmt.Sprintf("%d %s", i+1, name)))
		}
	}
	return strings.Join(parts, sep)
}

// setupIntro is the card above the wizard: what it asks, and where it saves.
func setupIntro(path string) string {
	num := lipgloss.NewStyle().Bold(true).Foreground(theme.Pink)
	word := lipgloss.NewStyle().Bold(true).Foreground(theme.Text)
	dim := lipgloss.NewStyle().Foreground(theme.Muted)
	step := func(n, w, d string) string {
		return num.Render(n) + "  " + word.Render(fmt.Sprintf("%-9s", w)) + dim.Render(d)
	}
	body := strings.Join([]string{
		word.Render("Let's set up ouija: three quick steps"),
		"",
		step("1", "provider", "who runs the model"),
		step("2", "API key", "skipped for Ollama, which runs locally"),
		step("3", "model", "the default; switch any time with /model in the chat"),
		"",
		dim.Render("Saved to " + path + " (only you can read it)."),
		dim.Render("Ctrl+C cancels at any step."),
	}, "\n")
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(theme.Lavender).Padding(1, 2).Width(formWidth).Render(body)
}

// keyHelp is the description under the key field: where to get one, where it
// is kept, and that a saved key survives a blank field.
func keyHelp(provider, saved string) string {
	var lines []string
	if u := info(provider, provider).keyURL; u != "" {
		lines = append(lines, "Get one at "+u)
	}
	lines = append(lines, "It is saved on this computer only.")
	if saved != "" {
		lines = append(lines, "Leave it blank to keep the saved key ("+maskKey(saved)+").")
	}
	return strings.Join(lines, "\n")
}

// setupAborted turns a cancelled form into a clean exit that says nothing
// was saved, and passes any other error on.
func setupAborted(out *colorprofile.Writer, err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		fmt.Fprintln(out, docDim.Render("  setup cancelled — nothing was saved"))
		return nil
	}
	return fmt.Errorf("setup: %w", err)
}

// buildGlobalConfig assembles the config the wizard saves. It starts from the
// existing config, so what the wizard does not show survives — the trusted
// projects (`ouija trust`) and the other providers' keys — and only sets the
// picked provider's key (a blank one keeps the saved key) and the defaults.
func buildGlobalConfig(existing *ouijaconfig.Config, provider, key, model string) *ouijaconfig.Config {
	cfg := &ouijaconfig.Config{}
	if existing != nil {
		*cfg = *existing
		cfg.APIKeys = maps.Clone(existing.APIKeys)
		cfg.TrustedProjects = maps.Clone(existing.TrustedProjects)
	}
	if cfg.APIKeys == nil {
		cfg.APIKeys = map[string]string{}
	}
	if key != "" {
		cfg.APIKeys[provider] = key
	}
	cfg.Defaults = &ouijaconfig.Defaults{Provider: provider, Model: model}
	cfg.Provider, cfg.Model = "", "" // legacy top-level defaults, superseded
	return cfg
}

// setupModelByName is step 3 when there is no list to pick from: the model
// is typed by name.
func setupModelByName(out *colorprofile.Writer, existing *ouijaconfig.Config, provider, key, model, name string) error {
	form := huh.NewForm(huh.NewGroup(
		huh.NewInput().
			Title("Default model").
			Description(name + " lists no models here: type the model's ID.").
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("type a model ID")
				}
				return nil
			}).
			Value(&model),
	).Title(stepTitle(3))).WithTheme(ouijaTheme{}).WithWidth(formWidth)
	if err := form.Run(); err != nil {
		return setupAborted(out, err)
	}
	if err := ouijaconfig.Save(buildGlobalConfig(existing, provider, key, strings.TrimSpace(model))); err != nil {
		return fmt.Errorf("save config: %w", err)
	}
	printDone("Config saved", [][2]string{{"provider", name}, {"model", strings.TrimSpace(model)}, {"file", displayPath(ouijaconfig.Path())}}, nil, "cd into a project and run `ouija`")
	return nil
}

// providerModelOptions lists provider's models for a picker, asking its API
// when there is a key to ask with (or none is needed).
func providerModelOptions(ms *models.Store, provider, key string) []huh.Option[string] {
	var p engine.Provider
	if key != "" || providers.Keyless(provider) {
		p, _ = providers.Build(provider, key)
	}
	return modelOptions(ms.Catalog(provider), ms.List(context.Background(), provider, p))
}

// modelOptions is the model picker: the built-in suggestions first, starred,
// then the rest of the provider's list. A provider list of hundreds stays
// usable that way — the picker shows a few rows at a time and filters with /.
func modelOptions(suggested, list []models.Model) []huh.Option[string] {
	// Columns, in plain text (the highlighted row is a band of color): the
	// name, the ID when it differs, and "free".
	isFree := func(m models.Model) bool {
		return strings.HasSuffix(m.ID, ":free") || strings.HasSuffix(m.ID, "-free") || strings.HasSuffix(m.Label, "(free)")
	}
	nameOf := func(m models.Model) string {
		if m.Label == "" || m.Label == m.ID {
			return m.ID
		}
		return strings.TrimSuffix(m.Label, " (free)")
	}
	nameW := 0
	for _, m := range append(slices.Clone(suggested), list...) {
		nameW = max(nameW, lipgloss.Width(nameOf(m)))
	}
	nameW = min(nameW, 30)
	labelOf := func(m models.Model) string {
		text := fmt.Sprintf("%-*s", nameW, nameOf(m))
		if nameOf(m) != m.ID {
			text += "  " + m.ID
		}
		if isFree(m) {
			text += "  · free"
		}
		return text + " "
	}
	inList := map[string]bool{}
	for _, m := range list {
		inList[m.ID] = true
	}
	var opts []huh.Option[string]
	seen := map[string]bool{}
	for _, m := range suggested {
		// A suggestion the provider no longer lists is left out; with no
		// list at all (offline), the suggestions are all there is.
		if len(list) > 0 && !inList[m.ID] {
			continue
		}
		opts = append(opts, huh.NewOption("★ "+labelOf(m), m.ID))
		seen[m.ID] = true
	}
	for _, m := range list {
		if !seen[m.ID] {
			opts = append(opts, huh.NewOption("  "+labelOf(m), m.ID))
			seen[m.ID] = true
		}
	}
	return opts
}

// maskKey shows only the end of a key, for "which key is saved" hints.
func maskKey(key string) string {
	if len(key) <= 8 {
		return "••••"
	}
	return "••••" + key[len(key)-4:]
}

// displayPath shortens a path under the home directory to ~/….
func displayPath(p string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		if rel, err := filepath.Rel(home, p); err == nil && !strings.HasPrefix(rel, "..") {
			return "~" + string(filepath.Separator) + rel
		}
	}
	return p
}

// firstLine is the first line of s.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
