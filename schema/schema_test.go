package schema

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestObj(t *testing.T) {
	props := map[string]any{"name": Str("the name")}

	got := Obj(props, "name")
	if got["type"] != "object" {
		t.Errorf("type = %v, want object", got["type"])
	}
	if !reflect.DeepEqual(got["properties"], props) {
		t.Errorf("properties = %#v, want %#v", got["properties"], props)
	}
	if !reflect.DeepEqual(got["required"], []string{"name"}) {
		t.Errorf("required = %#v, want [name]", got["required"])
	}
}

// With no required keys the schema must still carry "required": [] — a missing
// or null field makes strict providers reject the tool schema.
func TestObj_EmptyRequiredIsJSONArray(t *testing.T) {
	got := Obj(map[string]any{"name": Str("x")})

	req, ok := got["required"].([]string)
	if !ok {
		t.Fatalf("required = %T, want []string", got["required"])
	}
	if len(req) != 0 {
		t.Fatalf("required = %v, want empty", req)
	}

	b, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), `"required":[]`) {
		t.Errorf("marshaled schema = %s, want an empty required array", b)
	}
}

func TestStr(t *testing.T) {
	got := Str("a description")
	if got["type"] != "string" || got["description"] != "a description" {
		t.Errorf("Str() = %#v", got)
	}
}

func TestArr(t *testing.T) {
	got := Arr("a description")
	if got["type"] != "array" || got["description"] != "a description" {
		t.Errorf("Arr() = %#v", got)
	}
	items, ok := got["items"].(map[string]any)
	if !ok || items["type"] != "string" {
		t.Errorf("Arr() items = %#v, want an array of strings", got["items"])
	}
}

func TestEnum(t *testing.T) {
	got := Enum("pick one", "a", "b")
	if got["type"] != "string" {
		t.Errorf("type = %v, want string", got["type"])
	}
	if !reflect.DeepEqual(got["enum"], []string{"a", "b"}) {
		t.Errorf("enum = %#v, want [a b]", got["enum"])
	}
}
