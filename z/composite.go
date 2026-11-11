package z

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// ---------------------------------------------------------------- Slice

// sliceCore parses lists into a slice of the element schema's type;
// SliceSchema wraps it with static typing and sliceAny serves the tag path.
type sliceCore struct {
	o      opts[any] // message overrides for the issues the core reports
	elem   AnySchema
	min    int
	max    int
	length int
	unique bool
}

func newSliceCore(elem AnySchema) sliceCore {
	return sliceCore{elem: elem, min: -1, max: -1, length: -1}
}

func (c sliceCore) goType() reflect.Type { return reflect.SliceOf(c.elem.GoType()) }

// parse reads a list and its items; the result is a slice of goType.
func (c sliceCore) parse(p *pctx, path string, in any) (reflect.Value, bool) {
	items, ok := c.listItems(p, path, in)
	if !ok {
		return reflect.Value{}, false
	}

	before := len(p.issues)

	c.checkLen(p, path, len(items))

	out := c.parseItems(p, path, items)

	if c.unique && len(p.issues) == before && hasDuplicates(out) {
		c.o.issue(p, path, CodeNotUnique, CodeNotUnique, nil)
	}

	return out, len(p.issues) == before
}

// listItems accepts a decoded list, any Go slice but []byte, or (when
// coercing, as from forms) a single string as a one-element list.
func (c sliceCore) listItems(p *pctx, path string, in any) ([]any, bool) {
	if items, isList := in.([]any); isList {
		return items, true
	}

	if rv := reflect.ValueOf(in); rv.IsValid() && rv.Kind() == reflect.Slice && rv.Type() != reflect.TypeFor[[]byte]() {
		items := make([]any, rv.Len())
		for i := range items {
			items[i] = rv.Index(i).Interface()
		}

		return items, true
	}

	if _, isStr := in.(string); isStr && p.coerce {
		return []any{in}, true
	}

	c.o.issue(p, path, CodeInvalidType, CodeInvalidType,
		map[string]any{"expected": "a list", "received": typeName(in)})

	return nil, false
}

func (c sliceCore) checkLen(p *pctx, path string, n int) {
	switch {
	case c.length >= 0 && n != c.length:
		code := CodeTooSmall
		if n > c.length {
			code = CodeTooBig
		}

		c.o.issue(p, path, code, "exact.array", map[string]any{"len": c.length})
	case c.min >= 0 && n < c.min:
		c.o.issue(p, path, CodeTooSmall, "too_small.array", map[string]any{"min": c.min})
	case c.max >= 0 && n > c.max:
		c.o.issue(p, path, CodeTooBig, "too_big.array", map[string]any{"max": c.max})
	}
}

func (c sliceCore) parseItems(p *pctx, path string, items []any) reflect.Value {
	out := reflect.MakeSlice(c.goType(), 0, len(items))

	for i, item := range items {
		v, set, ok := c.elem.parseAny(p, joinPath(path, strconv.Itoa(i)), item, true)
		if !ok {
			continue
		}

		if set {
			out = reflect.Append(out, reflect.ValueOf(v))
		} else {
			out = reflect.Append(out, reflect.Zero(c.elem.GoType()))
		}
	}

	return out
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

// SliceSchema parses arrays into []E where E is the element schema's type.
// From forms, a single value is accepted as a one-element list.
type SliceSchema[E any] struct {
	core sliceCore
	o    opts[[]E]
}

// Slice returns a schema for a list whose elements match elem. The element
// type is inferred: Slice(String()) is a Schema[[]string].
func Slice[E any](elem Schema[E]) SliceSchema[E] { return SliceSchema[E]{core: newSliceCore(elem)} }

// GoType implements AnySchema.
func (s SliceSchema[E]) GoType() reflect.Type { return reflect.TypeFor[[]E]() }

func (s SliceSchema[E]) optional() bool { return s.o.optional || s.o.hasDef }

func (s SliceSchema[E]) asOptional() AnySchema { return s.Optional() }

func (s SliceSchema[E]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) ([]E, bool) {
		rv, ok := s.core.parse(c, path, in)
		if !ok {
			return nil, false
		}

		out, _ := reflect.TypeAssert[[]E](rv)

		return out, true
	})

	if set && s.o.hasDef && (!present || in == nil) {
		v = slices.Clone(v) // the default is the schema's: never hand it out
	}

	return v, set, ok
}

