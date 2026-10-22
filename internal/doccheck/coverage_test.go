// Coverage: every config key, CLI command and OUIJA_* variable the code has
// is in the reference pages, so a new one cannot ship undocumented.

package doccheck

import (
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/discorgento/ouija/engine"
	"github.com/discorgento/ouija/internal/ouijaconfig"
	"github.com/discorgento/ouija/registry"

	_ "github.com/discorgento/ouija/totems" // register the totems the CLI lists
)

func read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// jsonKeys lists the JSON keys of a config struct and of the structs it
// holds, as "key" (nested ones by their own name: the reference documents
// each section's keys in its own table).
func jsonKeys(t reflect.Type, seen map[reflect.Type]bool, out map[string]string) {
	for t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Map {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct || seen[t] {
		return
	}
	seen[t] = true
	for f := range t.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" || !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		out[name] = t.Name() + "." + f.Name
		jsonKeys(f.Type, seen, out)
	}
}

// documented reports whether doc names key as code: `key`, "key" or a
// dotted path ending in it (`tools.bash.sandbox`).
func documented(doc, key string) bool {
	return regexp.MustCompile("[`\".]" + regexp.QuoteMeta(key) + "[`\"]").MatchString(doc)
}

// Every key the config files can hold is in the config reference.
func TestEveryConfigKeyIsDocumented(t *testing.T) {
	doc := read(t, "docs/reference/config-schema.md")
	keys := map[string]string{}
	jsonKeys(reflect.TypeFor[engine.Config](), map[reflect.Type]bool{}, keys)
	jsonKeys(reflect.TypeFor[ouijaconfig.Config](), map[reflect.Type]bool{}, keys)
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		if !documented(doc, key) {
			t.Errorf("config key %q (%s) is not in docs/reference/config-schema.md", key, keys[key])
		}
	}
}

// Every command the CLI has, totems included, is in the CLI reference.
func TestEveryCommandIsDocumented(t *testing.T) {
	doc := read(t, "docs/reference/cli.md")
	main := read(t, "cmd/ouija/main.go")
	var cmds []string
	for _, m := range regexp.MustCompile(`(?m)^\t"([a-z-]+)":\s+"usage: ouija `).FindAllStringSubmatch(main, -1) {
		cmds = append(cmds, m[1])
	}
	if len(cmds) < 5 {
		t.Fatalf("found only %v in cmd/ouija/main.go's usage table: has it moved?", cmds)
	}
	for _, tm := range registry.List() {
		cmds = append(cmds, tm.Name())
	}
	for _, c := range cmds {
		if !strings.Contains(doc, "ouija "+c) {
			t.Errorf("command `ouija %s` is not in docs/reference/cli.md", c)
		}
	}
}

// Every OUIJA_* variable the code reads is in the environment reference.
func TestEveryEnvVarIsDocumented(t *testing.T) {
	doc := read(t, "docs/reference/environment.md")
	re := regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\("(OUIJA_[A-Z_]+)"\)`)
	found := map[string]string{}
	err := filepath.WalkDir(repoRoot(t), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() && (d.Name() == ".git" || d.Name() == "node_modules") {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return err
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			found[m[1]] = p
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(found) == 0 {
		t.Fatal("found no OUIJA_* variables in the code: has the lookup changed?")
	}
	for v, file := range found {
		if !strings.Contains(doc, v) {
			t.Errorf("%s (read in %s) is not in docs/reference/environment.md", v, file)
		}
	}
}
