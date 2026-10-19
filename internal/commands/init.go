package commands

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"charm.land/huh/v2"
	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/engine/providers"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/models"
	"github.com/discorgento/ouija/internal/ouijaconfig"
)

// Init opens the interactive project config wizard. It reads defaults from the
// global config (~/.config/ouija/config.json) and lets you pick provider/model
// + optionally override the API key for this project.
// Saved to .ouija/config.json in the current directory.
func Init(args []string) error {
	// Load global config to seed defaults and check existing keys.
	global, err := ouijaconfig.Load()
	if err != nil {
		return err
	}
	if global == nil {
		return fmt.Errorf("no global config found — run `ouija setup` first")
	}

	ms := models.Load()
	provNames, provLabels := ms.ProvidersFor(providers.Names())

	provOpts := make([]huh.Option[string], 0, len(provNames))
	for i, name := range provNames {
		provOpts = append(provOpts, huh.NewOption(provLabels[i], name))
	}

	// Seed from existing project config if it exists, otherwise from global.
	dir, _ := os.Getwd()
	path := filepath.Join(dir, ".ouija", "config.json")
	// Refuse before the form, not after it: a cloned repo's untrusted config
	// with permissions or an allowlist must not be adopted by rewriting it.
	if err := app.CheckWritable(path); err != nil {
		return err
	}
	cfg, existing, err := seedProjectConfig(path, global)
	if err != nil {
		return err
	}

	var (
		provider = cfg.Provider
		model    = cfg.Model
		apiKey   = cfg.APIKey
	)
	if apiKey == "" && global.HasKey(provider) {
		// Pre-fill with global key so user can keep it.
		apiKey = global.APIKeys[provider]
	}

	selProv := huh.NewSelect[string]().
		Title("Provider").
		Options(provOpts...).
		Value(&provider)

	selModel := huh.NewSelect[string]().
		Title("Model").
		OptionsFunc(func() []huh.Option[string] {
			key := apiKey
			if key == "" {
				key = global.APIKeys[provider]
			}
			return modelOptions(ms, provider, key)
		}, &provider).
		Value(&model)

	keyDesc := "Leave blank to use global key"
	if provider == "ollama" {
		keyDesc = "Not required for Ollama"
	}
	inpKey := huh.NewInput().
		Title("API key (project override)").
		Description(keyDesc).
		EchoMode(huh.EchoModePassword).
		Value(&apiKey)

	printBanner()

	err = huh.NewForm(
		huh.NewGroup(selProv, selModel, inpKey).Title("Project config"),
	).WithTheme(ouijaTheme{}).Run()
	if err != nil {
		if errors.Is(err, huh.ErrUserAborted) {
			return nil
		}
		return fmt.Errorf("init: %w", err)
	}

	cfg.Provider = provider
	cfg.Model = model
	// Only store the API key if it differs from the global one (or if there
	// is no global key for this provider).
	cfg.APIKey = initAPIKey(apiKey, global.APIKeys[provider])

	// The user just wrote this file: SaveProjectConfig trusts it, so its
	// settings apply.
	if err := app.SaveProjectConfig(path, cfg.Save); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	verb := "saved"
	if existing {
		verb = "updated"
	}
	fmt.Printf("\n✓ Project config %s (%s)\n", verb, path)
	fmt.Printf("  Provider: %s\n", provider)
	fmt.Printf("  Model:    %s\n", model)
	if cfg.APIKey != "" {
		fmt.Println("  API key:   (project override)")
		fmt.Println("  ⚠ the key is in " + path + " — keep that file out of git (.gitignore)")
	} else {
		fmt.Println("  API key:   (using global)")
	}
	return nil
}

// seedProjectConfig returns the project config to edit and whether it already
// existed: the file at path, else (only when there is none) engine defaults
// seeded with the global provider/model so the wizard opens on the user's
// usual choice. A file that does not load is an error: seeding defaults would
// save over the user's rules, totems and permissions.
func seedProjectConfig(path string, global *ouijaconfig.Config) (*engine.Config, bool, error) {
	cfg, err := app.LoadProjectConfig(path)
	switch {
	case err == nil:
		return cfg, true, nil
	case !errors.Is(err, fs.ErrNotExist):
		return nil, false, fmt.Errorf("%s: %w — fix or remove it, then run `ouija init` again", path, err)
	}
	cfg = engine.DefaultConfig()
	cfg.Provider = global.DefaultProvider()
	cfg.Model = global.DefaultModel()
	return cfg, false, nil
}

// initAPIKey decides the project-level API-key override: it is stored only when
// non-empty and different from the global key, so the project config does not
// duplicate the global secret.
func initAPIKey(chosen, globalKey string) string {
	if chosen != "" && chosen != globalKey {
		return chosen
	}
	return ""
}
