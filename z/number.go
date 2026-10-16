package z

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Number is the set of numeric types NumberSchema produces.
type Number interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

type numCheck[N Number] func(o *opts[N], c *pctx, path string, v N)

// NumberSchema parses numbers of type N. It accepts JSON numbers and, from
// forms and query strings, numeric strings. Integer schemas reject
// fractions and out-of-range values; float schemas reject NaN and ±Inf.
type NumberSchema[N Number] struct {
	o      opts[N]
	checks []numCheck[N]
}

// Int returns a schema for int.
func Int() NumberSchema[int] { return NumberSchema[int]{} }

// Int32 returns a schema for int32.
func Int32() NumberSchema[int32] { return NumberSchema[int32]{} }

// Int64 returns a schema for int64.
func Int64() NumberSchema[int64] { return NumberSchema[int64]{} }

// Uint returns a schema for uint.
func Uint() NumberSchema[uint] { return NumberSchema[uint]{} }

// Uint64 returns a schema for uint64.
func Uint64() NumberSchema[uint64] { return NumberSchema[uint64]{} }

// Float returns a schema for float64.
func Float() NumberSchema[float64] { return NumberSchema[float64]{} }

// Float32 returns a schema for float32.
func Float32() NumberSchema[float32] { return NumberSchema[float32]{} }

// NumberOf returns a schema for any numeric type, e.g. a named type.
func NumberOf[N Number]() NumberSchema[N] { return NumberSchema[N]{} }

func (s NumberSchema[N]) with(check numCheck[N]) NumberSchema[N] {
	s.o = s.o.clone()
	s.checks = append(slices.Clip(s.checks), check)

	return s
}

// GoType implements AnySchema.
func (NumberSchema[N]) GoType() reflect.Type { return reflect.TypeFor[N]() }

func (s NumberSchema[N]) optional() bool { return s.o.optional || s.o.hasDef }

func (s NumberSchema[N]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, true, s.core)
	return v, set, ok
}

func (s NumberSchema[N]) core(c *pctx, path string, in any) (N, bool) {
	v, ok := toNumber[N](in, c.coerce)
	if !ok {
		expected := "a number"
		if isInteger[N]() {
			expected = "an integer"
		}

		s.o.issue(
			c,
			path,
			CodeInvalidType,
			CodeInvalidType,
			map[string]any{"expected": expected, "received": typeName(in)},
		)

		return 0, false
	}

	before := len(c.issues)

	for _, chk := range s.checks {
		chk(&s.o, c, path, v)
	}

	return v, len(c.issues) == before
}

func isInteger[N Number]() bool {
	k := reflect.TypeFor[N]().Kind()
	return k != reflect.Float32 && k != reflect.Float64
}

func isUnsigned[N Number]() bool {
	switch reflect.TypeFor[N]().Kind() {
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return true
	default:
		return false
	}
}

// toNumber converts in to N without loss.
func toNumber[N Number](in any, coerce bool) (N, bool) {
	var text string

	switch x := in.(type) {
	case json.Number:
		text = x.String()
	case float64:
		text = strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		text = strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int:
		text = strconv.Itoa(x)
	case int64:
		text = strconv.FormatInt(x, 10)
	case int32:
		text = strconv.FormatInt(int64(x), 10)
	case uint:
		text = strconv.FormatUint(uint64(x), 10)
	case uint64:
		text = strconv.FormatUint(x, 10)
	case string:
		if !coerce {
			return 0, false
		}

		text = strings.TrimSpace(x)
	default:
		if v := reflect.ValueOf(in); v.IsValid() && v.CanConvert(reflect.TypeFor[N]()) && v.Kind() != reflect.String &&
			v.Kind() != reflect.Bool {
			return parseNumberText[N](toText(v))
		}

		return 0, false
	}

	return parseNumberText[N](text)
}

func toText(v reflect.Value) string {
	switch {
	case v.CanInt():
		return strconv.FormatInt(v.Int(), 10)
	case v.CanUint():
		return strconv.FormatUint(v.Uint(), 10)
	case v.CanFloat():
		return strconv.FormatFloat(v.Float(), 'f', -1, 64)
	default:
		return ""
	}
}

