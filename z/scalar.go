package z

import (
	"reflect"
	"slices"
	"strings"
	"time"
)

// BoolSchema parses booleans. From forms it accepts true/false, 1/0,
// on/off and yes/no (an unchecked checkbox is absent, so use Default(false)).
type BoolSchema struct {
	o opts[bool]
}

// Bool returns a schema for bool.
func Bool() BoolSchema { return BoolSchema{} }

// GoType implements AnySchema.
func (BoolSchema) GoType() reflect.Type { return reflect.TypeFor[bool]() }

func (s BoolSchema) optional() bool { return s.o.optional || s.o.hasDef }

func (s BoolSchema) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, true, func(c *pctx, path string, in any) (bool, bool) {
		switch x := in.(type) {
		case bool:
			return x, true
		case string:
			if c.coerce {
				switch strings.ToLower(strings.TrimSpace(x)) {
				case "true", "1", "on", "yes":
					return true, true
				case "false", "0", "off", "no":
					return false, true
				}
			}
		}

		s.o.issue(
			c,
			path,
			CodeInvalidType,
			CodeInvalidType,
			map[string]any{"expected": "true or false", "received": typeName(in)},
		)

		return false, false
	})

	return v, set, ok
}

// Optional allows the key to be absent.
func (s BoolSchema) Optional() BoolSchema { s.o = s.o.clone(); s.o.optional = true; return s }

// Nullable allows null.
func (s BoolSchema) Nullable() BoolSchema { s.o = s.o.clone(); s.o.nullable = true; return s }

// Default uses v when the key is absent.
func (s BoolSchema) Default(v bool) BoolSchema {
	s.o = s.o.clone()
	s.o.hasDef, s.o.def = true, v
	return s
}

// Refine adds a custom check.
func (s BoolSchema) Refine(fn func(bool) error) BoolSchema {
	s.o = s.o.clone()
	s.o.refines = append(s.o.refines, fn)

	return s
}

// Message overrides the message for an issue code.
func (s BoolSchema) Message(code, message string) BoolSchema {
	s.o = s.o.clone()
	if s.o.messages == nil {
		s.o.messages = map[string]string{}
	}

	s.o.messages[code] = message

	return s
}

// Describe sets a description used by schema exports.
func (s BoolSchema) Describe(text string) BoolSchema { s.o = s.o.clone(); s.o.desc = text; return s }

// Parse implements Schema.
func (s BoolSchema) Parse(in any) (bool, error) { return parseRoot[bool](s, in) }

// Check implements Schema.
func (s BoolSchema) Check(v bool) error { return checkValue(s, v) }

// TimeSchema parses times from RFC 3339 strings (plus the HTML
// datetime-local and date formats by default).
type TimeSchema struct {
	o       opts[time.Time]
	layouts []string
	after   *time.Time
	before  *time.Time
}

var defaultLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", time.DateOnly}

// Time returns a schema for time.Time.
func Time() TimeSchema { return TimeSchema{layouts: defaultLayouts} }

// GoType implements AnySchema.
func (TimeSchema) GoType() reflect.Type { return reflect.TypeFor[time.Time]() }

func (s TimeSchema) optional() bool { return s.o.optional || s.o.hasDef }

func (s TimeSchema) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, true, func(c *pctx, path string, in any) (time.Time, bool) {
		var t time.Time

		switch x := in.(type) {
		case time.Time:
			t = x
		case string:
			parsed := false

			for _, l := range s.layouts {
				if pt, err := time.Parse(l, strings.TrimSpace(x)); err == nil {
					t, parsed = pt, true
					break
				}
			}

			if !parsed {
				s.o.issue(c, path, CodeInvalidDate, CodeInvalidDate, nil)
				return time.Time{}, false
			}
		default:
			s.o.issue(
				c,
				path,
				CodeInvalidType,
				CodeInvalidType,
				map[string]any{"expected": "a date", "received": typeName(in)},
			)
			return time.Time{}, false
		}

		before := len(c.issues)

		if s.after != nil && !t.After(*s.after) {
			s.o.issue(c, path, CodeTooSmall, "too_small.date", map[string]any{"min": s.after.Format(time.RFC3339)})
		}

		if s.before != nil && !t.Before(*s.before) {
			s.o.issue(c, path, CodeTooBig, "too_big.date", map[string]any{"max": s.before.Format(time.RFC3339)})
		}

		return t, len(c.issues) == before
	})

	return v, set, ok
}

// Layouts replaces the accepted layouts.
func (s TimeSchema) Layouts(layouts ...string) TimeSchema {
	s.o = s.o.clone()
	s.layouts = slices.Clone(layouts)

	return s
}

// After requires the time to be strictly after t.
func (s TimeSchema) After(t time.Time) TimeSchema { s.o = s.o.clone(); s.after = &t; return s }

