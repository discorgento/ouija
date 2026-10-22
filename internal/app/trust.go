package app

// Project trust.
//
// A project config found by walking up from the working directory may come
// from a repository someone else wrote (a clone), so the keys that widen what
// the model may do or where data goes — permission levels, the bash allowlist,
// an apiKey override, the systemPrompt, model suggestions — are not applied until the user
// trusts that exact file with `ouija trust`. Trust is the file's SHA-256,
// recorded in the global config (a protected path the model cannot write); any
// change to the file lapses it. An explicit --config is trusted, like any CLI
// flag, and so is a file ouija itself wrote (`ouija init`, chat saves).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/models"
	"github.com/discorgento/ouija/internal/ouijaconfig"
)

// sensitiveKeys are the project-config keys applied only from a trusted file.
// projectRoot is one: it moves the sandbox, and through a symlink in the repo
// (src -> ~) it moved it onto the home directory without any prompt. rules is
// one too: it goes into the system message, like systemPrompt.
// budget is one when it raises a ceiling above the default: untrusted, it is
// clamped, and trusting the file lifts the clamp. modelSuggestions is one: a
// model ID the picker offers is one a click sends the code and key to.
var sensitiveKeys = []string{"permission", "tools", "apiKey", "systemPrompt", "rules", "budget", "projectRoot", "modelSuggestions"}

// Limits on what an untrusted file may set for the keys that are not plain
// grants but still cost money or time: it may lower them, never raise them
// past these. Trusting the file lifts them.
const (
	// untrustedMaxPasses is the totems' own default (task and doc both run 3
	// passes): every pass is another full set of model calls.
	untrustedMaxPasses = 3
	// untrustedMaxStreamTimeout is the longest idle wait, in seconds: a huge
	// one held a run on a silent provider indefinitely.
	untrustedMaxStreamTimeout = 600
	// untrustedMaxContextWindow caps contextWindow when the catalog does not
	// know the model; when it does, the catalog's size is the cap. A larger
	// window sends more of the conversation, and pays for it, on every call.
	untrustedMaxContextWindow = 1_000_000
)

// FindProjectConfig returns the project config path discovery would load from
// the current directory, "" when there is none.
func FindProjectConfig() (string, error) {
	_, path, _, err := findConfigFile("")
	return path, err
}

// IsTrusted reports whether the config file at path was trusted by the user
// and is unchanged since.
func IsTrusted(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	global, _ := ouijaconfig.Load()
	return trusted(global, path, b)
}

// trusted reports whether data, the contents of the file at path, is what
// the user trusted.
func trusted(global *ouijaconfig.Config, path string, data []byte) bool {
	if global == nil {
		return false
	}
	want, ok := global.TrustedProjects[trustKey(path)]
	return ok && want == digest(data)
}

// Trust records the current contents of the config file at path as trusted.
func Trust(path string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return TrustContents(path, b)
}

// TrustContents records data, the contents of the config file at path the
// user reviewed, as trusted: a file changed after the review stays untrusted.
func TrustContents(path string, data []byte) error {
	global, err := ouijaconfig.Load()
	if err != nil {
		return err
	}
	if global == nil {
		global = &ouijaconfig.Config{}
	}
	if global.TrustedProjects == nil {
		global.TrustedProjects = make(map[string]string)
	}
	global.TrustedProjects[trustKey(path)] = digest(data)
	return ouijaconfig.Save(global)
}

// Untrust forgets the trust recorded for path. It is not an error when path
// was never trusted.
func Untrust(path string) error {
	global, err := ouijaconfig.Load()
	if err != nil || global == nil {
		return err
	}
	key := trustKey(path)
	if _, ok := global.TrustedProjects[key]; !ok {
		return nil
	}
	delete(global.TrustedProjects, key)
	return ouijaconfig.Save(global)
}

// Review is a project config as `ouija trust` shows it before trusting it.
type Review struct {
	Data   []byte         // the file's contents, what TrustContents records
	Config *engine.Config // the parsed file, legacy keys carried over
	Keys   []string       // the sensitive keys it sets (SensitiveKeys)
}

// ReviewProjectConfig reads the config file at path once, for review.
func ReviewProjectConfig(path string) (*Review, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	cfg, err := parseProjectConfig(b)
	if err != nil {
		return nil, fmt.Errorf("load %s: %w", path, err)
	}
	global, _ := ouijaconfig.Load()
	keys, err := sensitiveKeysIn(b, global)
	if err != nil {
		return nil, err
	}
	return &Review{Data: b, Config: cfg, Keys: keys}, nil
}

// SensitiveKeys lists the sensitive keys the config file at path sets.
func SensitiveKeys(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	global, _ := ouijaconfig.Load()
	return sensitiveKeysIn(b, global)
}

