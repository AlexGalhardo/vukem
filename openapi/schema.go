package openapi

import (
	"encoding"
	"encoding/json"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Schema is a JSON Schema (2020-12, as used by OpenAPI 3.1) object.
type Schema = map[string]any

// Schemas turns Go types into JSON Schemas, collecting named structs as
// reusable components. Constraints come from the same `validate` tags the
// z package enforces, so the document matches what the server accepts.
type Schemas struct {
	defs  map[string]Schema
	names map[schemaKey]string
	// outputs, set by Info.RequiredOutputs, holds the named types some
	// response reaches. Components then depend on the direction: an
	// output schema requires every field the server always sends, and a
	// type in outputs reached from a request is <Name>Input.
	outputs map[reflect.Type]bool
	// input is true while a request (parameters, body) is described.
	input bool
}

type schemaKey struct {
	t     reflect.Type
	input bool
}

// NewSchemas returns an empty component registry.
func NewSchemas() *Schemas {
	return &Schemas{defs: map[string]Schema{}, names: map[schemaKey]string{}}
}

// Components returns the named schemas collected so far.
func (s *Schemas) Components() map[string]Schema { return s.defs }

// For returns the schema of t: a $ref for named structs, inline otherwise.
func (s *Schemas) For(t reflect.Type) Schema { return s.of(t, nil) }

var (
	timeType      = reflect.TypeFor[time.Time]()
	rawType       = reflect.TypeFor[json.RawMessage]()
	marshalerType = reflect.TypeFor[json.Marshaler]()
	textType      = reflect.TypeFor[encoding.TextMarshaler]()
	nameRe        = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)
)

func (s *Schemas) of(t reflect.Type, rules []string) Schema {
	if t.Kind() == reflect.Pointer {
		inner := s.of(t.Elem(), rules)
		if _, ok := inner["$ref"]; ok {
			return Schema{"anyOf": []any{inner, Schema{"type": "null"}}}
		}

		if typ, ok := inner["type"].(string); ok {
			inner["type"] = []any{typ, "null"}
		}

		return inner
	}

	value, dive := splitDive(rules)

	switch {
	case t == timeType:
		return Schema{"type": "string", "format": "date-time"}
	case t.PkgPath() == ormPkg && t.Name() == "NullTime":
		return Schema{"type": []any{"string", "null"}, "format": "date-time"}
	case t == rawType:
		return Schema{}
	case t.Kind() == reflect.Struct && unwrapsJSON(t):
		// Relations and nullable wrappers marshal as their inner value.
		return s.wrapped(t)
	case t.Implements(marshalerType) || reflect.PointerTo(t).Implements(marshalerType):
		return Schema{}
	case t.Kind() != reflect.String && (t.Implements(textType) || reflect.PointerTo(t).Implements(textType)):
		return Schema{"type": "string"}
	}

	switch t.Kind() {
	case reflect.String:
		return stringSchema(value)
	case reflect.Bool:
		return Schema{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return numberSchema("integer", t, value)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		sc := numberSchema("integer", t, value)
		if _, ok := sc["minimum"]; !ok {
			sc["minimum"] = 0
		}

		return sc
	case reflect.Float32, reflect.Float64:
		return numberSchema("number", t, value)
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return Schema{"type": "string", "contentEncoding": "base64"}
		}

		sc := Schema{"type": "array", "items": s.of(t.Elem(), dive)}
		lengths(sc, value, "minItems", "maxItems")

		for _, r := range value {
			if r == "unique" {
				sc["uniqueItems"] = true
			}
		}

		return sc
	case reflect.Map:
		return Schema{"type": "object", "additionalProperties": s.of(t.Elem(), dive)}
	case reflect.Struct:
		if t.Name() == "" {
			return s.Object(t, nil)
		}

		return s.ref(t)
	default:
		return Schema{}
	}
}

const ormPkg = "github.com/AlexGalhardo/vukem/orm"

// unwrapsJSON reports types whose JSON is a single inner value: the orm
// relation wrappers (one exported-less field holding T or []T).
func unwrapsJSON(t reflect.Type) bool {
	if !t.Implements(marshalerType) || t.NumField() == 0 || t.PkgPath() != ormPkg {
		return false
	}

	n := t.Name()

	return strings.HasPrefix(n, "BelongsTo[") || strings.HasPrefix(n, "HasOne[") ||
		strings.HasPrefix(n, "HasMany[") || strings.HasPrefix(n, "ManyToMany[")
}

