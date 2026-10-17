package z

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- Slice

// SliceSchema parses arrays into []E where E is the element schema's type.
// From forms, a single value is accepted as a one-element list.
type SliceSchema struct {
	o      opts[any]
	elem   AnySchema
	min    int
	max    int
	length int
	unique bool
}

// Slice returns a schema for a list whose elements match elem.
func Slice(elem AnySchema) SliceSchema { return SliceSchema{elem: elem, min: -1, max: -1, length: -1} }

// GoType implements AnySchema.
func (s SliceSchema) GoType() reflect.Type { return reflect.SliceOf(s.elem.GoType()) }

func (s SliceSchema) optional() bool { return s.o.optional || s.o.hasDef }

func (s SliceSchema) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	return parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (any, bool) {
		items, isList := in.([]any)
		if !isList {
			if rv := reflect.ValueOf(
				in,
			); rv.IsValid() && rv.Kind() == reflect.Slice &&
				rv.Type() != reflect.TypeFor[[]byte]() {
				items = make([]any, rv.Len())
				for i := range items {
					items[i] = rv.Index(i).Interface()
				}
			} else if _, isStr := in.(string); isStr &&
				c.coerce {
				items = []any{in}
			} else {
				s.o.issue(
					c,
					path,
					CodeInvalidType,
					CodeInvalidType,
					map[string]any{"expected": "a list", "received": typeName(in)},
				)
				return nil, false
			}
		}

		before := len(c.issues)

		switch n := len(items); {
		case s.length >= 0 && n != s.length:
			code := CodeTooSmall
			if n > s.length {
				code = CodeTooBig
			}

			s.o.issue(c, path, code, "exact.array", map[string]any{"len": s.length})
		case s.min >= 0 && n < s.min:
			s.o.issue(c, path, CodeTooSmall, "too_small.array", map[string]any{"min": s.min})
		case s.max >= 0 && n > s.max:
			s.o.issue(c, path, CodeTooBig, "too_big.array", map[string]any{"max": s.max})
		}

		out := reflect.MakeSlice(s.GoType(), 0, len(items))

		for i, item := range items {
			v, set, ok := s.elem.parseAny(c, joinPath(path, strconv.Itoa(i)), item, true)
			if !ok {
				continue
			}

			if set {
				out = reflect.Append(out, reflect.ValueOf(v))
			} else {
				out = reflect.Append(out, reflect.Zero(s.elem.GoType()))
			}
		}

		if s.unique && len(c.issues) == before && hasDuplicates(out) {
			s.o.issue(c, path, CodeNotUnique, CodeNotUnique, nil)
		}

		return out.Interface(), len(c.issues) == before
	})
}

func hasDuplicates(v reflect.Value) bool {
	if !v.Type().Elem().Comparable() {
		return false
	}

	seen := make(map[any]bool, v.Len())
	for i := range v.Len() {
		k := v.Index(i).Interface()
		if seen[k] {
			return true
		}

		seen[k] = true
	}

	return false
}

// Min requires at least n items.
func (s SliceSchema) Min(n int) SliceSchema { s.o = s.o.clone(); s.min = n; return s }

// Max allows at most n items.
func (s SliceSchema) Max(n int) SliceSchema { s.o = s.o.clone(); s.max = n; return s }

// Len requires exactly n items.
func (s SliceSchema) Len(n int) SliceSchema { s.o = s.o.clone(); s.length = n; return s }

// NonEmpty is Min(1).
func (s SliceSchema) NonEmpty() SliceSchema { return s.Min(1) }

// Unique rejects duplicate items.
func (s SliceSchema) Unique() SliceSchema { s.o = s.o.clone(); s.unique = true; return s }

// Optional allows the key to be absent (nil slice).
func (s SliceSchema) Optional() SliceSchema { s.o = s.o.clone(); s.o.optional = true; return s }

// Nullable allows null.
func (s SliceSchema) Nullable() SliceSchema { s.o = s.o.clone(); s.o.nullable = true; return s }