// sensitiveKeysIn lists the sensitive keys a config file's contents set.
// provider and model count when they differ from the user's own defaults
// (global): they decide which company gets the code and the key, and what
// every token costs.
func sensitiveKeysIn(b []byte, global *ouijaconfig.Config) ([]string, error) {
	if err := refuseDuplicateKeys(b); err != nil {
		return nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	var out []string
	for _, k := range sensitiveKeys {
		if setsKey(raw, k) {
			out = append(out, k)
		}
	}
	gp, gm := userDefaults(global)
	if c, err := parseProjectConfig(b); err == nil {
		out = append(out, limitedKeysIn(c, gp, gm)...)
	}
	if p := stringKey(raw, "provider"); p != "" && p != gp {
		out = append(out, "provider")
	}
	if m := stringKey(raw, "model"); m != "" && m != gm {
		out = append(out, "model")
	}
	return out, nil
}

// limitedKeysIn lists the keys of c an untrusted file cannot apply as set:
// totem settings that reach the prompts or multiply the calls, a longer
// stream timeout, a larger context window and negative pricing. c is parsed,
// so keys in any case and the legacy top-level passes/dimensions count.
// provider and model are the ones an untrusted file runs with.
func limitedKeysIn(c *engine.Config, provider, model string) []string {
	var out []string
	if totemsGrant(c.Totems) {
		out = append(out, "totems")
	}
	if c.StreamTimeout > untrustedMaxStreamTimeout {
		out = append(out, "streamTimeout")
	}
	if c.ContextWindow > contextWindowCap(provider, model) {
		out = append(out, "contextWindow")
	}
	if c.CostPer1MInput < 0 || c.CostPer1MOutput < 0 {
		out = append(out, "costPer1M")
	}
	return out
}

// totemsGrant reports whether a totem section sets dimensions (they go into
// the analysis prompts, like rules) or more passes than the default.
func totemsGrant(totems map[string]json.RawMessage) bool {
	for _, raw := range totems {
		var sec map[string]json.RawMessage
		if json.Unmarshal(raw, &sec) != nil {
			continue
		}
		for k, v := range sec {
			switch {
			case strings.EqualFold(k, "dimensions"):
				var d []string
				if json.Unmarshal(v, &d) != nil || len(d) > 0 {
					return true
				}
			case strings.EqualFold(k, "passes"):
				var n float64
				if json.Unmarshal(v, &n) == nil && n > untrustedMaxPasses {
					return true
				}
			}
		}
	}
	return false
}

// untrustedTotems is totems without what an untrusted file may not set: each
// section loses its dimensions (the totem's defaults apply) and keeps at most
// the default passes. The cosmetic keys (a document's client, author, brand)
// stay; the doc totem escapes them where it renders them.
func untrustedTotems(totems map[string]json.RawMessage) map[string]json.RawMessage {
	if totems == nil {
		return nil
	}
	out := make(map[string]json.RawMessage, len(totems))
	for name, raw := range totems {
		var sec map[string]json.RawMessage
		if json.Unmarshal(raw, &sec) != nil {
			out[name] = raw // not an object: the totem rejects it when it reads it
			continue
		}
		for k, v := range sec {
			switch {
			case strings.EqualFold(k, "dimensions"):
				delete(sec, k)
			case strings.EqualFold(k, "passes"):
				var n float64
				if json.Unmarshal(v, &n) == nil && n > untrustedMaxPasses {
					sec[k] = json.RawMessage(strconv.Itoa(untrustedMaxPasses))
				}
			}
		}
		b, err := json.Marshal(sec)
		if err != nil {
			continue
		}
		out[name] = b
	}
	return out
}

// contextWindowCap is the largest contextWindow an untrusted file may set
// for provider/model: the catalog's size when it knows the model, else
// untrustedMaxContextWindow.
func contextWindowCap(provider, model string) int {
	if info, ok := models.Load().ModelInfo(provider, model); ok && info.ContextWindow > 0 {
		return info.ContextWindow
	}
	return untrustedMaxContextWindow
}

// stringKey is raw's string value for k, matched without regard to case as
// encoding/json does; "" when absent or not a string.
func stringKey(raw map[string]json.RawMessage, k string) string {
	for name, v := range raw {
		var s string
		if strings.EqualFold(name, k) && json.Unmarshal(v, &s) == nil {
			return s
		}
	}
	return ""
}

// userDefaults is the provider and model the user chose in `ouija setup`.
func userDefaults(global *ouijaconfig.Config) (provider, model string) {
	if global == nil {
		return "", ""
	}
	return global.DefaultProvider(), global.DefaultModel()
}

// setsKey reports whether raw sets the sensitive key k. Keys match without
// regard to case, as encoding/json does when it fills the config: "Permission"
// or "APIKEY" apply exactly like "permission" and "apiKey".
func setsKey(raw map[string]json.RawMessage, k string) bool {
	for name, v := range raw {
		if !strings.EqualFold(name, k) || string(v) == "null" || string(v) == `""` {
			continue
		}
		// `ouija init` writes the built-in prompt; it grants nothing.
		if k == "projectRoot" { // `ouija init` writes "."; it moves nothing
			var r string
			if json.Unmarshal(v, &r) == nil && (r == "." || r == "./") {
				continue
			}
		}
		if k == "budget" { // only a budget above the defaults grants anything
			var b engine.Budget
			def := engine.DefaultConfig().Budget
			if json.Unmarshal(v, &b) == nil && b.MaxTokens <= def.MaxTokens && b.MaxSteps <= def.MaxSteps {
				continue
			}
		}
		if k == "rules" { // `ouija init` writes the built-in rules
			var r []string
			if json.Unmarshal(v, &r) == nil && (len(r) == 0 || slices.Equal(r, engine.DefaultConfig().Rules)) {
				continue
			}
		}
		if k == "modelSuggestions" {
			var m map[string][]string
			if json.Unmarshal(v, &m) == nil && len(m) == 0 {
				continue
			}
		}
		if k == "systemPrompt" {
			var p string
			if json.Unmarshal(v, &p) == nil && p == engine.DefaultConfig().SystemPrompt {
				continue
			}
		}
		return true
	}
	return false
}

// CheckWritable reports why ouija must not rewrite the project config at
// path: an existing untrusted file that sets sensitive keys. Rewriting it
// would either drop them or, by trusting the result, adopt them.
func CheckWritable(path string) error {
	if g := ouijaconfig.Path(); g != "" && trustKey(path) == trustKey(g) {
		return fmt.Errorf("%s is the global config, not a project config: ouija does not rewrite it as one", path)
	}
	if _, err := os.Stat(path); err == nil && !IsTrusted(path) {
		if keys, _ := SensitiveKeys(path); len(keys) > 0 {
			return fmt.Errorf("%s is not trusted (sets %s) — review it with `ouija trust` before ouija rewrites it",
				path, strings.Join(keys, ", "))
		}
	}
	return nil
}

// SaveProjectConfig writes the project config at path through save (e.g.
// Engine.SaveConfigAt), then trusts the result: ouija writing its own changes
// must not lapse the trust the user gave. It refuses an existing untrusted file
// that sets sensitive keys: rewriting it would either drop them or, by trusting
// the result, adopt them.
func SaveProjectConfig(path string, save func(string) error) error {
	if err := CheckWritable(path); err != nil {
		return err
	}
	if err := save(path); err != nil {
		return err
	}
	return Trust(path)
}

// stripUntrusted clears the sensitive keys of a project config that was loaded
// from an untrusted file.
// An untrusted file may also lower the budget, never raise it: a clone that
// set maxTokens to 1e12 spent the user's API key without a ceiling. And it
// keeps the user's provider and model: switching them would send the code and
// the user's key to another company, or to a model at many times the price.
// The keys that only cost money or time are clamped the same way: totem
// passes, streamTimeout and contextWindow may only go down, and pricing may
// only go up (a higher price trips budget.maxUSD sooner; a negative one kept
// the spend at zero so the ceiling never tripped). budget.maxUSD needs no
// clamp: without it there is no dollar ceiling at all, so any value lowers it.
// Totem dimensions go into the analysis prompts, like rules, and are dropped,
// and so are model suggestions: the picker would offer a model the file chose.
func stripUntrusted(c *engine.Config, global *ouijaconfig.Config) {
	gp, gm := userDefaults(global)
	if c.Provider != "" && c.Provider != gp || c.Model != "" && c.Model != gm {
		c.Provider, c.Model = "", "" // the global defaults apply
	}
	def := engine.DefaultConfig()
	c.Permission = nil
	c.Tools = nil
	c.APIKey = ""
	c.SystemPrompt = def.SystemPrompt
	c.Rules = def.Rules
	c.ModelSuggestions = nil
	c.ProjectRoot = "."
	c.Budget.MaxTokens = min(c.Budget.MaxTokens, def.Budget.MaxTokens)
	c.Budget.MaxSteps = min(c.Budget.MaxSteps, def.Budget.MaxSteps)
	c.Totems = untrustedTotems(c.Totems)
	c.StreamTimeout = min(c.StreamTimeout, untrustedMaxStreamTimeout)
	c.ContextWindow = min(c.ContextWindow, contextWindowCap(gp, gm))
	c.CostPer1MInput = max(c.CostPer1MInput, 0)
	c.CostPer1MOutput = max(c.CostPer1MOutput, 0)
}

func digest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// trustKey is the canonical path trust is recorded under: absolute, with
// symlinks resolved, so the same file is one entry however it is reached.
func trustKey(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if real, err := filepath.EvalSymlinks(path); err == nil {
		return real
	}
	return path
}
