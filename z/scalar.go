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

func (s BoolSchema) asOptional() AnySchema { return s.Optional() }

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
func (s BoolSchema) Optional() BoolSchema { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s BoolSchema) Nullable() BoolSchema { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent.
func (s BoolSchema) Default(v bool) BoolSchema {
	s.o = s.o.withDefault(v)
	return s
}

// Refine adds a custom check.
func (s BoolSchema) Refine(fn func(bool) error) BoolSchema {
	s.o = s.o.withRefine(fn)

	return s
}

// Message overrides the message for an issue code.
func (s BoolSchema) Message(code, message string) BoolSchema {
	s.o = s.o.withMessage(code, message)

	return s
}

// Transform maps the parsed value.
func (s BoolSchema) Transform(fn func(bool) bool) BoolSchema {
	s.o = s.o.withTransform(fn)

	return s
}

// Describe sets a description used by schema exports.
func (s BoolSchema) Describe(text string) BoolSchema { s.o = s.o.withDescribe(text); return s }

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
	// afterEq and beforeEq are the inclusive bounds; future and past compare
	// with the clock at parse time.
	afterEq, beforeEq      *time.Time
	futureClock, pastClock func() time.Time
}

var defaultLayouts = []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02T15:04", time.DateOnly}

// Time returns a schema for time.Time.
func Time() TimeSchema { return TimeSchema{layouts: defaultLayouts} }

// GoType implements AnySchema.
func (TimeSchema) GoType() reflect.Type { return reflect.TypeFor[time.Time]() }

func (s TimeSchema) optional() bool { return s.o.optional || s.o.hasDef }

func (s TimeSchema) asOptional() AnySchema { return s.Optional() }

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

		s.checkInclusive(c, path, t)

		return t, len(c.issues) == before
	})

	return v, set, ok
}

// checkInclusive runs the bounds that accept their own instant and the ones
// relative to the clock.
func (s TimeSchema) checkInclusive(c *pctx, path string, t time.Time) {
	if s.afterEq != nil && t.Before(*s.afterEq) {
		s.o.issue(c, path, CodeTooSmall, "too_small.date_inclusive",
			map[string]any{"min": s.afterEq.Format(time.RFC3339)})
	}

	if s.beforeEq != nil && t.After(*s.beforeEq) {
		s.o.issue(c, path, CodeTooBig, "too_big.date_inclusive",
			map[string]any{"max": s.beforeEq.Format(time.RFC3339)})
	}

	if s.futureClock != nil && !t.After(s.futureClock()) {
		s.o.issue(c, path, CodeTooSmall, "date.future", nil)
	}

	if s.pastClock != nil && !t.Before(s.pastClock()) {
		s.o.issue(c, path, CodeTooBig, "date.past", nil)
	}
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

// AfterOrEqual requires the time to be t or later.
func (s TimeSchema) AfterOrEqual(t time.Time) TimeSchema {
	s.o = s.o.clone()
	s.afterEq = &t

	return s
}

// BeforeOrEqual requires the time to be t or earlier.
func (s TimeSchema) BeforeOrEqual(t time.Time) TimeSchema {
	s.o = s.o.clone()
	s.beforeEq = &t

	return s
}

// Future requires the time to be after the moment of the parse. It reads
// time.Now each time, so a schema built once stays correct; instants are
// compared, so the zone of the input does not matter. Use
// [TimeSchema.FutureAt] to control the clock, in a test or with a clock the
// app injects.
func (s TimeSchema) Future() TimeSchema { return s.FutureAt(time.Now) }

// FutureAt is [TimeSchema.Future] with the clock now instead of time.Now. It
// panics when now is nil.
func (s TimeSchema) FutureAt(now func() time.Time) TimeSchema {
	if now == nil {
		panic("z: FutureAt needs a clock")
	}

	s.o = s.o.clone()
	s.futureClock = now

	return s
}

// Past requires the time to be before the moment of the parse, like
// [TimeSchema.Future].
func (s TimeSchema) Past() TimeSchema { return s.PastAt(time.Now) }

// PastAt is [TimeSchema.Past] with the clock now instead of time.Now. It
// panics when now is nil.
func (s TimeSchema) PastAt(now func() time.Time) TimeSchema {
	if now == nil {
		panic("z: PastAt needs a clock")
	}

	s.o = s.o.clone()
	s.pastClock = now

	return s
}

// Optional allows the key to be absent.
func (s TimeSchema) Optional() TimeSchema { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s TimeSchema) Nullable() TimeSchema { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent.
func (s TimeSchema) Default(v time.Time) TimeSchema {
	s.o = s.o.withDefault(v)

	return s
}

// Refine adds a custom check.
func (s TimeSchema) Refine(fn func(time.Time) error) TimeSchema {
	s.o = s.o.withRefine(fn)

	return s
}

// Message overrides the message for an issue code.
func (s TimeSchema) Message(code, message string) TimeSchema {
	s.o = s.o.withMessage(code, message)

	return s
}

// Transform maps the parsed value, e.g. to UTC.
func (s TimeSchema) Transform(fn func(time.Time) time.Time) TimeSchema {
	s.o = s.o.withTransform(fn)

	return s
}

// Describe sets a description used by schema exports.
func (s TimeSchema) Describe(text string) TimeSchema { s.o = s.o.withDescribe(text); return s }

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

func (s EnumSchema[T]) asOptional() AnySchema { return s.Optional() }

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
func (s EnumSchema[T]) Optional() EnumSchema[T] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s EnumSchema[T]) Nullable() EnumSchema[T] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent.
func (s EnumSchema[T]) Default(v T) EnumSchema[T] {
	s.o = s.o.withDefault(v)
	return s
}

// Refine adds a custom check.
func (s EnumSchema[T]) Refine(fn func(T) error) EnumSchema[T] {
	s.o = s.o.withRefine(fn)

	return s
}

// Message overrides the message for an issue code.
func (s EnumSchema[T]) Message(code, message string) EnumSchema[T] {
	s.o = s.o.withMessage(code, message)

	return s
}

// Transform maps the parsed value.
func (s EnumSchema[T]) Transform(fn func(T) T) EnumSchema[T] {
	s.o = s.o.withTransform(fn)

	return s
}

// Describe sets a description used by schema exports.
func (s EnumSchema[T]) Describe(text string) EnumSchema[T] {
	s.o = s.o.withDescribe(text)
	return s
}

// Parse implements Schema.
func (s EnumSchema[T]) Parse(in any) (T, error) { return parseRoot[T](s, in) }

// Check implements Schema.
func (s EnumSchema[T]) Check(v T) error { return checkValue(s, v) }
