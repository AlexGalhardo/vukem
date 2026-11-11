package z

import (
	"fmt"
	"reflect"
)

// BrandSchema converts the value a schema produces to a defined type.
type BrandSchema[B, T any] struct {
	o     opts[B]
	inner Schema[T]
}

// Brand returns a schema producing the defined type B from inner, so a value
// of the domain ("a user id") cannot be mixed up with its plain type:
//
//	type UserID int64
//
//	var UserIDSchema = z.Brand[UserID](z.Int64().Positive())
//	id, err := UserIDSchema.Parse("42") // UserID(42)
//
// B's underlying type must be T's (same kind, convertible); it panics
// otherwise, at boot. The conversion is Go's, with no cost at run time. The
// rules of inner apply, and so do its issues; modifiers (Optional, Default)
// go on the brand. The JSON Schema is inner's.
func Brand[B, T any](inner Schema[T]) BrandSchema[B, T] {
	from, to := reflect.TypeFor[T](), reflect.TypeFor[B]()
	if from.Kind() != to.Kind() || !from.ConvertibleTo(to) {
		panic(fmt.Sprintf("z: Brand: %s is not a defined type over %s", to, from))
	}

	return BrandSchema[B, T]{inner: inner}
}

// GoType implements AnySchema.
func (BrandSchema[B, T]) GoType() reflect.Type { return reflect.TypeFor[B]() }

func (s BrandSchema[B, T]) optional() bool { return s.o.optional || s.o.hasDef }

func (s BrandSchema[B, T]) asOptional() AnySchema { return s.Optional() }

func (s BrandSchema[B, T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (B, bool) {
		var zero B

		inner, set, ok := s.inner.parseAny(c, path, in, true)
		if !ok || !set {
			return zero, false
		}

		out, _ := reflect.TypeAssert[B](reflect.ValueOf(inner).Convert(reflect.TypeFor[B]()))

		return out, true
	})

	return v, set, ok
}

// Optional allows the key to be absent.
func (s BrandSchema[B, T]) Optional() BrandSchema[B, T] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s BrandSchema[B, T]) Nullable() BrandSchema[B, T] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent.
func (s BrandSchema[B, T]) Default(v B) BrandSchema[B, T] { s.o = s.o.withDefault(v); return s }

// Refine adds a custom check on the converted value.
func (s BrandSchema[B, T]) Refine(fn func(B) error) BrandSchema[B, T] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the converted value.
func (s BrandSchema[B, T]) Transform(fn func(B) B) BrandSchema[B, T] {
	s.o = s.o.withTransform(fn)

	return s
}

// Message overrides the message for an issue code of the brand itself.
func (s BrandSchema[B, T]) Message(code, message string) BrandSchema[B, T] {
	s.o = s.o.withMessage(code, message)

	return s
}

// Describe sets a description used by schema exports.
func (s BrandSchema[B, T]) Describe(text string) BrandSchema[B, T] {
	s.o = s.o.withDescribe(text)

	return s
}

// Parse implements Schema.
func (s BrandSchema[B, T]) Parse(in any) (B, error) { return parseRoot[B](s, in) }

// Check implements Schema: v is checked by inner as a T.
func (s BrandSchema[B, T]) Check(v B) error {
	plain, _ := reflect.TypeAssert[T](reflect.ValueOf(v).Convert(reflect.TypeFor[T]()))

	return s.inner.Check(plain)
}

func (s BrandSchema[B, T]) jsonSchema(jc *jsctx) map[string]any {
	m := jc.of(s.inner)
	if s.o.desc != "" {
		m["description"] = s.o.desc
	}

	return m
}