// Min requires at least n items.
func (s SliceSchema[E]) Min(n int) SliceSchema[E] { s.core.min = n; return s }

// Max allows at most n items.
func (s SliceSchema[E]) Max(n int) SliceSchema[E] { s.core.max = n; return s }

// Len requires exactly n items.
func (s SliceSchema[E]) Len(n int) SliceSchema[E] { s.core.length = n; return s }

// NonEmpty is Min(1).
func (s SliceSchema[E]) NonEmpty() SliceSchema[E] { return s.Min(1) }

// Unique rejects duplicate items.
func (s SliceSchema[E]) Unique() SliceSchema[E] { s.core.unique = true; return s }

// Optional allows the key to be absent (nil slice).
func (s SliceSchema[E]) Optional() SliceSchema[E] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s SliceSchema[E]) Nullable() SliceSchema[E] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent; each Parse returns its own copy.
func (s SliceSchema[E]) Default(v []E) SliceSchema[E] { s.o = s.o.withDefault(v); return s }

// Refine adds a custom check on the parsed list; return FieldError for
// nested paths or FieldErrorKey for a translatable message.
func (s SliceSchema[E]) Refine(fn func([]E) error) SliceSchema[E] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the parsed list after the checks; Refine sees the result.
func (s SliceSchema[E]) Transform(fn func([]E) []E) SliceSchema[E] {
	s.o = s.o.withTransform(fn)

	return s
}

// Message overrides the message for an issue code (or a detailed key such as
// "too_small.array"), for the checks of the list as well as a missing value.
func (s SliceSchema[E]) Message(code, message string) SliceSchema[E] {
	s.o = s.o.withMessage(code, message)
	s.core.o = s.core.o.withMessage(code, message)

	return s
}

// Describe sets a description used by schema exports.
func (s SliceSchema[E]) Describe(text string) SliceSchema[E] {
	s.o = s.o.withDescribe(text)

	return s
}

// Parse implements Schema.
func (s SliceSchema[E]) Parse(in any) ([]E, error) { return parseRoot[[]E](s, in) }

// Check implements Schema.
func (s SliceSchema[E]) Check(v []E) error { return checkValue(s, v) }

// ------------------------------------------------------------------ Map

// mapCore parses objects into map[string]V for the value schema's type;
// MapSchema wraps it with static typing and mapAny serves the tag path.
type mapCore struct {
	o     opts[any] // message overrides for the issues the core reports
	value AnySchema
	min   int
	max   int
}

func newMapCore(value AnySchema) mapCore { return mapCore{value: value, min: -1, max: -1} }

func (c mapCore) goType() reflect.Type {
	return reflect.MapOf(reflect.TypeFor[string](), c.value.GoType())
}

func (c mapCore) parse(p *pctx, path string, in any) (reflect.Value, bool) {
	m, isMap := in.(map[string]any)
	if !isMap {
		c.o.issue(
			p,
			path,
			CodeInvalidType,
			CodeInvalidType,
			map[string]any{"expected": "an object", "received": typeName(in)},
		)

		return reflect.Value{}, false
	}

	before := len(p.issues)

	if c.min >= 0 && len(m) < c.min {
		c.o.issue(p, path, CodeTooSmall, "too_small.array", map[string]any{"min": c.min})
	}

	if c.max >= 0 && len(m) > c.max {
		c.o.issue(p, path, CodeTooBig, "too_big.array", map[string]any{"max": c.max})
	}

	out := reflect.MakeMapWithSize(c.goType(), len(m))

	for _, k := range sortedKeys(m) {
		v, set, ok := c.value.parseAny(p, joinPath(path, k), m[k], true)
		if !ok {
			continue
		}

		if set {
			out.SetMapIndex(reflect.ValueOf(k), reflect.ValueOf(v))
		} else {
			out.SetMapIndex(reflect.ValueOf(k), reflect.Zero(c.value.GoType()))
		}
	}

	return out, len(p.issues) == before
}

