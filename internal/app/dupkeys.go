package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// refuseDuplicateKeys rejects a config that sets a key twice in one object,
// in any case ("provider" and "PROVIDER" are one key to encoding/json). The
// trust review reads a file as a map, which keeps a key's last copy, while
// the parse into the config keeps an earlier one where a later copy is null
// or {}: `"apiKey":"sk-…","apiKey":null` showed nothing to review and still
// set the key once trusted. One copy per key makes both reads agree.
func refuseDuplicateKeys(b []byte) error {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var walk func(path string) error
	walk = func(path string) error {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		switch tok {
		case json.Delim('{'):
			seen := map[string]bool{}
			for dec.More() {
				t, err := dec.Token()
				if err != nil {
					return err
				}
				key, _ := t.(string)
				folded := foldKey(key)
				if seen[folded] {
					return fmt.Errorf("the key %q is set twice: ouija refuses a config with duplicate keys (they could hide a setting from `ouija trust`)", path+key)
				}
				seen[folded] = true
				if err := walk(path + key + "."); err != nil {
					return err
				}
			}
			_, err = dec.Token() // '}'
			return err
		case json.Delim('['):
			for i := 0; dec.More(); i++ {
				if err := walk(fmt.Sprintf("%s%d.", path, i)); err != nil {
					return err
				}
			}
			_, err = dec.Token() // ']'
			return err
		}
		return nil
	}
	return walk("")
}

// foldKey is key with every rune replaced by the smallest one it folds to,
// so keys encoding/json would match against one field compare equal (the
// Kelvin sign is K, ſ is s).
func foldKey(key string) string {
	return strings.Map(func(r rune) rune {
		lowest := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			lowest = min(lowest, f)
		}
		return lowest
	}, key)
}
