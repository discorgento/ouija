package commands

import (
	"context"
	"errors"
	"fmt"
	"maps"

	"charm.land/huh/v2"
	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/providers"
	"github.com/discorgento/ouija/internal/models"
	"github.com/discorgento/ouija/internal/ouijaconfig"
)

// Setup opens the interactive global config wizard. It collects API keys for
// every provider and lets the user pick defaults (provider + model).
// Saved to ~/.config/ouija/config.json.
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

	// Build input fields: one per provider (ollama excluded — no key needed).
	var inputs []*huh.Input
	keys := make(map[string]*string)
	for i, name := range provNames {
		if name == "ollama" {
			continue
		}
		key := ""
		if existing != nil {
			key = existing.APIKeys[name]
		}
		keys[name] = &key

		inp := huh.NewInput().
			Title(provLabels[i]).
			Description("API key (leave blank to skip)").
			EchoMode(huh.EchoModePassword).
			Value(&key)
		inputs = append(inputs, inp)
	}

	// Default provider/model selects.
	defProv := ""
	defModel := ""
	if existing != nil {
		defProv = existing.DefaultProvider()
		defModel = existing.DefaultModel()
	}
	if defProv == "" {
		defProv = provNames[0]
	}

	provOpts := make([]huh.Option[string], len(provNames))
	for i, name := range provNames {
		provOpts[i] = huh.NewOption(provLabels[i], name)
	}

	selProv := huh.NewSelect[string]().
		Title("Default provider").
		Options(provOpts...).
		Value(&defProv)

	selModel := huh.NewSelect[string]().
		Title("Default model").
		OptionsFunc(func() []huh.Option[string] {
			key := ""
			if k, ok := keys[defProv]; ok {
				key = *k
			}
			return modelOptions(ms, defProv, key)
		}, &defProv).
		Value(&defModel)

	// Build form groups.
	fields := make([]huh.Field, len(inputs))
	for i, inp := range inputs {
		fields[i] = inp
	}
	groups := []*huh.Group{
		huh.NewGroup(fields...).Title("API keys"),
		huh.NewGroup(selProv, selModel).Title("Defaults"),
	}

	printBanner()

	err = huh.NewForm(groups...).WithTheme(ouijaTheme{}).Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil
		}
		return fmt.Errorf("setup: %w", err)
	}

	cfg := buildGlobalConfig(existing, keys, defProv, defModel)

	if err := ouijaconfig.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	verb := "saved"
	if existing != nil {
		verb = "updated"
	}
	fmt.Printf("\n✓ Config %s (%s)\n", verb, ouijaconfig.Path())
	fmt.Printf("  Default provider: %s\n", defProv)
	fmt.Printf("  Default model:    %s\n", defModel)
	configured := 0
	for name, key := range cfg.APIKeys {
		if key != "" {
			configured++
			fmt.Printf("  %s key: ✓\n", name)
		}
	}
	if configured == 0 {
		fmt.Println("  (no API keys configured — run `ouija setup` again)")
	}
	return nil
}

// buildGlobalConfig assembles the config the wizard saves. It starts from the
// existing config, so what the wizard does not show survives — the trusted
// projects (`ouija trust`) and keys of providers not in the form — and only
// replaces the keys it showed (a blank one is removed) and the defaults.
func buildGlobalConfig(existing *ouijaconfig.Config, keys map[string]*string, provider, model string) *ouijaconfig.Config {
	cfg := &ouijaconfig.Config{}
	if existing != nil {
		*cfg = *existing
		cfg.APIKeys = maps.Clone(existing.APIKeys)
		cfg.TrustedProjects = maps.Clone(existing.TrustedProjects)
	}
	if cfg.APIKeys == nil {
		cfg.APIKeys = map[string]string{}
	}
	for name, key := range keys {
		if *key != "" {
			cfg.APIKeys[name] = *key
		} else {
			delete(cfg.APIKeys, name)
		}
	}
	cfg.Defaults = &ouijaconfig.Defaults{Provider: provider, Model: model}
	cfg.Provider, cfg.Model = "", "" // legacy top-level defaults, superseded
	return cfg
}

// modelOptions lists provider's models for a picker: the provider's own list
// from its API when there is a key to ask with (or none is needed), else the
// built-in suggestions.
func modelOptions(ms *models.Store, provider, key string) []huh.Option[string] {
	var p engine.Provider
	if key != "" || providers.Keyless(provider) {
		p, _ = providers.Build(provider, key)
	}
	list := ms.List(context.Background(), provider, p)
	opts := make([]huh.Option[string], len(list))
	for i, m := range list {
		label := m.ID
		if m.Label != "" && m.Label != m.ID {
			label = m.Label + "  (" + m.ID + ")"
		}
		opts[i] = huh.NewOption(label, m.ID)
	}
	return opts
}