// MapSchema parses objects into map[string]V where V is the value schema's
// type.
type MapSchema[V any] struct {
	core mapCore
	o    opts[map[string]V]
}

// Map returns a schema for an object with string keys and values matching
// value. The value type is inferred: Map(Int()) is a Schema[map[string]int].
func Map[V any](value Schema[V]) MapSchema[V] { return MapSchema[V]{core: newMapCore(value)} }

// GoType implements AnySchema.
func (s MapSchema[V]) GoType() reflect.Type { return reflect.TypeFor[map[string]V]() }

func (s MapSchema[V]) optional() bool { return s.o.optional || s.o.hasDef }

func (s MapSchema[V]) asOptional() AnySchema { return s.Optional() }

func (s MapSchema[V]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (map[string]V, bool) {
		rv, ok := s.core.parse(c, path, in)
		if !ok {
			return nil, false
		}

		out, _ := reflect.TypeAssert[map[string]V](rv)

		return out, true
	})

	if set && s.o.hasDef && (!present || in == nil) {
		v = maps.Clone(v) // the default is the schema's: never hand it out
	}

	return v, set, ok
}

// Min requires at least n keys.
func (s MapSchema[V]) Min(n int) MapSchema[V] { s.core.min = n; return s }

// Max allows at most n keys.
func (s MapSchema[V]) Max(n int) MapSchema[V] { s.core.max = n; return s }

// Optional allows the key to be absent.
func (s MapSchema[V]) Optional() MapSchema[V] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s MapSchema[V]) Nullable() MapSchema[V] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent; each Parse returns its own copy.
func (s MapSchema[V]) Default(v map[string]V) MapSchema[V] { s.o = s.o.withDefault(v); return s }

// Refine adds a custom check on the parsed map; return FieldError for
// nested paths or FieldErrorKey for a translatable message.
func (s MapSchema[V]) Refine(fn func(map[string]V) error) MapSchema[V] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the parsed map after the checks; Refine sees the result.
func (s MapSchema[V]) Transform(fn func(map[string]V) map[string]V) MapSchema[V] {
	s.o = s.o.withTransform(fn)

	return s
}

// Message overrides the message for an issue code (or a detailed key), for
// the checks of the map as well as a missing value.
func (s MapSchema[V]) Message(code, message string) MapSchema[V] {
	s.o = s.o.withMessage(code, message)
	s.core.o = s.core.o.withMessage(code, message)

	return s
}

// Describe sets a description used by schema exports.
func (s MapSchema[V]) Describe(text string) MapSchema[V] {
	s.o = s.o.withDescribe(text)

	return s
}

// Parse implements Schema.
func (s MapSchema[V]) Parse(in any) (map[string]V, error) { return parseRoot[map[string]V](s, in) }

// Check implements Schema.
func (s MapSchema[V]) Check(v map[string]V) error { return checkValue(s, v) }

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
	// passthrough keeps the keys outside the shape for StructSchema.Passthrough.
	passthrough bool
	// emptyAsAbsent reads an empty string as a missing key (EmptyAsAbsent);
	// bail stops each field at its first issue (Bail).
	emptyAsAbsent, bail bool
	rules               []structRule // cross-field rules, run after the fields
	known               []string     // keys read by a rule outside the shape (<key>_confirmation)
	o                   opts[any]
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

		sf, ok := fitField(key, f.Index, schema, f.Type)
		if !ok {
			panic(
				fmt.Sprintf(
					"z: %s.%s is %s but the schema for %q produces %s",
					rt,
					f.Name,
					f.Type,
					key,
					schema.GoType(),
				),
			)
		}

		core.fields = append(core.fields, sf)
	}

	return core
}

// fitField describes how a value of schema's type is stored in a field of
// type ft: as is, behind a pointer, or converted to a named type of the same
// kind. ok is false when it does not fit.
func fitField(key string, index []int, schema AnySchema, ft reflect.Type) (structField, bool) {
	st := schema.GoType()
	sf := structField{key: key, index: index, schema: schema, ftype: ft}

	switch {
	case st.AssignableTo(ft):
	case ft.Kind() == reflect.Pointer && st.AssignableTo(ft.Elem()):
		sf.ptr = true
	case st.ConvertibleTo(ft) && st.Kind() == ft.Kind():
		sf.conv = true
	case ft.Kind() == reflect.Pointer && st.ConvertibleTo(ft.Elem()) && st.Kind() == ft.Elem().Kind():
		sf.ptr, sf.conv = true, true
	default:
		return structField{}, false
	}

	return sf, true
}

