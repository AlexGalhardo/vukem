package z

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"time"
)

// structRule is a check on a whole object, run after its fields were read.
// raw is the input, out the parsed struct, and bad the keys of the fields
// that already reported an issue (or were skipped): a rule never piles a
// second issue on a field that is already wrong. typed is true when the data
// is not a decoded request but a value of the struct (Check, or Parse given
// a T): there is no raw input to ask whether a key was sent.
type structRule struct {
	// keys are the keys of the shape the rule cites, so a derived schema
	// (Pick, Omit, Extend, Partial) knows whether it still applies.
	keys []string
	// needsInput marks a rule that depends on whether a key was sent in the
	// raw input; it is skipped for typed data, where a zero value and a
	// missing one cannot be told apart.
	needsInput bool
	// confirmation is the key Confirmed reads as <key>_confirmation; Strict
	// and Passthrough do not treat it as unknown.
	confirmation string
	run          func(c *pctx, path string, raw map[string]any, out reflect.Value, bad map[string]bool, typed bool)
	// rebuild makes the same rule for another core (a derived schema); nil
	// for a rule that cannot be remade (RequiredWhen holds a func of T).
	rebuild func(s structCore) structRule
}

// with returns a copy of s with rule appended; the rules slice is never
// shared between schemas.
func (s structCore) with(rule structRule) structCore {
	s.rules = append(slices.Clip(s.rules), rule)

	if rule.confirmation != "" {
		s.known = append(slices.Clip(s.known), rule.confirmation)
	}

	return s
}

// withDerived returns s with the rules of from that still apply: a rule is
// kept, remade for this shape, when every key it cites survived; otherwise it
// is dropped, as it would have nothing to compare.
func (s structCore) withDerived(from []structRule) structCore {
	for _, r := range from {
		if r.rebuild == nil || slices.ContainsFunc(r.keys, func(k string) bool { return !s.hasKey(k) }) {
			continue
		}

		s = s.with(r.rebuild(s))
	}

	return s
}

// field returns the field of the shape read from key; what names the rule for
// the panic of a key that is not in the shape.
func (s structCore) field(what, key string) structField {
	for _, f := range s.fields {
		if f.key == key {
			return f
		}
	}

	keys := make([]string, len(s.fields))
	for i, f := range s.fields {
		keys[i] = f.key
	}

	panic(fmt.Sprintf("z: %s: no key %q in the shape (keys: %s)", what, key, strings.Join(keys, ", ")))
}

// exists reports whether key was sent with a value: present, not null and
// not the empty string, which is what an HTML form can say.
func exists(raw map[string]any, key string) bool {
	v, ok := raw[key]
	if !ok || v == nil {
		return false
	}

	s, isStr := v.(string)

	return !isStr || s != ""
}

// names joins the human names of keys for a message and its params.
func names(keys []string) string {
	out := make([]string, len(keys))
	for i, k := range keys {
		out[i] = fieldName(k)
	}

	return strings.Join(out, ", ")
}

// fieldValue is the Go value of the field f in out, dereferenced; nil for a
// nil pointer.
func fieldValue(out reflect.Value, f structField) any {
	v := reflect.Indirect(out.FieldByIndex(f.index))
	if !v.IsValid() {
		return nil
	}

	return v.Interface()
}

// holds reports whether the field f of out holds a value: in typed data a
// non-zero one (a nil pointer and the zero value are the typed way of "not
// set"), otherwise whether key was sent.
func holds(typed bool, raw map[string]any, key string, out reflect.Value, f structField) bool {
	if !typed {
		return exists(raw, key)
	}

	v := fieldValue(out, f)

	return v != nil && !reflect.ValueOf(v).IsZero()
}

// ------------------------------------------------------------ confirmed

func (s structCore) confirmed(key string) structRule {
	s.field("Confirmed", key)

	return structRule{
		keys: []string{key}, needsInput: true, confirmation: key + "_confirmation",
		rebuild: func(c structCore) structRule { return c.confirmed(key) },
		run: func(c *pctx, path string, raw map[string]any, _ reflect.Value, bad map[string]bool, _ bool) {
			if bad[key] || !exists(raw, key) {
				return
			}

			// The confirmation is compared as sent. Its value is never put in
			// the issue: it is usually a password.
			if !reflect.DeepEqual(raw[key], raw[key+"_confirmation"]) {
				s.o.issue(c, joinPath(path, key), CodeCustom, "confirmed", nil)
			}
		},
	}
}

