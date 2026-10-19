// Package schema holds tiny JSON-schema builders shared by the engine tools and
// every totem, so the {type,properties,required} boilerplate lives in one place.
package schema

// Obj builds an object schema with the given properties and required keys.
func Obj(props map[string]any, required ...string) map[string]any {
	if required == nil {
		required = []string{}
	}
	return map[string]any{"type": "object", "properties": props, "required": required}
}

// Str builds a string property with a description.
func Str(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

// Arr builds an array-of-strings property with a description.
func Arr(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}

// Enum builds a string property constrained to a set of values.
func Enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "description": desc, "enum": values}
}