// store sets the field of out that f describes to v, a value of the
// schema's type.
func (f structField) store(out reflect.Value, v any) {
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

// unknownKeys returns the entries of m that no field of the shape reads.
func (s structCore) unknownKeys(m map[string]any) map[string]any {
	known := make(map[string]bool, len(s.fields)+len(s.known))
	for _, f := range s.fields {
		known[f.key] = true
	}

	for _, k := range s.known {
		known[k] = true
	}

	extra := map[string]any{}

	for k, v := range m {
		if !known[k] {
			extra[k] = v
		}
	}

	return extra
}

func (s structCore) parse(c *pctx, path string, in any) (reflect.Value, bool) {
	return s.parseFrom(c, path, in, false)
}

// parseFrom is parse; fromTyped says the input was built from a value of the
// struct.
func (s structCore) parseFrom(c *pctx, path string, in any, fromTyped bool) (reflect.Value, bool) {
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

	if s.bail {
		defer func(prev bool) { c.bail = prev }(c.bail)

		c.bail = true
	}

	var bad map[string]bool

	for _, f := range s.fields {
		raw, present := m[f.key]

		if str, isStr := raw.(string); s.emptyAsAbsent && isStr && str == "" {
			present = false
		}

		issues := len(c.issues)

		v, set, ok := f.schema.parseAny(c, joinPath(path, f.key), raw, present)
		if !ok || len(c.issues) > issues {
			if bad == nil {
				bad = map[string]bool{}
			}

			bad[f.key] = true
		}

		if !ok || !set {
			continue
		}

		f.store(out, v)
	}

	// Typed data is a value of the struct (Check, or Parse given a T), not
	// a decoded request: what was "sent" cannot be known.
	typed := fromTyped || !c.coerce

	for _, rule := range s.rules {
		if typed && rule.needsInput {
			continue
		}

		rule.run(c, path, m, out, bad, typed)
	}

	if s.strict {
		known := make(map[string]bool, len(s.fields)+len(s.known))
		for _, f := range s.fields {
			known[f.key] = true
		}

		for _, k := range s.known {
			known[k] = true
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
	core  structCore
	o     opts[T]
	extra func(dst *T, extra map[string]any) // Passthrough
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

func (s StructSchema[T]) asOptional() AnySchema { return s.Optional() }

func (s StructSchema[T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (T, bool) {
		fromTyped := false
		if t, isT := in.(T); isT {
			in, fromTyped = toMap(t), true
		}

		rv, ok := s.core.parseFrom(c, path, in, fromTyped)
		if !ok {
			var zero T
			return zero, false
		}

		out, _ := reflect.TypeAssert[T](rv)

		if s.extra != nil {
			m, _ := in.(map[string]any)
			s.extra(&out, s.core.unknownKeys(m))
		}

		return out, true
	})

	return v, set, ok
}

// Strict rejects keys that are not in the shape. It panics after
// [StructSchema.Passthrough], which keeps them instead.
func (s StructSchema[T]) Strict() StructSchema[T] {
	if s.core.passthrough {
		panic("z: Strict cannot be combined with Passthrough")
	}

	s.core.strict = true

	return s
}

// Passthrough hands the keys that are not in the shape to fn after the
// fields are parsed, instead of dropping them: dst is the parsed value and
// extra holds the unknown keys with their decoded input. extra is NOT
// validated and is untrusted input, aliased from the request (the map is
// fresh for each Parse, the values are not copied): copy it, and validate it,
// before storing or acting on it. It panics when called after
// [StructSchema.Strict], and when fn is nil.
func (s StructSchema[T]) Passthrough(fn func(dst *T, extra map[string]any)) StructSchema[T] {
	if fn == nil {
		panic("z: Passthrough needs a function")
	}

	if s.core.strict {
		panic("z: Passthrough cannot be combined with Strict")
	}

	s.core.passthrough = true
	s.extra = fn

	return s
}

// Optional allows the key to be absent.
func (s StructSchema[T]) Optional() StructSchema[T] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s StructSchema[T]) Nullable() StructSchema[T] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent. The value is returned as is, not
// parsed, and shared between calls: do not mutate it.
func (s StructSchema[T]) Default(v T) StructSchema[T] {
	s.o = s.o.withDefault(v)

	return s
}

// Refine adds a cross-field check; return FieldError to point at a field,
// or FieldErrorKey for a translatable message.
func (s StructSchema[T]) Refine(fn func(T) error) StructSchema[T] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the parsed value.
func (s StructSchema[T]) Transform(fn func(T) T) StructSchema[T] {
	s.o = s.o.withTransform(fn)

	return s
}

// Describe sets a description used by schema exports.
func (s StructSchema[T]) Describe(text string) StructSchema[T] {
	s.o = s.o.withDescribe(text)
	return s
}

// Message overrides the message for an issue code or detailed key. It
// applies to the issues of the shape itself (a value that is not an object)
// as well as to a missing value.
func (s StructSchema[T]) Message(code, message string) StructSchema[T] {
	s.o = s.o.withMessage(code, message)
	s.core.o = s.core.o.withMessage(code, message)

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

// Check implements Schema. It validates v as it stands: the pre-steps run
// inside the check, but nothing is returned, so Trim, Default and Transform
// do not change v. Use [StructSchema.Normalize] to get the transformed value.
func (s StructSchema[T]) Check(v T) error { return checkValue(s, v) }

// Normalize validates v like Parse does for decoded input and returns the
// result with the pre-steps (Trim, ToLower, Preprocess), defaults and
// transforms applied. v goes through its JSON form, so fields tagged
// omitempty or "-" are seen as absent.
func (s StructSchema[T]) Normalize(v T) (T, error) {
	in, err := normalizeValue(v)
	if err != nil {
		var zero T

		return zero, fmt.Errorf("z: normalize: %w", err)
	}

	return parseRoot[T](s, in)
}

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
// becomes an issue with its message ([FieldError] and [FieldErrorKey] are
// honored as in Refine).
func Custom[T any](fn func(in any) (T, error)) CustomSchema[T] { return CustomSchema[T]{fn: fn} }

// GoType implements AnySchema.
func (CustomSchema[T]) GoType() reflect.Type { return reflect.TypeFor[T]() }

func (s CustomSchema[T]) optional() bool { return s.o.optional || s.o.hasDef }

func (s CustomSchema[T]) asOptional() AnySchema { return s.Optional() }

func (s CustomSchema[T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, true, func(c *pctx, path string, in any) (T, bool) {
		v, err := s.fn(in)
		if err != nil {
			c.addErr(path, err)
			return v, false
		}

		return v, true
	})

	return v, set, ok
}

// Optional allows the key to be absent.
func (s CustomSchema[T]) Optional() CustomSchema[T] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s CustomSchema[T]) Nullable() CustomSchema[T] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent.
func (s CustomSchema[T]) Default(v T) CustomSchema[T] {
	s.o = s.o.withDefault(v)

	return s
}

// Refine adds a custom check on the value fn returned; return FieldError for
// nested paths or FieldErrorKey for a translatable message.
func (s CustomSchema[T]) Refine(fn func(T) error) CustomSchema[T] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the value fn returned.
func (s CustomSchema[T]) Transform(fn func(T) T) CustomSchema[T] {
	s.o = s.o.withTransform(fn)

	return s
}

// Message overrides the message for an issue code, e.g. "required".
func (s CustomSchema[T]) Message(code, message string) CustomSchema[T] {
	s.o = s.o.withMessage(code, message)

	return s
}

// Describe sets a description used by schema exports.
func (s CustomSchema[T]) Describe(text string) CustomSchema[T] {
	s.o = s.o.withDescribe(text)

	return s
}

// Parse implements Schema.
func (s CustomSchema[T]) Parse(in any) (T, error) { return parseRoot[T](s, in) }

// Check implements Schema.
func (s CustomSchema[T]) Check(v T) error { return checkValue(s, v) }