// Default uses v (a slice of the element type) when the key is absent.
func (s SliceSchema) Default(v any) SliceSchema {
	if reflect.TypeOf(v) != s.GoType() {
		panic(fmt.Sprintf("z: Slice default must be %s, got %T", s.GoType(), v))
	}

	s.o = s.o.clone()
	s.o.hasDef, s.o.def = true, v

	return s
}

// Message overrides the message for an issue code.
func (s SliceSchema) Message(code, message string) SliceSchema {
	s.o = s.o.clone()
	if s.o.messages == nil {
		s.o.messages = map[string]string{}
	}

	s.o.messages[code] = message

	return s
}

// Describe sets a description used by schema exports.
func (s SliceSchema) Describe(text string) SliceSchema { s.o = s.o.clone(); s.o.desc = text; return s }

// Parse returns the parsed slice as any ([]E).
func (s SliceSchema) Parse(in any) (any, error) { return parseRoot[any](s, in) }

// ------------------------------------------------------------------ Map

// MapSchema parses objects into map[string]V.
type MapSchema struct {
	o     opts[any]
	value AnySchema
	min   int
	max   int
}

// Map returns a schema for an object with string keys and values matching
// value.
func Map(value AnySchema) MapSchema { return MapSchema{value: value, min: -1, max: -1} }

// GoType implements AnySchema.
func (s MapSchema) GoType() reflect.Type {
	return reflect.MapOf(reflect.TypeFor[string](), s.value.GoType())
}

func (s MapSchema) optional() bool { return s.o.optional || s.o.hasDef }

func (s MapSchema) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	return parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (any, bool) {
		m, isMap := in.(map[string]any)
		if !isMap {
			s.o.issue(
				c,
				path,
				CodeInvalidType,
				CodeInvalidType,
				map[string]any{"expected": "an object", "received": typeName(in)},
			)
			return nil, false
		}

		before := len(c.issues)

		if s.min >= 0 && len(m) < s.min {
			s.o.issue(c, path, CodeTooSmall, "too_small.array", map[string]any{"min": s.min})
		}

		if s.max >= 0 && len(m) > s.max {
			s.o.issue(c, path, CodeTooBig, "too_big.array", map[string]any{"max": s.max})
		}

		out := reflect.MakeMapWithSize(s.GoType(), len(m))

		for _, k := range sortedKeys(m) {
			v, set, ok := s.value.parseAny(c, joinPath(path, k), m[k], true)
			if !ok {
				continue
			}

			if set {
				out.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(v))
			} else {
				out.SetMapIndex(reflect.ValueOf(k), reflect.Zero(s.value.GoType()))
			}
		}

		return out.Interface(), len(c.issues) == before
	})
}

// Min requires at least n keys.
func (s MapSchema) Min(n int) MapSchema { s.o = s.o.clone(); s.min = n; return s }

// Max allows at most n keys.
func (s MapSchema) Max(n int) MapSchema { s.o = s.o.clone(); s.max = n; return s }

// Optional allows the key to be absent.
func (s MapSchema) Optional() MapSchema { s.o = s.o.clone(); s.o.optional = true; return s }

// Nullable allows null.
func (s MapSchema) Nullable() MapSchema { s.o = s.o.clone(); s.o.nullable = true; return s }

// Parse returns the parsed map as any (map[string]V).
func (s MapSchema) Parse(in any) (any, error) { return parseRoot[any](s, in) }

// --------------------------------------------------------------- Struct

// Shape maps input keys to field schemas.
type Shape map[string]AnySchema

type structField struct {
	key    string
	index  []int
	schema AnySchema
	ptr    bool
	conv   bool
	ftype  reflect.Type
}

// structCore parses objects into a struct type; StructSchema wraps it with
// static typing.
type structCore struct {
	rt     reflect.Type
	fields []structField
	strict bool
	o      opts[any]
}