// ------------------------------------------------------ same / different

// sameValue compares two field values: instants of time with Equal (the same
// moment in two zones is the same), anything else deeply.
func sameValue(a, b any) bool {
	if ta, ok := a.(time.Time); ok {
		tb, ok := b.(time.Time)

		return ok && ta.Equal(tb)
	}

	return reflect.DeepEqual(a, b)
}

func (s structCore) sameOrDifferent(what, key, other string, want bool) structRule {
	a, b := s.field(what, key), s.field(what, other)
	if a.ftype != b.ftype {
		panic(fmt.Sprintf("z: %s: fields %q and %q have different types", what, key, other))
	}

	issueKey := "same"
	if !want {
		issueKey = "different"
	}

	return structRule{
		keys:    []string{key, other},
		rebuild: func(c structCore) structRule { return c.sameOrDifferent(what, key, other, want) },
		run: func(c *pctx, path string, raw map[string]any, out reflect.Value, bad map[string]bool, typed bool) {
			if bad[key] || bad[other] || !holds(typed, raw, key, out, a) || !holds(typed, raw, other, out, b) {
				return
			}

			if sameValue(fieldValue(out, a), fieldValue(out, b)) != want {
				s.o.issue(c, joinPath(path, key), CodeCustom, issueKey, map[string]any{"other": fieldName(other)})
			}
		},
	}
}

// ------------------------------------------------------------- required

// requiredField checks that key may be absent (the rule would never matter
// otherwise).
func (s structCore) requiredField(what, key string) {
	if f := s.field(what, key); !f.schema.optional() {
		panic(fmt.Sprintf("z: %s: key %q must be optional in the shape, or it is always required", what, key))
	}
}

func (s structCore) requiredIf(key, other string, values []any) structRule {
	s.requiredField("RequiredIf", key)

	o := s.field("RequiredIf", other)

	wanted := make([]string, len(values))
	for i, v := range values {
		wanted[i] = fmt.Sprint(v)
	}

	return structRule{
		keys: []string{key, other}, needsInput: true,
		rebuild: func(c structCore) structRule { return c.requiredIf(key, other, values) },
		run: func(c *pctx, path string, raw map[string]any, out reflect.Value, bad map[string]bool, _ bool) {
			if bad[key] || bad[other] || exists(raw, key) {
				return
			}

			// The condition reads the parsed value of other, so a Default
			// on it counts as what the field holds; only key asks whether
			// it was sent.
			v := fieldValue(out, o)
			if v == nil {
				return
			}

			if i := slices.Index(wanted, fmt.Sprint(v)); i >= 0 {
				s.o.issue(c, joinPath(path, key), CodeRequired, "required_if",
					map[string]any{"other": fieldName(other), "value": wanted[i]})
			}
		},
	}
}

// requiredWith builds the rule of one of the RequiredIf* families: key is
// required when cond(present) is true, where present lists, for each key of
// others, whether it was sent.
func (s structCore) requiredWith(
	what, issueKey, key string,
	others []string,
	cond func(present []bool) bool,
) structRule {
	s.requiredField(what, key)

	for _, o := range others {
		s.field(what, o)
	}

	if len(others) == 0 {
		panic(fmt.Sprintf("z: %s needs at least one other key", what))
	}

	return structRule{
		keys: append([]string{key}, others...), needsInput: true,
		rebuild: func(c structCore) structRule { return c.requiredWith(what, issueKey, key, others, cond) },
		run: func(c *pctx, path string, raw map[string]any, _ reflect.Value, bad map[string]bool, _ bool) {
			if bad[key] || exists(raw, key) {
				return
			}

			present := make([]bool, len(others))

			for i, o := range others {
				// A field that is itself wrong says nothing about presence.
				if bad[o] {
					return
				}

				present[i] = exists(raw, o)
			}

			if cond(present) {
				s.o.issue(c, joinPath(path, key), CodeRequired, issueKey, map[string]any{"others": names(others)})
			}
		},
	}
}

