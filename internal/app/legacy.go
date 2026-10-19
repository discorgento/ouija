package app

import (
	"encoding/json"
	"os"

	"github.com/discorgento/ouija/engine"
)

// LoadProjectConfig loads a project config file, carrying the layout of
// configs written before per-totem settings over to the current one (see
// migrateLegacyTotemKeys). Saving the result writes the current layout.
func LoadProjectConfig(path string) (*engine.Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return parseProjectConfig(b)
}

// parseProjectConfig is LoadProjectConfig on the file's contents.
func parseProjectConfig(b []byte) (*engine.Config, error) {
	cfg, err := engine.ParseConfig(b)
	if err != nil {
		return nil, err
	}
	migrateLegacyTotemKeys(b, cfg)
	return cfg, nil
}

// migrateLegacyTotemKeys moves the top-level `passes` and `dimensions` keys
// into `totems.task`, and `passes` into `totems.doc` too (both totems read
// it), unless the file already has those sections.
func migrateLegacyTotemKeys(b []byte, cfg *engine.Config) {
	var legacy struct {
		Passes     *int     `json:"passes"`
		Dimensions []string `json:"dimensions"`
	}
	if json.Unmarshal(b, &legacy) != nil || (legacy.Passes == nil && legacy.Dimensions == nil) {
		return
	}
	set := func(totem string, v map[string]any) {
		if _, ok := cfg.Totems[totem]; ok || len(v) == 0 {
			return
		}
		raw, err := json.Marshal(v)
		if err != nil {
			return
		}
		if cfg.Totems == nil {
			cfg.Totems = make(map[string]json.RawMessage)
		}
		cfg.Totems[totem] = raw
	}
	task := map[string]any{}
	doc := map[string]any{}
	if legacy.Passes != nil {
		task["passes"], doc["passes"] = *legacy.Passes, *legacy.Passes
	}
	if legacy.Dimensions != nil {
		task["dimensions"] = legacy.Dimensions
	}
	set("task", task)
	set("doc", doc)
}
