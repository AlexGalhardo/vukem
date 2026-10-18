package z

import (
	"maps"
	"reflect"
	"slices"
)

// JSONSchema returns the JSON Schema (2020-12) of s: types, required keys,
// lengths, ranges, formats, patterns, enums, defaults and descriptions.
// Refine, Transform and Custom logic cannot be expressed and is omitted,
// so the server may reject values the schema allows, never the reverse.
func JSONSchema(s AnySchema) map[string]any {
	if js, ok := s.(interface{ jsonSchema() map[string]any }); ok {
		return js.jsonSchema()
	}

	return map[string]any{}
}

func (o *opts[T]) setKW(k string, v any) {
	if o.kw == nil {
		o.kw = map[string]any{}
	}

	o.kw[k] = v
}

// decorate adds the common keywords to a schema with base type typ.
func (o *opts[T]) decorate(m map[string]any, typ string) map[string]any {
	maps.Copy(m, o.kw)

	if typ != "" {
		m["type"] = typ
		if o.nullable {
			m["type"] = []any{typ, "null"}
		}
	}

	if o.desc != "" {
		m["description"] = o.desc
	}

	if o.hasDef {
		m["default"] = o.def
	}

	return m
}

func (s StringSchema) jsonSchema() map[string]any { return s.o.decorate(map[string]any{}, "string") }

func (s NumberSchema[N]) jsonSchema() map[string]any {
	return s.o.decorate(map[string]any{}, numberType(s.GoType()))
}

func numberType(t reflect.Type) string {
	if k := t.Kind(); k == reflect.Float32 || k == reflect.Float64 {
		return "number"
	}

	return "integer"
}

func (s BoolSchema) jsonSchema() map[string]any { return s.o.decorate(map[string]any{}, "boolean") }

func (s TimeSchema) jsonSchema() map[string]any {
	m := map[string]any{}
	if slices.Equal(s.layouts, defaultLayouts) {
		m["format"] = "date-time"
	}

	return s.o.decorate(m, "string")
}

func (s EnumSchema[T]) jsonSchema() map[string]any {
	enum := make([]any, 0, len(s.values)+1)
	for _, v := range s.values {
		enum = append(enum, string(v))
	}

	if s.o.nullable {
		enum = append(enum, nil)
	}

	m := map[string]any{"enum": enum}
	nullable := s.o.nullable
	s.o.nullable = false // expressed by the null enum member

	m = s.o.decorate(m, "string")
	if nullable {
		m["type"] = []any{"string", "null"}
	}

	return m
}

func (s SliceSchema) jsonSchema() map[string]any {
	m := map[string]any{"items": JSONSchema(s.elem)}

	if s.min >= 0 {
		m["minItems"] = s.min
	}

	if s.max >= 0 {
		m["maxItems"] = s.max
	}

	if s.length >= 0 {
		m["minItems"], m["maxItems"] = s.length, s.length
	}

	if s.unique {
		m["uniqueItems"] = true
	}

	return s.o.decorate(m, "array")
}

func (s MapSchema) jsonSchema() map[string]any {
	m := map[string]any{"additionalProperties": JSONSchema(s.value)}

	if s.min >= 0 {
		m["minProperties"] = s.min
	}

	if s.max >= 0 {
		m["maxProperties"] = s.max
	}

	return s.o.decorate(m, "object")
}

func (c structCore) jsonSchema() map[string]any {
	props := map[string]any{}

	var required []string

	for _, f := range c.fields {
		prop := JSONSchema(f.schema)
		props[f.key] = prop

		// The parser accepts a Nullable key that is absent, so it is not
		// required here either.
		if !f.schema.optional() && !allowsNull(prop) {
			required = append(required, f.key)
		}
	}

	m := map[string]any{"properties": props}
	if len(required) > 0 {
		slices.Sort(required)
		m["required"] = required
	}

	if c.strict {
		m["additionalProperties"] = false
	}

	return m
}

func (s StructSchema[T]) jsonSchema() map[string]any {
	return s.o.decorate(s.core.jsonSchema(), "object")
}

func (s structAny) jsonSchema() map[string]any {
	return s.core.o.decorate(s.core.jsonSchema(), "object")
}

func (c convertedString) jsonSchema() map[string]any { return c.s.jsonSchema() }

func (n numberAny) jsonSchema() map[string]any {
	m := maps.Clone(n.kw)
	if m == nil {
		m = map[string]any{}
	}

	m["type"] = numberType(n.t)

	return m
}

// allowsNull reports a schema whose type list includes "null" (Nullable).
func allowsNull(m map[string]any) bool {
	types, _ := m["type"].([]any)
	return slices.Contains(types, any("null"))
}

// jsonSchema for a JSON value: an object says so, any value says nothing
// at all, which is how JSON Schema and OpenAPI 3.1 spell "anything".
func (s JSONValueSchema) jsonSchema() map[string]any {
	if s.object {
		return s.o.decorate(map[string]any{}, "object")
	}

	return s.o.decorate(map[string]any{}, "")
}