func all(present []bool) bool   { return !slices.Contains(present, false) }
func anyOf(present []bool) bool { return slices.Contains(present, true) }
func none(present []bool) bool  { return !anyOf(present) }

// --------------------------------------------------------------- dates

func (s structCore) timeField(what, key string) structField {
	f := s.field(what, key)

	t := f.ftype
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	if t != reflect.TypeFor[time.Time]() {
		panic(fmt.Sprintf("z: %s: key %q must be a time.Time field, got %s", what, key, f.ftype))
	}

	return f
}

// dateRule compares two time fields; bad reports whether (a, b) breaks it.
func (s structCore) dateRule(
	what, issueKey, code, key, other string,
	bad func(a, b time.Time) bool,
) structRule {
	a, b := s.timeField(what, key), s.timeField(what, other)

	return structRule{
		keys:    []string{key, other},
		rebuild: func(c structCore) structRule { return c.dateRule(what, issueKey, code, key, other, bad) },
		run: func(c *pctx, path string, raw map[string]any, out reflect.Value, failed map[string]bool, typed bool) {
			if failed[key] || failed[other] || !holds(typed, raw, key, out, a) || !holds(typed, raw, other, out, b) {
				return
			}

			ta, _ := fieldValue(out, a).(time.Time)
			tb, _ := fieldValue(out, b).(time.Time)

			if bad(ta, tb) {
				s.o.issue(c, joinPath(path, key), code, issueKey, map[string]any{"other": fieldName(other)})
			}
		},
	}
}

// ------------------------------------------------------ StructSchema API

// Confirmed requires the value of key to equal the one sent as
// key+"_confirmation" ("password" and "password_confirmation"), the way a
// sign-up form asks for it twice. The confirmation need not be in the shape
// or in the struct (Strict and Passthrough do not count it as an unknown
// key); a missing one counts as different. The issue is reported at key and
// carries no value. It needs the raw input, so it is skipped for a typed value
// (Check, or Parse given a T). It panics when key is not in the shape.
func (s StructSchema[T]) Confirmed(key string) StructSchema[T] {
	s.core = s.core.with(s.core.confirmed(key))

	return s
}

// SameAs requires the fields key and other to hold the same value. Both must
// be in the shape and have the same Go type (it panics otherwise); the rule
// is skipped when either is absent or already failed (for a typed value: when
// either holds the zero value). Instants of time are compared with Equal. The
// issue is reported at key.
func (s StructSchema[T]) SameAs(key, other string) StructSchema[T] {
	s.core = s.core.with(s.core.sameOrDifferent("SameAs", key, other, true))

	return s
}

// NotSameAs requires the fields key and other to hold different values, with
// the same conditions as [StructSchema.SameAs].
func (s StructSchema[T]) NotSameAs(key, other string) StructSchema[T] {
	s.core = s.core.with(s.core.sameOrDifferent("NotSameAs", key, other, false))

	return s
}

// RequiredIf requires key when the field other holds one of values; they are
// compared as text (fmt.Sprint), so RequiredIf("tax_id", "kind", "company")
// works for a string or a named string. key must be optional in the shape (it
// panics otherwise: a required key could never be missing) and both keys must
// be in the shape. The condition reads the parsed value of other, so its
// Default counts. Like the other required rules it is skipped for a typed
// value (Check, or Parse given a T), which cannot say whether a key was sent.
// The issue is reported at key.
func (s StructSchema[T]) RequiredIf(key, other string, values ...any) StructSchema[T] {
	s.core = s.core.with(s.core.requiredIf(key, other, values))

	return s
}

