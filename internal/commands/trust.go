package commands

import (
	"encoding/json"
	"flag"
	"fmt"
	"strings"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/termsafe"
)

// Trust implements `ouija trust [--revoke] [path]`: it trusts the project
// config (the one discovery finds from the current directory, or path), so
// its permission, bash allowlist, apiKey and systemPrompt keys apply. It shows
// what those keys grant before recording the trust; any later change to the
// file lapses it.
func Trust(args []string) error {
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	revoke := fs.Bool("revoke", false, "forget the trust recorded for the project config")
	if err := fs.Parse(args); err != nil {
		return app.ErrUsage // flag already printed why
	}
	// flag stops at the first non-flag: `trust <path> --revoke` must revoke,
	// not silently trust, so parse what follows the path too.
	path := fs.Arg(0)
	if fs.NArg() > 1 {
		if err := fs.Parse(fs.Args()[1:]); err != nil {
			return app.ErrUsage
		}
		if fs.NArg() > 0 {
			return fmt.Errorf("unexpected arguments: %v (usage: ouija trust [--revoke] [path])", fs.Args())
		}
	}
	if path == "" {
		found, err := app.FindProjectConfig()
		if err != nil {
			return err
		}
		if found == "" {
			return fmt.Errorf("no project config found from here (.ouija/config.json, .ouija.json, ouija.json)")
		}
		path = found
	}

	if *revoke {
		if err := app.Untrust(path); err != nil {
			return err
		}
		fmt.Printf("✓ No longer trusted: %s\n", path)
		return nil
	}

	cfg, err := engine.LoadConfig(path)
	if err != nil {
		return fmt.Errorf("load %s: %w", path, err)
	}
	keys, err := app.SensitiveKeys(path)
	if err != nil {
		return err
	}
	fmt.Printf("Project config: %s\n", path)
	if len(keys) == 0 {
		fmt.Println("  It sets no security-sensitive keys.")
	}
	for _, k := range keys {
		// The values come from the file under review: an escape sequence in
		// one could erase the lines above it and hide what is granted.
		fmt.Printf("  %-13s %s\n", k, termsafe.Clean(describeSensitive(k, cfg)))
	}
	if err := app.Trust(path); err != nil {
		return err
	}
	fmt.Println("✓ Trusted. Any change to the file needs `ouija trust` again.")
	return nil
}

// describeSensitive summarizes what a sensitive key grants, never printing a
// secret.
func describeSensitive(key string, cfg *engine.Config) string {
	switch key {
	case "permission":
		if cfg.Permission == nil {
			return ""
		}
		b, _ := json.Marshal(cfg.Permission)
		return string(b)
	case "tools":
		if cfg.Tools != nil && cfg.Tools.Bash != nil && len(cfg.Tools.Bash.Allowlist) > 0 {
			return "bash allowlist: " + strings.Join(cfg.Tools.Bash.Allowlist, ", ")
		}
		return "(no bash allowlist)"
	case "apiKey":
		return "overrides your API key for provider " + cfg.Provider
	case "projectRoot":
		return fmt.Sprintf("moves the sandbox root to %q (it must stay inside the project)", cfg.ProjectRoot)
	case "systemPrompt":
		p := strings.Join(strings.Fields(cfg.SystemPrompt), " ")
		if len(p) > 70 {
			p = strings.ToValidUTF8(p[:70], "") + "…"
		}
		return fmt.Sprintf("%q", p)
	}
	return ""
}
