package z

import (
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// assignableTo panics unless the Go type of every schema fits T; it is the
// boot check shared by Union and DiscriminatedUnion.
func assignableTo[T any](what string, variants []AnySchema) {
	rt := reflect.TypeFor[T]()

	for i, v := range variants {
		if !v.GoType().AssignableTo(rt) {
			panic(fmt.Sprintf("z: %s variant %d produces %s, which is not assignable to %s", what, i, v.GoType(), rt))
		}
	}
}

// ---------------------------------------------------------------- Union

// UnionSchema accepts a value that matches one of several schemas.
type UnionSchema[T any] struct {
	o        opts[T]
	variants []AnySchema
}

// Union accepts the first of variants that parses the input without issues.
// T is the type of the result: any when the variants are primitives, or an
// interface the variants' Go types implement, which the caller narrows with
// a type switch. It panics when fewer than two variants are given or when a
// variant's Go type is not assignable to T.
//
// The order matters when the input can be coerced (a form value is text):
// Union[any](Int(), String()) turns "5" into 5, while Union[any](String(),
// Int()) keeps "5". When no variant fits, a single invalid_union issue is
// reported at the path of the union, not the issues of each variant; use
// [DiscriminatedUnion] for objects, which reports the issues of the chosen
// variant.
//
// Check reads a typed value through its JSON form and does not coerce text,
// so it can refuse what Parse accepts from a form ("5" for an Int variant).
// The Refine and Transform of a variant run on every attempt, including the
// ones that end up not matching: keep them free of side effects. Union
// attempts made inside other attempts (a union that recurses through [Lazy])
// are bounded per parse; past the bound a too_deep issue is reported.
func Union[T any](variants ...AnySchema) UnionSchema[T] {
	if len(variants) < 2 {
		panic("z: Union needs at least two variants")
	}

	assignableTo[T]("Union", variants)

	return UnionSchema[T]{variants: slices.Clone(variants)}
}

// GoType implements AnySchema.
func (UnionSchema[T]) GoType() reflect.Type { return reflect.TypeFor[T]() }

func (s UnionSchema[T]) optional() bool { return s.o.optional || s.o.hasDef }

func (s UnionSchema[T]) asOptional() AnySchema { return s.Optional() }

func (s UnionSchema[T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (T, bool) {
		var zero T

		for _, variant := range s.variants {
			if !c.spendNested() {
				s.o.issue(c, path, CodeTooDeep, CodeTooDeep, nil)

				return zero, false
			}

			// A scratch context: the issues of a variant that does not fit
			// are not the caller's business.
			try := c.fork()

			v, set, ok := variant.parseAny(try, path, in, true)
			if !ok || len(try.issues) > 0 {
				continue
			}

			if !set {
				return zero, true
			}

			out, _ := v.(T)

			return out, true
		}

		s.o.issue(c, path, CodeInvalidUnion, CodeInvalidUnion, nil)

		return zero, false
	})

	return v, set, ok
}

// Optional allows the key to be absent.
func (s UnionSchema[T]) Optional() UnionSchema[T] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s UnionSchema[T]) Nullable() UnionSchema[T] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent.
func (s UnionSchema[T]) Default(v T) UnionSchema[T] { s.o = s.o.withDefault(v); return s }

// Refine adds a custom check on the parsed value.
func (s UnionSchema[T]) Refine(fn func(T) error) UnionSchema[T] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the parsed value.
func (s UnionSchema[T]) Transform(fn func(T) T) UnionSchema[T] {
	s.o = s.o.withTransform(fn)

	return s
}

// Message overrides the message for an issue code, e.g. "invalid_union".
func (s UnionSchema[T]) Message(code, message string) UnionSchema[T] {
	s.o = s.o.withMessage(code, message)

	return s
}

// Describe sets a description used by schema exports.
func (s UnionSchema[T]) Describe(text string) UnionSchema[T] {
	s.o = s.o.withDescribe(text)

	return s
}

// Parse implements Schema.
func (s UnionSchema[T]) Parse(in any) (T, error) { return parseRoot[T](s, in) }

// Check implements Schema.
func (s UnionSchema[T]) Check(v T) error { return checkValue(s, v) }

func (s UnionSchema[T]) jsonSchema(jc *jsctx) map[string]any {
	return unionJSON(jc, "anyOf", s.variants, s.o.nullable, s.o.decorate)
}

// unionJSON lists the variants under keyword; a nullable union also accepts
// null, which is a variant of its own in JSON Schema.
func unionJSON(
	jc *jsctx,
	keyword string,
	variants []AnySchema,
	nullable bool,
	decorate func(map[string]any, string) map[string]any,
) map[string]any {
	list := make([]any, 0, len(variants)+1)
	for _, v := range variants {
		list = append(list, jc.of(v))
	}

	if nullable {
		list = append(list, map[string]any{"type": "null"})
	}

	// decorate would add the null type to a schema that has no type of its
	// own: the member above says it already.
	return decorate(map[string]any{keyword: list}, "")
}

// ------------------------------------------------- DiscriminatedUnion

// keyed is implemented by the object schemas whose shape can be asked for a
// key.
type keyed interface{ hasKey(key string) bool }

func (s StructSchema[T]) hasKey(key string) bool { return s.core.hasKey(key) }

func (s structAny) hasKey(key string) bool { return s.core.hasKey(key) }