// Before requires the time to be strictly before t.
func (s TimeSchema) Before(t time.Time) TimeSchema { s.o = s.o.clone(); s.before = &t; return s }

// Optional allows the key to be absent.
func (s TimeSchema) Optional() TimeSchema { s.o = s.o.clone(); s.o.optional = true; return s }

// Nullable allows null.
func (s TimeSchema) Nullable() TimeSchema { s.o = s.o.clone(); s.o.nullable = true; return s }

// Default uses v when the key is absent.
func (s TimeSchema) Default(v time.Time) TimeSchema {
	s.o = s.o.clone()
	s.o.hasDef, s.o.def = true, v

	return s
}

// Refine adds a custom check.
func (s TimeSchema) Refine(fn func(time.Time) error) TimeSchema {
	s.o = s.o.clone()
	s.o.refines = append(s.o.refines, fn)

	return s
}

// Message overrides the message for an issue code.
func (s TimeSchema) Message(code, message string) TimeSchema {
	s.o = s.o.clone()
	if s.o.messages == nil {
		s.o.messages = map[string]string{}
	}

	s.o.messages[code] = message

	return s
}

// Describe sets a description used by schema exports.
func (s TimeSchema) Describe(text string) TimeSchema { s.o = s.o.clone(); s.o.desc = text; return s }

// Parse implements Schema.
func (s TimeSchema) Parse(in any) (time.Time, error) { return parseRoot[time.Time](s, in) }

// Check implements Schema.
func (s TimeSchema) Check(v time.Time) error { return checkValue(s, v) }

// EnumSchema parses one of a fixed set of string values into T.
type EnumSchema[T ~string] struct {
	o      opts[T]
	values []T
}

// Enum returns a schema accepting one of values; it panics when values is
// empty.
func Enum(values ...string) EnumSchema[string] { return EnumOf(values...) }

// EnumOf returns an enum schema for a named string type:
//
//	type Status string
//	z.EnumOf(StatusDraft, StatusPublished)
//
// It panics when values is empty.
func EnumOf[T ~string](values ...T) EnumSchema[T] {
	if len(values) == 0 {
		panic("z: Enum needs at least one value")
	}

	return EnumSchema[T]{values: slices.Clone(values)}
}

// GoType implements AnySchema.
func (EnumSchema[T]) GoType() reflect.Type { return reflect.TypeFor[T]() }

func (s EnumSchema[T]) optional() bool { return s.o.optional || s.o.hasDef }

// Values returns the allowed values.
func (s EnumSchema[T]) Values() []T { return slices.Clone(s.values) }

func (s EnumSchema[T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, true, func(c *pctx, path string, in any) (T, bool) {
		str, isStr := in.(string)
		if !isStr {
			if rv := reflect.ValueOf(in); rv.IsValid() && rv.Kind() == reflect.String {
				str, isStr = rv.String(), true
			}
		}

		if !isStr || !slices.Contains(s.values, T(str)) {
			options := make([]string, len(s.values))
			for i, v := range s.values {
				options[i] = string(v)
			}

			s.o.issue(c, path, CodeInvalidEnum, CodeInvalidEnum, map[string]any{"options": strings.Join(options, ", ")})

			return "", false
		}

		return T(str), true
	})

	return v, set, ok
}

// Optional allows the key to be absent.
func (s EnumSchema[T]) Optional() EnumSchema[T] { s.o = s.o.clone(); s.o.optional = true; return s }

// Nullable allows null.
func (s EnumSchema[T]) Nullable() EnumSchema[T] { s.o = s.o.clone(); s.o.nullable = true; return s }

// Default uses v when the key is absent.
func (s EnumSchema[T]) Default(v T) EnumSchema[T] {
	s.o = s.o.clone()
	s.o.hasDef, s.o.def = true, v
	return s
}

// Refine adds a custom check.
func (s EnumSchema[T]) Refine(fn func(T) error) EnumSchema[T] {
	s.o = s.o.clone()
	s.o.refines = append(s.o.refines, fn)

	return s
}

// Message overrides the message for an issue code.
func (s EnumSchema[T]) Message(code, message string) EnumSchema[T] {
	s.o = s.o.clone()
	if s.o.messages == nil {
		s.o.messages = map[string]string{}
	}

	s.o.messages[code] = message

	return s
}

// Describe sets a description used by schema exports.
func (s EnumSchema[T]) Describe(text string) EnumSchema[T] {
	s.o = s.o.clone()
	s.o.desc = text
	return s
}

// Parse implements Schema.
func (s EnumSchema[T]) Parse(in any) (T, error) { return parseRoot[T](s, in) }

// Check implements Schema.
func (s EnumSchema[T]) Check(v T) error { return checkValue(s, v) }