func (s *Schemas) wrapped(t reflect.Type) Schema {
	for f := range t.Fields() {
		if f.Type.Kind() == reflect.Slice {
			// Not loaded marshals as null.
			return s.of(reflect.PointerTo(f.Type), nil)
		}

		if f.Type.Kind() == reflect.Pointer || f.Type.Kind() == reflect.Struct {
			ft := f.Type
			if ft.Kind() != reflect.Pointer {
				ft = reflect.PointerTo(ft)
			}

			return s.of(ft, nil)
		}
	}

	return Schema{}
}

func (s *Schemas) ref(t reflect.Type) Schema {
	key := schemaKey{t, s.input && s.outputs != nil}

	name, ok := s.names[key]
	if !ok {
		suffix := ""
		if key.input && s.outputs[t] {
			suffix = "Input"
		}

		name = s.uniqueName(t, suffix)
		s.names[key] = name
		s.defs[name] = Schema{} // placeholder: recursive types refer to it
		s.defs[name] = s.Object(t, nil)
	}

	return Schema{"$ref": "#/components/schemas/" + name}
}

func (s *Schemas) uniqueName(t reflect.Type, suffix string) string {
	base := nameRe.ReplaceAllString(t.Name(), "_")
	base = strings.Trim(base, "_") + suffix

	if _, taken := s.defs[base]; !taken {
		return base
	}

	pkg := t.PkgPath()
	if i := strings.LastIndexByte(pkg, '/'); i >= 0 {
		pkg = pkg[i+1:]
	}

	name := pkg + "." + base
	for i := 2; ; i++ {
		if _, taken := s.defs[name]; !taken {
			return name
		}

		name = pkg + "." + base + strconv.Itoa(i)
	}
}

// Object returns the inline object schema of struct t without the
// properties named in skip (e.g. path parameters).
func (s *Schemas) Object(t reflect.Type, skip map[string]bool) Schema {
	props := map[string]any{}

	var required []string

	for _, f := range Fields(t) {
		if skip[f.Key] {
			continue
		}

		sc := s.of(f.Type, f.Rules)
		if f.Doc != "" {
			sc = withDescription(sc, f.Doc)
		}

		if v := f.ExampleValue(); v != nil {
			sc = withExamples(sc, v)
		}

		props[f.Key] = sc

		if f.Required || s.outputs != nil && !s.input && !f.optional {
			required = append(required, f.Key)
		}
	}

	out := Schema{"type": "object", "properties": props}
	if len(required) > 0 {
		out["required"] = required
	}

	return out
}

func withDescription(sc Schema, doc string) Schema {
	if _, ok := sc["$ref"]; ok {
		return Schema{"allOf": []any{sc}, "description": doc}
	}

	sc["description"] = doc

	return sc
}

// withExamples sets the JSON-Schema examples keyword, wrapping a $ref the
// way withDescription does: keywords beside a $ref are ignored.
func withExamples(sc Schema, v any) Schema {
	if _, ok := sc["$ref"]; ok {
		return Schema{"allOf": []any{sc}, "examples": []any{v}}
	}

	sc["examples"] = []any{v}

	return sc
}

// Field is one JSON property of a struct.
type Field struct {
	Key      string
	Type     reflect.Type
	Rules    []string
	Required bool
	Doc      string
	// Example is the raw `example` struct tag; ExampleValue decodes it.
	Example string
	// optional: a response may leave the key out or send null (a pointer,
	// omitempty, omitzero, or promoted through an embedded pointer).
	optional bool
}

// ExampleValue decodes the field's `example` tag into a value of the
// field's JSON type, so the document shows 7 rather than "7". A composite
// field (slice, map, struct) takes JSON; anything that does not decode
// stays the raw string. It returns nil when there is no tag.
func (f Field) ExampleValue() any {
	if f.Example == "" {
		return nil
	}

	t := f.Type
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	// A type with its own text form (time.Time, a UUID) marshals as a
	// string whatever its Go kind is.
	if t.Kind() == reflect.String || t == timeType ||
		t.Implements(textType) || reflect.PointerTo(t).Implements(textType) {
		return f.Example
	}

	switch t.Kind() {
	case reflect.Bool:
		if v, err := strconv.ParseBool(f.Example); err == nil {
			return v
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if v, err := strconv.ParseInt(f.Example, 10, 64); err == nil {
			return v
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if v, err := strconv.ParseUint(f.Example, 10, 64); err == nil {
			return v
		}
	case reflect.Float32, reflect.Float64:
		if v, err := strconv.ParseFloat(f.Example, 64); err == nil {
			return v
		}
	case reflect.Slice, reflect.Array, reflect.Map, reflect.Struct, reflect.Interface:
		var v any
		if err := json.Unmarshal([]byte(f.Example), &v); err == nil {
			return v
		}
	default: // a kind with no JSON form of its own keeps the raw tag
	}

	return f.Example
}

// Fields lists the JSON properties of struct t (embedded structs are
// flattened, `json:"-"` is skipped), with their validate rules, their
// `doc` description and their raw `example` tag.
func Fields(t reflect.Type) []Field {
	var out []Field

	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() || (f.Anonymous && f.Type.Kind() == reflect.Struct) {
			continue
		}

		key := f.Name
		optional := f.Type.Kind() == reflect.Pointer

		if tag, ok := f.Tag.Lookup("json"); ok {
			name, opts, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}

			if name != "" {
				key = name
			}

			for o := range strings.SplitSeq(opts, ",") {
				optional = optional || o == "omitempty" || o == "omitzero"
			}
		}

		for i := 1; i < len(f.Index); i++ {
			optional = optional || t.FieldByIndex(f.Index[:i]).Type.Kind() == reflect.Pointer
		}

		rules := splitRules(f.Tag.Get("validate"))
		value, _ := splitDive(rules)

		out = append(out, Field{
			Key: key, Type: f.Type, Rules: rules, Required: has(value, "required"),
			Doc: f.Tag.Get("doc"), Example: f.Tag.Get("example"), optional: optional,
		})
	}

	return out
}