func parseNumberText[N Number](text string) (N, bool) {
	bits := reflect.TypeFor[N]().Bits()

	switch {
	case !isInteger[N]():
		f, err := strconv.ParseFloat(text, bits)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}

		return N(f), true
	case isUnsigned[N]():
		u, err := strconv.ParseUint(text, 10, bits)
		if err != nil {
			return 0, false
		}

		return N(u), true
	default:
		i, err := strconv.ParseInt(text, 10, bits)
		if err != nil {
			return 0, false
		}

		return N(i), true
	}
}

// Min requires v >= n.
func (s NumberSchema[N]) Min(n N) NumberSchema[N] {
	return s.kw("minimum", n).with(func(o *opts[N], c *pctx, path string, v N) {
		if v < n {
			o.issue(c, path, CodeTooSmall, "too_small.number", map[string]any{"min": n})
		}
	})
}

// Max requires v <= n.
func (s NumberSchema[N]) Max(n N) NumberSchema[N] {
	return s.kw("maximum", n).with(func(o *opts[N], c *pctx, path string, v N) {
		if v > n {
			o.issue(c, path, CodeTooBig, "too_big.number", map[string]any{"max": n})
		}
	})
}

// Positive requires v > 0.
func (s NumberSchema[N]) Positive() NumberSchema[N] {
	return s.kw("exclusiveMinimum", 0).with(func(o *opts[N], c *pctx, path string, v N) {
		if v <= 0 {
			o.issue(c, path, CodeTooSmall, "too_small.positive", map[string]any{"min": 0})
		}
	})
}

// NonNegative requires v >= 0.
func (s NumberSchema[N]) NonNegative() NumberSchema[N] { return s.Min(0) }

// MultipleOf requires v to be a multiple of m.
func (s NumberSchema[N]) MultipleOf(m N) NumberSchema[N] {
	return s.kw("multipleOf", m).with(func(o *opts[N], c *pctx, path string, v N) {
		if m != 0 && math.Remainder(float64(v), float64(m)) != 0 {
			o.issue(c, path, CodeNotMultiple, CodeNotMultiple, map[string]any{"multiple": m})
		}
	})
}

// Optional allows the key to be absent (the zero value is used). An empty
// string from a form counts as absent.
func (s NumberSchema[N]) Optional() NumberSchema[N] { s.o = s.o.clone(); s.o.optional = true; return s }

// Nullable allows null.
func (s NumberSchema[N]) Nullable() NumberSchema[N] { s.o = s.o.clone(); s.o.nullable = true; return s }

// Default uses v when the key is absent.
func (s NumberSchema[N]) Default(v N) NumberSchema[N] {
	s.o = s.o.clone()
	s.o.hasDef, s.o.def = true, v

	return s
}

// Refine adds a custom check.
func (s NumberSchema[N]) Refine(fn func(N) error) NumberSchema[N] {
	s.o = s.o.clone()
	s.o.refines = append(s.o.refines, fn)

	return s
}

// Transform maps the parsed value.
func (s NumberSchema[N]) Transform(fn func(N) N) NumberSchema[N] {
	s.o = s.o.clone()
	s.o.transforms = append(s.o.transforms, fn)

	return s
}

// Message overrides the message for an issue code or detailed key.
func (s NumberSchema[N]) Message(code, message string) NumberSchema[N] {
	s.o = s.o.clone()
	if s.o.messages == nil {
		s.o.messages = map[string]string{}
	}

	s.o.messages[code] = message

	return s
}

// Describe sets a description used by schema exports.
func (s NumberSchema[N]) Describe(text string) NumberSchema[N] {
	s.o = s.o.clone()
	s.o.desc = text
	return s
}

// Parse implements Schema.
func (s NumberSchema[N]) Parse(in any) (N, error) { return parseRoot[N](s, in) }

// Check implements Schema.
func (s NumberSchema[N]) Check(v N) error { return checkValue(s, v) }

func (s NumberSchema[N]) kw(k string, v any) NumberSchema[N] {
	s.o = s.o.clone()
	s.o.setKW(k, v)

	return s
}