func newStructCore(rt reflect.Type, shape Shape) structCore {
	if rt.Kind() != reflect.Struct {
		panic(fmt.Sprintf("z: Struct needs a struct type, got %s", rt))
	}

	byKey := map[string]reflect.StructField{}

	for _, f := range reflect.VisibleFields(rt) {
		if !f.IsExported() || (f.Anonymous && f.Type.Kind() == reflect.Struct) {
			continue
		}

		for _, k := range fieldKeys(f) {
			if _, taken := byKey[k]; !taken {
				byKey[k] = f
			}
		}
	}

	core := structCore{rt: rt}

	for _, key := range sortedKeys(shape) {
		schema := shape[key]

		f, ok := byKey[key]
		if !ok {
			f, ok = byKey[strings.ToLower(key)]
		}

		if !ok {
			panic(fmt.Sprintf("z: %s has no field for key %q (match json/form tag or field name)", rt, key))
		}

		st, ft := schema.GoType(), f.Type
		sf := structField{key: key, index: f.Index, schema: schema, ftype: ft}

		switch {
		case st.AssignableTo(ft):
		case ft.Kind() == reflect.Pointer && st.AssignableTo(ft.Elem()):
			sf.ptr = true
		case st.ConvertibleTo(ft) && st.Kind() == ft.Kind():
			sf.conv = true
		case ft.Kind() == reflect.Pointer && st.ConvertibleTo(ft.Elem()) && st.Kind() == ft.Elem().Kind():
			sf.ptr, sf.conv = true, true
		default:
			panic(fmt.Sprintf("z: %s.%s is %s but the schema for %q produces %s", rt, f.Name, ft, key, st))
		}

		core.fields = append(core.fields, sf)
	}

	return core
}

// fieldKeys returns the input keys a struct field answers to.
func fieldKeys(f reflect.StructField) []string {
	var keys []string

	for _, tag := range []string{"json", "form", "query"} {
		name, _, _ := strings.Cut(f.Tag.Get(tag), ",")
		if name == "-" {
			return nil
		}

		if name != "" {
			keys = append(keys, name)
		}
	}

	return append(keys, f.Name, strings.ToLower(f.Name))
}

func (s structCore) parse(c *pctx, path string, in any) (reflect.Value, bool) {
	m, isMap := in.(map[string]any)
	if !isMap {
		s.o.issue(
			c,
			path,
			CodeInvalidType,
			CodeInvalidType,
			map[string]any{"expected": "an object", "received": typeName(in)},
		)
		return reflect.Value{}, false
	}

	before := len(c.issues)
	out := reflect.New(s.rt).Elem()

	for _, f := range s.fields {
		raw, present := m[f.key]

		v, set, ok := f.schema.parseAny(c, joinPath(path, f.key), raw, present)
		if !ok || !set {
			continue
		}

		rv := reflect.ValueOf(v)

		target := f.ftype
		if f.ptr {
			target = f.ftype.Elem()
		}

		if f.conv {
			rv = rv.Convert(target)
		}

		if f.ptr {
			p := reflect.New(target)
			p.Elem().Set(rv)
			rv = p
		}

		out.FieldByIndex(f.index).Set(rv)
	}

	if s.strict {
		known := make(map[string]bool, len(s.fields))
		for _, f := range s.fields {
			known[f.key] = true
		}

		for _, k := range sortedKeys(m) {
			if !known[k] {
				s.o.issue(c, joinPath(path, k), CodeUnrecognizedKeys, CodeUnrecognizedKeys, nil)
			}
		}
	}

	return out, len(c.issues) == before
}

// StructSchema parses objects into T.
type StructSchema[T any] struct {
	core structCore
	o    opts[T]
}

// Struct returns a schema producing T from an object whose keys are
// described by shape. Keys map to fields by json, form or query tag, or by
// field name. It panics when a key has no field or the schema's type does
// not fit the field, so mistakes surface at boot.
func Struct[T any](shape Shape) StructSchema[T] {
	return StructSchema[T]{core: newStructCore(reflect.TypeFor[T](), shape)}
}

// GoType implements AnySchema.
func (StructSchema[T]) GoType() reflect.Type { return reflect.TypeFor[T]() }