// splitRules mirrors z: regex= is last and may contain commas.
func splitRules(tag string) []string {
	if tag == "" {
		return nil
	}

	if i := strings.Index(tag, "regex="); i >= 0 {
		head := strings.TrimSuffix(tag[:i], ",")

		var rules []string
		if head != "" {
			rules = strings.Split(head, ",")
		}

		return append(rules, tag[i:])
	}

	return strings.Split(tag, ",")
}

func splitDive(rules []string) (value, dive []string) {
	for i, r := range rules {
		if strings.TrimSpace(r) == "dive" {
			return rules[:i], rules[i+1:]
		}
	}

	return rules, nil
}

func has(rules []string, name string) bool {
	for _, r := range rules {
		if strings.TrimSpace(r) == name {
			return true
		}
	}

	return false
}

func lengths(sc Schema, rules []string, minKey, maxKey string) {
	for _, r := range rules {
		name, arg, _ := strings.Cut(strings.TrimSpace(r), "=")

		n, err := strconv.Atoi(arg)
		if err != nil {
			continue
		}

		switch name {
		case "min":
			sc[minKey] = n
		case "max":
			sc[maxKey] = n
		case "len":
			sc[minKey], sc[maxKey] = n, n
		}
	}
}

func stringSchema(rules []string) Schema {
	sc := Schema{"type": "string"}
	lengths(sc, rules, "minLength", "maxLength")

	// z rejects "" for a required string whatever min says.
	if n, _ := sc["minLength"].(int); has(rules, "required") && n < 1 {
		sc["minLength"] = 1
	}

	for _, r := range rules {
		name, arg, _ := strings.Cut(strings.TrimSpace(r), "=")

		switch name {
		case "email":
			sc["format"] = "email"
		case "url":
			sc["format"] = "uri"
		case "uuid":
			sc["format"] = "uuid"
		case "ulid":
			sc["pattern"] = "^[0-9A-HJKMNP-TV-Z]{26}$"
		case "alpha":
			sc["pattern"] = "^[a-zA-Z]+$"
		case "alphanum":
			sc["pattern"] = "^[a-zA-Z0-9]+$"
		case "numeric":
			sc["pattern"] = "^[0-9]+$"
		case "oneof":
			var enum []any
			for v := range strings.FieldsSeq(arg) {
				enum = append(enum, v)
			}

			sc["enum"] = enum
		case "regex":
			sc["pattern"] = arg
		case "startswith":
			sc["pattern"] = "^" + regexp.QuoteMeta(arg)
		case "endswith":
			sc["pattern"] = regexp.QuoteMeta(arg) + "$"
		case "contains":
			sc["pattern"] = regexp.QuoteMeta(arg)
		}
	}

	return sc
}

func numberSchema(typ string, t reflect.Type, rules []string) Schema {
	sc := Schema{"type": typ}

	switch t.Kind() { //nolint:exhaustive // other kinds have no format
	case reflect.Int32, reflect.Uint32:
		sc["format"] = "int32"
	case reflect.Int64, reflect.Uint64:
		sc["format"] = "int64"
	case reflect.Float32:
		sc["format"] = "float"
	case reflect.Float64:
		sc["format"] = "double"
	}

	for _, r := range rules {
		name, arg, _ := strings.Cut(strings.TrimSpace(r), "=")

		n, err := strconv.ParseFloat(arg, 64)

		switch {
		case name == "positive":
			sc["exclusiveMinimum"] = 0
		case err != nil:
		case name == "min":
			sc["minimum"] = n
		case name == "max":
			sc["maximum"] = n
		}
	}

	return sc
}
