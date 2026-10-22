package commands

import (
	"bufio"
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/x/term"
	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/app"
	"github.com/discorgento/ouija/internal/ouijaconfig"
	"github.com/discorgento/ouija/internal/termsafe"
)

// trustIn and trustInteractive are the confirmation's input and whether it
// comes from a person at a terminal; tests replace them.
var (
	trustIn          io.Reader = os.Stdin
	trustInteractive           = func() bool { return term.IsTerminal(os.Stdin.Fd()) && term.IsTerminal(os.Stdout.Fd()) }
)

// errTrustUnconfirmed is returned when nobody can confirm the trust: no
// terminal and no --yes.
var errTrustUnconfirmed = errors.New("not trusted: confirming needs a terminal — review the keys above and run `ouija trust --yes` to trust them")

// Trust implements `ouija trust [--revoke] [--yes] [path]`: it trusts the
// project config (the one discovery finds from the current directory, or
// path), so its sensitive keys apply. It shows in full what those keys grant,
// then asks before recording the trust (--yes skips the question); any later
// change to the file lapses it. What is trusted is the exact contents shown.
func Trust(args []string) error {
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	revoke := fs.Bool("revoke", false, "forget the trust recorded for the project config")
	var yes bool
	fs.BoolVar(&yes, "yes", false, "trust without asking (for scripts; review the file first)")
	fs.BoolVar(&yes, "y", false, "shorthand for --yes")
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
			return fmt.Errorf("unexpected arguments: %v (usage: ouija trust [--revoke] [--yes] [path])", fs.Args())
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
		fmt.Printf("✓ No longer trusted: %s\n", termsafe.Clean(path))
		return nil
	}

	rev, err := app.ReviewProjectConfig(path)
	if err != nil {
		return err
	}
	fmt.Printf("Project config: %s\n", termsafe.Clean(path))
	if len(rev.Keys) == 0 {
		fmt.Println("  It sets no security-sensitive keys.")
	}
	hidden := false
	for _, k := range rev.Keys {
		// The values come from the file under review: an escape sequence in
		// one could erase the lines above it and hide what is granted, and
		// an invisible character (a Unicode tag spells ASCII only a model
		// sees) could hide an instruction. Reveal shows both as \u{…}.
		raw := describeSensitive(k, rev.Config)
		hidden = hidden || termsafe.Hidden(raw)
		desc := termsafe.Reveal(raw)
		fmt.Printf("  %-13s %s\n", k, strings.ReplaceAll(desc, "\n", "\n                "))
	}
	if hidden {
		fmt.Println("  ⚠ It holds invisible or control characters, shown above as \\u{…}: a model reads text there that a terminal does not show.")
	}
	if len(rev.Keys) > 0 && !yes {
		if !trustInteractive() {
			return errTrustUnconfirmed
		}
		fmt.Print("Trust this config? [y/N] ")
		line, _ := bufio.NewReader(trustIn).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Println("Not trusted; ouija keeps ignoring those keys.")
			return nil
		}
	}
	// The bytes shown, not the file as it is now: a change made while the
	// question was open stays untrusted.
	if err := app.TrustContents(path, rev.Data); err != nil {
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
		if cfg.Tools == nil || cfg.Tools.Bash == nil {
			return "(no bash settings)"
		}
		var parts []string
		if a := cfg.Tools.Bash.Allowlist; len(a) > 0 {
			parts = append(parts, "bash allowlist: "+strings.Join(a, ", "))
		}
		if sb := cfg.Tools.Bash.Sandbox; sb != nil && sb.Enabled {
			parts = append(parts, describeSandbox(sb))
		} else if sb != nil {
			parts = append(parts, "turns the bash sandbox off (a global sandbox still applies)")
		}
		if len(parts) == 0 {
			return "(no bash allowlist)"
		}
		return strings.Join(parts, "; ")
	case "provider", "model":
		gp, gm := "", ""
		if g, _ := ouijaconfig.Load(); g != nil {
			gp, gm = g.DefaultProvider(), g.DefaultModel()
		}
		if key == "provider" {
			return fmt.Sprintf("sends your code (and the key you saved for it) to %s instead of your default %s", cfg.Provider, cmp.Or(gp, "(none)"))
		}
		return fmt.Sprintf("uses model %s instead of your default %s — check what it costs", cfg.Model, cmp.Or(gm, "(none)"))
	case "budget":
		return fmt.Sprintf("raises the budget above the defaults: maxTokens %d, maxSteps %d", cfg.Budget.MaxTokens, cfg.Budget.MaxSteps)
	case "rules":
		// Not %q: it would hide an invisible character as an escape Trust
		// cannot tell apart; Trust reveals them itself.
		return `adds to the system message: "` + strings.Join(cfg.Rules, " | ") + `"`
	case "apiKey":
		return "overrides your API key for provider " + cfg.Provider
	case "projectRoot":
		return fmt.Sprintf("moves the sandbox root to %q (it must stay inside the project)", cfg.ProjectRoot)
	case "systemPrompt":
		// In full: the end of a long prompt is where an instruction hides.
		return "replaces the system message with:\n" + cfg.SystemPrompt
	case "totems":
		return describeTotems(cfg.Totems)
	case "modelSuggestions":
		return describeSuggestions(cfg.ModelSuggestions)
	case "streamTimeout":
		return fmt.Sprintf("waits up to %ds for a silent provider", cfg.StreamTimeout)
	case "contextWindow":
		return fmt.Sprintf("sends up to %d tokens of context per call — check what it costs", cfg.ContextWindow)
	case "costPer1M":
		return fmt.Sprintf("prices tokens at $%g in / $%g out per 1M: below zero, budget.maxUSD never trips", cfg.CostPer1MInput, cfg.CostPer1MOutput)
	}
	return ""
}