// RequiredWhen requires key when when(value) is true, where value is the
// parsed struct: the escape hatch for a condition the other methods do not
// express. key must be optional in the shape. The issue is reported at key.
func (s StructSchema[T]) RequiredWhen(key string, when func(T) bool) StructSchema[T] {
	s.core.requiredField("RequiredWhen", key)

	rule := structRule{
		// Not derivable: when is written for T.
		keys: []string{key}, needsInput: true,
		run: func(c *pctx, path string, raw map[string]any, out reflect.Value, bad map[string]bool, _ bool) {
			if bad[key] || exists(raw, key) {
				return
			}

			if v, ok := reflect.TypeAssert[T](out); ok && when(v) {
				s.core.o.issue(c, joinPath(path, key), CodeRequired, CodeRequired, nil)
			}
		},
	}

	s.core = s.core.with(rule)

	return s
}

// RequiredIfExists requires key when every one of others was sent (present,
// not null and not empty). key must be optional in the shape and the others
// must be in it.
func (s StructSchema[T]) RequiredIfExists(key string, others ...string) StructSchema[T] {
	s.core = s.core.with(s.core.requiredWith("RequiredIfExists", "required_with", key, others, all))

	return s
}

// RequiredIfAnyExists requires key when at least one of others was sent.
func (s StructSchema[T]) RequiredIfAnyExists(key string, others ...string) StructSchema[T] {
	s.core = s.core.with(s.core.requiredWith("RequiredIfAnyExists", "required_with", key, others, anyOf))

	return s
}

// RequiredIfMissing requires key when none of others was sent.
func (s StructSchema[T]) RequiredIfMissing(key string, others ...string) StructSchema[T] {
	s.core = s.core.with(s.core.requiredWith("RequiredIfMissing", "required_without", key, others, none))

	return s
}

// RequiredIfAnyMissing requires key when at least one of others was not sent.
func (s StructSchema[T]) RequiredIfAnyMissing(key string, others ...string) StructSchema[T] {
	s.core = s.core.with(s.core.requiredWith("RequiredIfAnyMissing", "required_without", key, others,
		func(present []bool) bool { return !all(present) }))

	return s
}

// AfterField requires the time at key to be after the one at other. Both
// must be time.Time (or *time.Time) fields in the shape; the rule is skipped
// when either is absent or already failed. The issue is reported at key.
func (s StructSchema[T]) AfterField(key, other string) StructSchema[T] {
	s.core = s.core.with(s.core.dateRule("AfterField", "date.after_field", CodeTooSmall, key, other,
		func(a, b time.Time) bool { return !a.After(b) }))

	return s
}

// AfterOrEqualField is [StructSchema.AfterField] that accepts the same instant.
func (s StructSchema[T]) AfterOrEqualField(key, other string) StructSchema[T] {
	s.core = s.core.with(s.core.dateRule("AfterOrEqualField", "date.after_or_equal_field", CodeTooSmall, key, other,
		func(a, b time.Time) bool { return a.Before(b) }))

	return s
}

// BeforeField requires the time at key to be before the one at other, with
// the conditions of [StructSchema.AfterField].
func (s StructSchema[T]) BeforeField(key, other string) StructSchema[T] {
	s.core = s.core.with(s.core.dateRule("BeforeField", "date.before_field", CodeTooBig, key, other,
		func(a, b time.Time) bool { return !a.Before(b) }))

	return s
}

// BeforeOrEqualField is [StructSchema.BeforeField] that accepts the same instant.
func (s StructSchema[T]) BeforeOrEqualField(key, other string) StructSchema[T] {
	s.core = s.core.with(s.core.dateRule("BeforeOrEqualField", "date.before_or_equal_field", CodeTooBig, key, other,
		func(a, b time.Time) bool { return a.After(b) }))

	return s
}

// EmptyAsAbsent treats an empty string sent for any field as if the key were
// missing, the way HTML forms send a blank input: an optional field takes
// its Default (or stays unset) and a required one reports "required". It is
// off by default, because for z.String() the empty string is a valid value.
func (s StructSchema[T]) EmptyAsAbsent() StructSchema[T] {
	s.core.emptyAsAbsent = true

	return s
}

// Bail makes every field report at most its first issue instead of all of
// them: a password that is too short and also lacks a digit says only the
// first. It applies to the checks of string and number fields, including
// those of nested structs; fields still report independently of each other.
func (s StructSchema[T]) Bail() StructSchema[T] {
	s.core.bail = true

	return s
}