func (s structCore) hasKey(key string) bool {
	return slices.ContainsFunc(s.fields, func(f structField) bool { return f.key == key })
}

// DiscriminatedUnionSchema accepts one of several object shapes, chosen by
// the value of one key.
type DiscriminatedUnionSchema[T any] struct {
	o        opts[T]
	key      string
	variants map[string]AnySchema
}

// DiscriminatedUnion picks the variant by the string value of key in the
// input object, then parses the whole object with it, so the issues
// reported are those of the chosen variant, with their exact paths. T is the
// type of the result, usually an interface the variants' structs implement.
// It panics when there are no variants, when a variant is not an object
// schema ([Struct] or one derived from it) whose shape has key, or when its
// Go type is not assignable to T.
//
// Include the key in every variant (a [Literal] works well) and in its JSON
// form: [DiscriminatedUnionSchema.Check] reads a typed value through JSON.
func DiscriminatedUnion[T any](key string, variants map[string]AnySchema) DiscriminatedUnionSchema[T] {
	if len(variants) == 0 {
		panic("z: DiscriminatedUnion needs at least one variant")
	}

	values := slices.Sorted(maps.Keys(variants))
	list := make([]AnySchema, len(values))

	for i, value := range values {
		list[i] = variants[value]

		k, ok := list[i].(keyed)
		if !ok || !k.hasKey(key) {
			panic(fmt.Sprintf("z: DiscriminatedUnion variant %q must be an object schema with a %q key", value, key))
		}
	}

	assignableTo[T]("DiscriminatedUnion", list)

	return DiscriminatedUnionSchema[T]{key: key, variants: maps.Clone(variants)}
}

// GoType implements AnySchema.
func (DiscriminatedUnionSchema[T]) GoType() reflect.Type { return reflect.TypeFor[T]() }

func (s DiscriminatedUnionSchema[T]) optional() bool { return s.o.optional || s.o.hasDef }

func (s DiscriminatedUnionSchema[T]) asOptional() AnySchema { return s.Optional() }

func (s DiscriminatedUnionSchema[T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (T, bool) {
		var zero T

		m, isMap := in.(map[string]any)
		if !isMap {
			s.o.issue(c, path, CodeInvalidType, CodeInvalidType,
				map[string]any{"expected": "an object", "received": typeName(in)})

			return zero, false
		}

		// A discriminator that is not a string names no variant.
		tag, _ := m[s.key].(string)

		variant, found := s.variants[tag]
		if !found {
			s.o.issue(c, joinPath(path, s.key), CodeInvalidDiscriminator, CodeInvalidDiscriminator,
				map[string]any{"options": strings.Join(slices.Sorted(maps.Keys(s.variants)), ", ")})

			return zero, false
		}

		v, set, ok := variant.parseAny(c, path, in, true)
		if !ok || !set {
			return zero, false
		}

		out, _ := v.(T)

		return out, true
	})

	return v, set, ok
}

// Optional allows the key to be absent.
func (s DiscriminatedUnionSchema[T]) Optional() DiscriminatedUnionSchema[T] {
	s.o = s.o.withOptional()

	return s
}

// Nullable allows null.
func (s DiscriminatedUnionSchema[T]) Nullable() DiscriminatedUnionSchema[T] {
	s.o = s.o.withNullable()

	return s
}

// Default uses v when the key is absent.
func (s DiscriminatedUnionSchema[T]) Default(v T) DiscriminatedUnionSchema[T] {
	s.o = s.o.withDefault(v)

	return s
}

// Refine adds a custom check on the parsed value.
func (s DiscriminatedUnionSchema[T]) Refine(fn func(T) error) DiscriminatedUnionSchema[T] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the parsed value.
func (s DiscriminatedUnionSchema[T]) Transform(fn func(T) T) DiscriminatedUnionSchema[T] {
	s.o = s.o.withTransform(fn)

	return s
}

// Message overrides the message for an issue code, e.g.
// "invalid_union_discriminator".
func (s DiscriminatedUnionSchema[T]) Message(code, message string) DiscriminatedUnionSchema[T] {
	s.o = s.o.withMessage(code, message)

	return s
}

// Describe sets a description used by schema exports.
func (s DiscriminatedUnionSchema[T]) Describe(text string) DiscriminatedUnionSchema[T] {
	s.o = s.o.withDescribe(text)

	return s
}

// Parse implements Schema.
func (s DiscriminatedUnionSchema[T]) Parse(in any) (T, error) { return parseRoot[T](s, in) }

// Check implements Schema. v goes through its JSON form, so the key must be
// serialized by the variants' structs.
func (s DiscriminatedUnionSchema[T]) Check(v T) error { return checkValue(s, v) }

func (s DiscriminatedUnionSchema[T]) jsonSchema(jc *jsctx) map[string]any {
	// In the order of the discriminator values, so the output is stable.
	variants := make([]AnySchema, 0, len(s.variants))
	for _, value := range slices.Sorted(maps.Keys(s.variants)) {
		variants = append(variants, s.variants[value])
	}

	m := unionJSON(jc, "oneOf", variants, s.o.nullable, s.o.decorate)
	// discriminator is OpenAPI 3.1; plain JSON Schema validators ignore it.
	m["discriminator"] = map[string]any{"propertyName": s.key}

	return m
}
