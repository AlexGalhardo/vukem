package z

import (
	"fmt"
	"reflect"
	"sync"
)

// lazyResolver builds the schema of a LazySchema on first use. It is shared
// by every copy of the LazySchema, so fn runs once; get is a sync.OnceValue,
// which gives the same panic to every later caller when fn panicked.
type lazyResolver[T any] struct {
	get func() Schema[T]
}

// LazySchema defers building its schema until it is first used.
type LazySchema[T any] struct {
	o opts[T]
	r *lazyResolver[T]
}

// Lazy defers building a schema until it is first used, so a schema can
// refer to itself (a comment with replies, a tree). fn is called once, on
// the first Parse, Check or JSONSchema. It panics when fn is nil.
//
// Because fn runs late, a mistake in the schema it builds (a [Struct] shape
// with a key that has no field, which panics at boot for any other schema)
// surfaces at the first use instead, and then on every later use with the same
// panic: exercise the schema in a test. The depth of a recursive input is
// bounded: past 1000 nested Lazy schemas Parse reports a too_deep issue, so a
// map that contains itself cannot overflow the stack.
//
// The repository forbids package-level state set at init, so build the
// schema in a function and close over a variable that holds it:
//
//	type Comment struct {
//		Body    string    `json:"body"`
//		Replies []Comment `json:"replies"`
//	}
//
//	func NewCommentSchema() z.Schema[Comment] {
//		var self z.Schema[Comment]
//		self = z.Struct[Comment](z.Shape{
//			"body":    z.String().Min(1),
//			"replies": z.Slice(z.Lazy(func() z.Schema[Comment] { return self })).Optional(),
//		})
//
//		return self
//	}
//
// Modifiers belong on the Lazy itself: it does not look inside the schema fn
// returns to decide whether a key may be absent. The depth of the input is
// limited by the JSON decoder, so Lazy adds no limit of its own. In
// [JSONSchema] a Lazy schema becomes a $ref to an entry of $defs named after
// its Go type, which is what lets a recursive schema be exported.
func Lazy[T any](fn func() Schema[T]) LazySchema[T] {
	if fn == nil {
		panic("z: Lazy needs a function")
	}

	return LazySchema[T]{r: &lazyResolver[T]{get: sync.OnceValue(fn)}}
}

// GoType implements AnySchema without resolving the schema.
func (LazySchema[T]) GoType() reflect.Type { return reflect.TypeFor[T]() }

func (s LazySchema[T]) optional() bool { return s.o.optional || s.o.hasDef }

func (s LazySchema[T]) asOptional() AnySchema { return s.Optional() }

func (s LazySchema[T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (T, bool) {
		var zero T

		// Input can nest without end (a map that contains itself): stop
		// before the stack does.
		if c.depth >= maxLazyDepth {
			s.o.issue(c, path, CodeTooDeep, CodeTooDeep, nil)

			return zero, false
		}

		c.depth++
		defer func() { c.depth-- }()

		v, set, ok := s.r.get().parseAny(c, path, in, true)
		if !ok || !set {
			return zero, false
		}

		out, _ := v.(T)

		return out, true
	})

	return v, set, ok
}

// Optional allows the key to be absent.
func (s LazySchema[T]) Optional() LazySchema[T] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s LazySchema[T]) Nullable() LazySchema[T] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent.
func (s LazySchema[T]) Default(v T) LazySchema[T] { s.o = s.o.withDefault(v); return s }

// Refine adds a custom check on the parsed value.
func (s LazySchema[T]) Refine(fn func(T) error) LazySchema[T] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the parsed value.
func (s LazySchema[T]) Transform(fn func(T) T) LazySchema[T] {
	s.o = s.o.withTransform(fn)

	return s
}

// Message overrides the message for an issue code.
func (s LazySchema[T]) Message(code, message string) LazySchema[T] {
	s.o = s.o.withMessage(code, message)

	return s
}

// Describe sets a description used by schema exports.
func (s LazySchema[T]) Describe(text string) LazySchema[T] {
	s.o = s.o.withDescribe(text)

	return s
}

// Parse implements Schema.
func (s LazySchema[T]) Parse(in any) (T, error) { return parseRoot[T](s, in) }

// Check implements Schema.
func (s LazySchema[T]) Check(v T) error { return checkValue(s, v) }

// jsonSchema registers the resolved schema in $defs and returns a $ref to it.
// The name is reserved before the schema is built, so a Lazy met again while
// building it (the recursion) finds the name and stops there.
func (s LazySchema[T]) jsonSchema(jc *jsctx) map[string]any {
	name, known := jc.names[s.r]
	if !known {
		name = jc.defName(reflect.TypeFor[T]().Name())

		if jc.names == nil {
			jc.names = map[any]string{}
			jc.defs = map[string]any{}
		}

		jc.names[s.r] = name
		jc.defs[name] = map[string]any{}
		jc.defs[name] = jc.of(s.r.get())
	}

	ref := map[string]any{"$ref": "#/$defs/" + name}
	if s.o.nullable {
		ref = map[string]any{"anyOf": []any{ref, map[string]any{"type": "null"}}}
	}

	o := s.o
	o.nullable = false // said by the null member

	return o.decorate(ref, "")
}

// defName picks the $defs name for a Lazy schema of the Go type named base: the
// type's own name unless taken (or empty, as for an unnamed type), then
// Lazy1, Lazy2... in the order the schemas are met.
func (jc *jsctx) defName(base string) string {
	if _, taken := jc.defs[base]; base != "" && !taken {
		return base
	}

	for i := 1; ; i++ {
		if name := fmt.Sprintf("Lazy%d", i); jc.defs[name] == nil {
			return name
		}
	}
}
