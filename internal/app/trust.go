package app

// Project trust.
//
// A project config found by walking up from the working directory may come
// from a repository someone else wrote (a clone), so the keys that widen what
// the model may do or where data goes — permission levels, the bash allowlist,
// an apiKey override and the systemPrompt — are not applied until the user
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
	"strings"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/ouijaconfig"
)

// sensitiveKeys are the project-config keys applied only from a trusted file.
var sensitiveKeys = []string{"permission", "tools", "apiKey", "systemPrompt"}

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
	global.TrustedProjects[trustKey(path)] = digest(b)
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

// SensitiveKeys lists the sensitive keys the config file at path sets.
func SensitiveKeys(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return sensitiveKeysIn(b)
}

// sensitiveKeysIn lists the sensitive keys a config file's contents set.
func sensitiveKeysIn(b []byte) ([]string, error) {
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
	return out, nil
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
func stripUntrusted(c *engine.Config) {
	c.Permission = nil
	c.Tools = nil
	c.APIKey = ""
	c.SystemPrompt = engine.DefaultConfig().SystemPrompt
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