func (s StructSchema[T]) optional() bool { return s.o.optional || s.o.hasDef }

func (s StructSchema[T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (T, bool) {
		if t, isT := in.(T); isT {
			in = toMap(t)
		}

		rv, ok := s.core.parse(c, path, in)
		if !ok {
			var zero T
			return zero, false
		}

		out, _ := reflect.TypeAssert[T](rv)

		return out, true
	})

	return v, set, ok
}

// Strict rejects keys that are not in the shape.
func (s StructSchema[T]) Strict() StructSchema[T] {
	s.core.strict = true
	return s
}

// Optional allows the key to be absent.
func (s StructSchema[T]) Optional() StructSchema[T] { s.o = s.o.clone(); s.o.optional = true; return s }

// Nullable allows null.
func (s StructSchema[T]) Nullable() StructSchema[T] { s.o = s.o.clone(); s.o.nullable = true; return s }

// Refine adds a cross-field check; return FieldError to point at a field.
func (s StructSchema[T]) Refine(fn func(T) error) StructSchema[T] {
	s.o = s.o.clone()
	s.o.refines = append(s.o.refines, fn)

	return s
}

// Transform maps the parsed value.
func (s StructSchema[T]) Transform(fn func(T) T) StructSchema[T] {
	s.o = s.o.clone()
	s.o.transforms = append(s.o.transforms, fn)

	return s
}

// Describe sets a description used by schema exports.
func (s StructSchema[T]) Describe(text string) StructSchema[T] {
	s.o = s.o.clone()
	s.o.desc = text
	return s
}

// Keys returns the shape keys in order.
func (s StructSchema[T]) Keys() []string {
	keys := make([]string, len(s.core.fields))
	for i, f := range s.core.fields {
		keys[i] = f.key
	}

	return keys
}

// Field returns the struct field of T that key parses into, for tools that
// map schema keys to other types (the admin panel copies them to models).
func (s StructSchema[T]) Field(key string) (reflect.StructField, bool) {
	for _, f := range s.core.fields {
		if f.key == key {
			sf := s.core.rt.FieldByIndex(f.index)
			sf.Index = slices.Clone(f.index)

			return sf, true
		}
	}

	return reflect.StructField{}, false
}

// Parse implements Schema.
func (s StructSchema[T]) Parse(in any) (T, error) { return parseRoot[T](s, in) }

// Check implements Schema.
func (s StructSchema[T]) Check(v T) error { return checkValue(s, v) }

// toMap converts a struct value back to generic input for re-validation.
func toMap(v any) any {
	in, err := normalizeValue(v)
	if err != nil {
		return v
	}

	return in
}

// --------------------------------------------------------------- Custom

// CustomSchema parses with a user function.
type CustomSchema[T any] struct {
	o  opts[T]
	fn func(in any) (T, error)
}

// Custom returns a schema whose parsing is done by fn; a returned error
// becomes an issue with its message.
func Custom[T any](fn func(in any) (T, error)) CustomSchema[T] { return CustomSchema[T]{fn: fn} }

// GoType implements AnySchema.
func (CustomSchema[T]) GoType() reflect.Type { return reflect.TypeFor[T]() }

func (s CustomSchema[T]) optional() bool { return s.o.optional || s.o.hasDef }

func (s CustomSchema[T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, true, func(c *pctx, path string, in any) (T, bool) {
		v, err := s.fn(in)
		if err != nil {
			c.add(path, CodeCustom, err.Error(), nil)
			return v, false
		}

		return v, true
	})

	return v, set, ok
}

// Optional allows the key to be absent.
func (s CustomSchema[T]) Optional() CustomSchema[T] { s.o = s.o.clone(); s.o.optional = true; return s }

// Parse implements Schema.
func (s CustomSchema[T]) Parse(in any) (T, error) { return parseRoot[T](s, in) }

// Check implements Schema.
func (s CustomSchema[T]) Check(v T) error { return checkValue(s, v) }
