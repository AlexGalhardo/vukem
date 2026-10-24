package z

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

func numberFromRules(t reflect.Type, rs ruleSet, optional bool) (AnySchema, error) {
	var bounds []func(float64) error

	positive := false
	kw := map[string]any{}

	var def *float64

	for _, r := range rs.rules {
		name, arg, _ := strings.Cut(r, "=")

		switch name {
		case "min", "max":
			n, err := strconv.ParseFloat(arg, 64)
			if err != nil {
				return nil, fmt.Errorf("rule %q needs a number", r)
			}

			isMin := name == "min"
			kw[map[bool]string{true: "minimum", false: "maximum"}[isMin]] = n
			bounds = append(bounds, func(v float64) error {
				if isMin && v < n || !isMin && v > n {
					return boundError{min: isMin, n: n}
				}

				return nil
			})
		case "positive":
			positive = true
			kw["exclusiveMinimum"] = 0
		case "default":
			n, err := strconv.ParseFloat(arg, 64)
			if err != nil {
				return nil, fmt.Errorf("rule %q needs a number", r)
			}

			// Not re-validated: a default outside the field's own bounds
			// would be returned for every absent key and fail nothing.
			def = &n
			kw["default"] = n
		default:
			return nil, unknownRuleError(r, scopeNumber)
		}
	}

	if def != nil {
		if positive && *def <= 0 {
			return nil, fmt.Errorf("default %v does not satisfy \"positive\"", *def)
		}

		for _, b := range bounds {
			if err := b(*def); err != nil {
				return nil, fmt.Errorf("default %v does not satisfy this field's own bounds: %w", *def, err)
			}
		}
	}

	return numberAny{t: t, bounds: bounds, positive: positive, opt: optional, def: def, kw: kw}, nil
}

// numberOpts carries the tag's optionality and default into the typed
// schema the parse runs on.
func numberOpts[N int64 | uint64 | float64](optional bool, def *float64) opts[N] {
	o := opts[N]{optional: optional}
	if def != nil {
		o.hasDef, o.def = true, N(*def)
	}

	return o
}

type boundError struct {
	min bool
	n   float64
}

func (e boundError) Error() string { return "out of bounds" }

// numberAny parses any numeric kind chosen at run time (tag schemas).
type numberAny struct {
	t        reflect.Type
	bounds   []func(float64) error
	positive bool
	opt      bool
	// def is the "default=V" value, used when the key is absent.
	def *float64
	kw  map[string]any
}

func (n numberAny) GoType() reflect.Type { return n.t }
func (n numberAny) optional() bool       { return n.opt }

func (n numberAny) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	var s AnySchema

	switch n.t.Kind() {
	case reflect.Float32, reflect.Float64:
		s = NumberSchema[float64]{o: numberOpts[float64](n.opt, n.def)}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s = NumberSchema[uint64]{o: numberOpts[uint64](n.opt, n.def)}
	default:
		s = NumberSchema[int64]{o: numberOpts[int64](n.opt, n.def)}
	}

	v, set, ok := s.parseAny(c, path, in, present)
	if !ok || !set {
		return v, set, ok
	}

	rv := reflect.ValueOf(v)
	if !rv.CanConvert(n.t) || rv.Convert(n.t).Convert(rv.Type()).Interface() != v {
		c.addKeyed(path, CodeInvalidType, CodeInvalidType, render(defaultMessages[CodeInvalidType], path,
			map[string]any{"expected": "a number in range"}), nil)

		return nil, false, false
	}

	f, _ := toNumber[float64](v, false)

	if n.positive && f <= 0 {
		params := map[string]any{"min": 0}
		c.addKeyed(path, CodeTooSmall, "too_small.positive",
			render(defaultMessages["too_small.positive"], path, params), params)

		return nil, false, false
	}

	for _, b := range n.bounds {
		err := b(f)
		if err == nil {
			continue
		}

		be, _ := err.(boundError) //nolint:errorlint // local sentinel type, never wrapped

		code, key, param := CodeTooBig, "too_big.number", "max"
		if be.min {
			code, key, param = CodeTooSmall, "too_small.number", "min"
		}

		params := map[string]any{param: be.n}
		c.addKeyed(path, code, key, render(defaultMessages[key], path, params), params)

		return nil, false, false
	}

	return rv.Convert(n.t).Interface(), true, true
}
