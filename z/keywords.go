package z

import (
	"fmt"
	"reflect"
	"slices"
	"time"
)

// structural keys describe the shape of a value, which document generators
// own; Keywords returns only what the rules add on top of it.
var structuralKeys = []string{
	"type",
	"items",
	"properties",
	"additionalProperties",
	"required",
	"$defs",
} //nolint:gochecknoglobals // read-only

// Keywords returns the JSON Schema keywords the `validate` rules enforce on a
// value of type t, the same ones FromTags validates: minLength, maxLength,
// pattern, format, enum, minimum, maximum, exclusiveMinimum, minItems,
// maxItems, uniqueItems, default and the anyOf an omitempty string adds.
// Pointers are dereferenced. Rules from "dive" on are ignored: call it again
// with the element type and those rules. required reports the "required"
// rule. It never returns "type", "items", "properties" or
// "additionalProperties", and a struct, any or map[string]any gives an empty
// map (those take only "required"). It returns an error, not a panic, for a
// rule FromTags rejects.
func Keywords(t reflect.Type, rules []string) (kw map[string]any, required bool, err error) {
	if i := slices.Index(rules, "dive"); i >= 0 {
		rules = rules[:i]
	}

	// Cross-field rules (required_if, gtfield…) compare keys of the struct;
	// they say nothing about this value's own keywords.
	rules, _ = splitCross(rules)

	rs := parseRules(rules)
	if err := rs.coherent(); err != nil {
		return nil, false, err
	}

	base := t
	for base.Kind() == reflect.Pointer {
		base = base.Elem()
	}

	if base.Kind() == reflect.Struct && base != reflect.TypeFor[time.Time]() || base.Kind() == reflect.Interface ||
		base == reflect.TypeFor[map[string]any]() {
		return map[string]any{}, rs.required, nil
	}

	// The element schema is discarded (items is structural), and building it
	// for a recursive element type would panic: describe the list or map with
	// an any element, which takes the same length and uniqueness rules.
	switch base.Kind() { //nolint:exhaustive // only containers are rewritten
	case reflect.Slice:
		if base.Elem().Kind() != reflect.Uint8 {
			t = reflect.SliceOf(reflect.TypeFor[any]())
		}
	case reflect.Map:
		t = reflect.MapOf(base.Key(), reflect.TypeFor[any]())
	}

	defer func() {
		if r := recover(); r != nil {
			kw, required, err = nil, false, fmt.Errorf("z: %v", r)
		}
	}()

	s, err := schemaForType(t, rules, 0)
	if err != nil {
		return nil, false, err
	}

	return SchemaKeywords(s), rs.required, nil
}

// SchemaKeywords returns the constraint keywords of s for a document whose
// structure (type, items, properties, additionalProperties, required) is
// described elsewhere: what JSONSchema(s) says minus those keys, with enum
// lists as []any and a required string's minLength at least 1 (z refuses ""
// for it whatever Min says, which its JSON Schema does not show).
func SchemaKeywords(s AnySchema) map[string]any {
	kw := JSONSchema(s)
	for _, k := range structuralKeys {
		delete(kw, k)
	}

	if t := s.GoType(); t != nil && t.Kind() == reflect.String && !s.optional() {
		if n, ok := kw["minLength"].(int); !ok || n < 1 {
			kw["minLength"] = 1
		}
	}

	if e, ok := kw["enum"].([]string); ok {
		vals := make([]any, len(e))
		for i, v := range e {
			vals[i] = v
		}

		kw["enum"] = vals
	}

	return kw
}

// Properties returns the schema of each key of a struct schema, built with
// Struct or derived from tags (For, FromTags), and whether s is one.
func Properties(s AnySchema) (map[string]AnySchema, bool) {
	fs, ok := s.(interface{ structFields() []structField })
	if !ok {
		return nil, false
	}

	out := map[string]AnySchema{}
	for _, f := range fs.structFields() {
		out[f.key] = f.schema
	}

	return out, true
}

func (s StructSchema[T]) structFields() []structField { return s.core.fields }
func (s structAny) structFields() []structField       { return s.core.fields }
