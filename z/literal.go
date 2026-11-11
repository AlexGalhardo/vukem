package z

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
)

// LiteralValue is the set of types [Literal] accepts.
type LiteralValue interface {
	~string | ~bool | ~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 | ~float32 | ~float64
}

// LiteralSchema accepts exactly one of a fixed set of values of type T.
type LiteralSchema[T LiteralValue] struct {
	o      opts[T]
	values []T
}

// Literal returns a schema accepting exactly one of values, such as a
// version tag ("v1"), a flag (true) or a code (42). From forms, numbers and
// booleans are read from text like [Int] and [Bool] do. Literal panics when
// values is empty.
func Literal[T LiteralValue](values ...T) LiteralSchema[T] {
	if len(values) == 0 {
		panic("z: Literal needs at least one value")
	}

	return LiteralSchema[T]{values: slices.Clone(values)}
}

// GoType implements AnySchema.
func (LiteralSchema[T]) GoType() reflect.Type { return reflect.TypeFor[T]() }

func (s LiteralSchema[T]) optional() bool { return s.o.optional || s.o.hasDef }

func (s LiteralSchema[T]) asOptional() AnySchema { return s.Optional() }

// Values returns the accepted values.
func (s LiteralSchema[T]) Values() []T { return slices.Clone(s.values) }

func (s LiteralSchema[T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, true, func(c *pctx, path string, in any) (T, bool) {
		if v, ok := readLiteral[T](in, c.coerce); ok && slices.Contains(s.values, v) {
			return v, true
		}

		expected := make([]string, len(s.values))
		for i, v := range s.values {
			expected[i] = fmt.Sprint(v)
		}

		s.o.issue(
			c,
			path,
			CodeInvalidLiteral,
			CodeInvalidLiteral,
			map[string]any{"expected": strings.Join(expected, ", ")},
		)

		var zero T

		return zero, false
	})

	return v, set, ok
}

// readLiteral converts in to T the way the schema of T's kind would:
// numbers without loss, text only when coercing (forms, query strings).
func readLiteral[T LiteralValue](in any, coerce bool) (T, bool) {
	var zero T

	rt := reflect.TypeFor[T]()
	out := reflect.New(rt).Elem()

	switch k := rt.Kind(); {
	case k == reflect.String:
		rv := reflect.ValueOf(in)
		if !rv.IsValid() || rv.Kind() != reflect.String {
			return zero, false
		}

		out.SetString(rv.String())
	case k == reflect.Bool:
		b, ok := readBool(in, coerce)
		if !ok {
			return zero, false
		}

		out.SetBool(b)
	case k == reflect.Float32 || k == reflect.Float64:
		f, ok := toNumber[float64](in, coerce)
		if !ok {
			return zero, false
		}

		out.SetFloat(f)
	case k >= reflect.Uint && k <= reflect.Uint64:
		u, ok := toNumber[uint64](in, coerce)
		if !ok || out.OverflowUint(u) {
			return zero, false
		}

		out.SetUint(u)
	default:
		i, ok := toNumber[int64](in, coerce)
		if !ok || out.OverflowInt(i) {
			return zero, false
		}

		out.SetInt(i)
	}

	v, _ := reflect.TypeAssert[T](out)

	return v, true
}

// readBool is the boolean reading of [BoolSchema].
func readBool(in any, coerce bool) (bool, bool) {
	switch x := in.(type) {
	case bool:
		return x, true
	case string:
		if coerce {
			switch strings.ToLower(strings.TrimSpace(x)) {
			case "true", "1", "on", "yes":
				return true, true
			case "false", "0", "off", "no":
				return false, true
			}
		}
	}

	return false, false
}

// Optional allows the key to be absent.
func (s LiteralSchema[T]) Optional() LiteralSchema[T] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s LiteralSchema[T]) Nullable() LiteralSchema[T] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent.
func (s LiteralSchema[T]) Default(v T) LiteralSchema[T] { s.o = s.o.withDefault(v); return s }

// Refine adds a custom check.
func (s LiteralSchema[T]) Refine(fn func(T) error) LiteralSchema[T] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the parsed value.
func (s LiteralSchema[T]) Transform(fn func(T) T) LiteralSchema[T] {
	s.o = s.o.withTransform(fn)

	return s
}

// Message overrides the message for an issue code, e.g. "invalid_literal".
func (s LiteralSchema[T]) Message(code, message string) LiteralSchema[T] {
	s.o = s.o.withMessage(code, message)

	return s
}

// Describe sets a description used by schema exports.
func (s LiteralSchema[T]) Describe(text string) LiteralSchema[T] {
	s.o = s.o.withDescribe(text)

	return s
}

// Parse implements Schema.
func (s LiteralSchema[T]) Parse(in any) (T, error) { return parseRoot[T](s, in) }

// Check implements Schema.
func (s LiteralSchema[T]) Check(v T) error { return checkValue(s, v) }

func (s LiteralSchema[T]) jsonSchema(*jsctx) map[string]any {
	m := map[string]any{}

	if len(s.values) == 1 {
		m["const"] = s.values[0]
	} else {
		enum := make([]any, len(s.values))
		for i, v := range s.values {
			enum[i] = v
		}

		m["enum"] = enum
	}

	return s.o.decorate(m, literalType(reflect.TypeFor[T]().Kind()))
}

func literalType(k reflect.Kind) string {
	switch k {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Float32, reflect.Float64:
		return "number"
	default:
		return "integer"
	}
}