// describeSuggestions lists the models the /model picker would offer first,
// per provider.
func describeSuggestions(sug map[string][]string) string {
	providers := make([]string, 0, len(sug))
	for p := range sug {
		providers = append(providers, p)
	}
	sort.Strings(providers)
	parts := make([]string, 0, len(providers))
	for _, p := range providers {
		parts = append(parts, p+": "+strings.Join(sug[p], ", "))
	}
	return "offers these models first in /model: " + strings.Join(parts, "; ")
}

// describeTotems lists, per totem, the settings an untrusted file cannot
// apply: dimensions in full (they go into the prompts) and the passes.
func describeTotems(totems map[string]json.RawMessage) string {
	names := make([]string, 0, len(totems))
	for name := range totems {
		names = append(names, name)
	}
	sort.Strings(names)
	var parts []string
	for _, name := range names {
		var sec struct {
			Passes     int      `json:"passes"`
			Dimensions []string `json:"dimensions"`
		}
		if json.Unmarshal(totems[name], &sec) != nil {
			continue
		}
		var p []string
		if sec.Passes > 0 {
			p = append(p, fmt.Sprintf("%d passes", sec.Passes))
		}
		if len(sec.Dimensions) > 0 {
			p = append(p, `dimensions added to the prompts: "`+strings.Join(sec.Dimensions, " | ")+`"`)
		}
		if len(p) > 0 {
			parts = append(parts, name+": "+strings.Join(p, ", "))
		}
	}
	return strings.Join(parts, "; ")
}

// describeSandbox says what an enabled project sandbox adds: the paths bash
// may read and write beyond the project, and the network it may reach.
func describeSandbox(sb *engine.SandboxConfig) string {
	out := "bash sandbox"
	if len(sb.Read) > 0 {
		out += ", also reads " + strings.Join(sb.Read, ", ")
	}
	if len(sb.Write) > 0 {
		out += ", also writes " + strings.Join(sb.Write, ", ")
	}
	switch n := sb.Network; {
	case n == nil:
	case n.All:
		out += ", ANY network"
	case len(n.Connect)+len(n.Bind) > 0:
		out += fmt.Sprintf(", TCP connect %v bind %v", n.Connect, n.Bind)
	}
	return out
}
