package z

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FromTags builds a schema for T from `validate` struct tags, e.g.
//
//	type Login struct {
//		Email    string `json:"email"    validate:"required,email"`
//		Password string `json:"password" validate:"required,min=12"`
//	}
//
// Rules: required, min=N, max=N, len=N, email, url, uuid, ulid, alpha,
// alphanum, numeric, oneof=a b c, regex=PATTERN (last rule), startswith=,
// endswith=, contains=, positive, unique, dive (rules after it apply to
// slice elements). Fields without "required" are optional; pointers are
// optional and nullable; nested structs are parsed recursively. It panics
// on unknown rules or bad arguments, so mistakes surface at boot.
func FromTags[T any]() StructSchema[T] {
	return StructSchema[T]{core: tagCore(reflect.TypeFor[T](), 0)}
}

// tagCache memoizes FromTags per type for For; values are immutable schemas.
var tagCache sync.Map //nolint:gochecknoglobals // memoization of immutable values only

// For returns the tag-derived schema for T, built once and cached.
func For[T any]() StructSchema[T] {
	rt := reflect.TypeFor[T]()
	if v, ok := tagCache.Load(rt); ok {
		core, _ := v.(structCore)

		return StructSchema[T]{core: core}
	}

	core := tagCore(rt, 0)
	tagCache.Store(rt, core)

	return StructSchema[T]{core: core}
}

const maxTagDepth = 32

func tagCore(rt reflect.Type, depth int) structCore {
	if depth > maxTagDepth {
		panic(fmt.Sprintf("z: %s nests too deeply (recursive type?)", rt))
	}

	if rt.Kind() != reflect.Struct {
		panic(fmt.Sprintf("z: FromTags needs a struct type, got %s", rt))
	}

	shape := Shape{}

	for _, f := range reflect.VisibleFields(rt) {
		if !f.IsExported() || (f.Anonymous && f.Type.Kind() == reflect.Struct) {
			continue
		}

		keys := fieldKeys(f)
		if len(keys) == 0 {
			continue // json:"-"
		}

		rules := splitRules(f.Tag.Get("validate"))

		s, err := schemaForType(f.Type, rules, depth)
		if err != nil {
			panic(fmt.Sprintf("z: %s.%s: %v", rt, f.Name, err))
		}

		shape[keys[0]] = s
	}

	return newStructCore(rt, shape)
}

// splitRules splits "required,min=3,regex=^a,b$" keeping regex last and
// whole.
func splitRules(tag string) []string {
	if tag == "" {
		return nil
	}

	if i := strings.Index(tag, "regex="); i >= 0 {
		head := strings.TrimSuffix(tag[:i], ",")

		var rules []string
		if head != "" {
			rules = strings.Split(head, ",")
		}

		return append(rules, tag[i:])
	}

	return strings.Split(tag, ",")
}

type ruleSet struct {
	required bool
	rules    []string // rules for this value
	dive     []string // rules for slice elements
}

func parseRules(rules []string) ruleSet {
	var rs ruleSet

	for i, r := range rules {
		r = strings.TrimSpace(r)

		switch r {
		case "":
			continue
		case "required":
			rs.required = true
		case "omitempty":
		case "dive":
			rs.dive = rules[i+1:]
			return rs
		default:
			rs.rules = append(rs.rules, r)
		}
	}

	return rs
}

func schemaForType(t reflect.Type, raw []string, depth int) (AnySchema, error) {
	rs := parseRules(raw)
	nullable := false

	if t.Kind() == reflect.Pointer {
		nullable = true
		t = t.Elem()
	}

	optional := !rs.required || nullable

	switch {
	case t == reflect.TypeFor[time.Time]():
		s := Time()
		if err := noRules(rs.rules); err != nil {
			return nil, err
		}

		if optional {
			s = s.Optional()
		}

		if nullable {
			s = s.Nullable()
		}

		return s, nil
	case t.Kind() == reflect.String:
		return stringFromRules(t, rs, optional, nullable)
	case t.Kind() == reflect.Bool:
		if err := noRules(rs.rules); err != nil {
			return nil, err
		}

		s := Bool()
		if optional {
			s = s.Optional()
		}

		return s, nil
	case isNumberKind(t.Kind()):
		return numberFromRules(t, rs, optional)
	case t.Kind() == reflect.Slice:
		elem, err := schemaForType(t.Elem(), rs.dive, depth+1)
		if err != nil {
			return nil, err
		}

		s := Slice(elem)

		for _, r := range rs.rules {
			name, arg, _ := strings.Cut(r, "=")

			switch name {
			case "min", "max", "len":
				n, err := strconv.Atoi(arg)
				if err != nil {
					return nil, fmt.Errorf("rule %q needs an integer", r)
				}

				switch name {
				case "min":
					s = s.Min(n)
				case "max":
					s = s.Max(n)
				default:
					s = s.Len(n)
				}
			case "unique":
				s = s.Unique()
			default:
				return nil, fmt.Errorf("unknown rule %q for a list", r)
			}
		}

		if rs.required {
			s = s.NonEmpty()
		}

		if optional {
			s = s.Optional()
		}

		return s, nil
	case t.Kind() == reflect.Struct:
		if err := noRules(rs.rules); err != nil {
			return nil, err
		}

		core := tagCore(t, depth+1)
		core.o.optional = optional

		return structAny{core: core}, nil
	default:
		return nil, fmt.Errorf("unsupported type %s", t)
	}
}

func noRules(rules []string) error {
	if len(rules) > 0 {
		return fmt.Errorf("unsupported rules %v for this type", rules)
	}

	return nil
}

func isNumberKind(k reflect.Kind) bool {
	switch k {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64:
		return true
	default:
		return false
	}
}

func stringFromRules(t reflect.Type, rs ruleSet, optional, nullable bool) (AnySchema, error) {
	s := String()

	if rs.required {
		s = s.Min(1).Message("too_small.string", defaultMessages[CodeRequired])
	}

	for _, r := range rs.rules {
		name, arg, _ := strings.Cut(r, "=")

		switch name {
		case "min", "max", "len":
			n, err := strconv.Atoi(arg)
			if err != nil {
				return nil, fmt.Errorf("rule %q needs an integer", r)
			}

			switch name {
			case "min":
				s = s.Min(n)
			case "max":
				s = s.Max(n)
			default:
				s = s.Len(n)
			}
		case "email":
			s = s.Email()
		case "url":
			s = s.URL()
		case "uuid":
			s = s.UUID()
		case "ulid":
			s = s.ULID()
		case "alpha":
			s = s.Alpha()
		case "alphanum":
			s = s.Alphanum()
		case "numeric":
			s = s.Numeric()
		case "oneof":
			s = s.OneOf(strings.Fields(arg)...)
		case "regex":
			re, err := regexp.Compile(arg)
			if err != nil {
				return nil, fmt.Errorf("rule %q: %w", r, err)
			}

			s = s.Regex(re)
		case "startswith":
			s = s.StartsWith(arg)
		case "endswith":
			s = s.EndsWith(arg)
		case "contains":
			s = s.Includes(arg)
		case "trim":
			s = s.Trim()
		case "lowercase":
			s = s.ToLower()
		default:
			return nil, fmt.Errorf("unknown rule %q for a string", r)
		}
	}

	if optional {
		s = s.Optional()
	}

	if nullable {
		s = s.Nullable()
	}

	if t != reflect.TypeFor[string]() {
		return convertedString{s: s, t: t}, nil
	}

	return s, nil
}

// convertedString adapts a string schema to a named string type.
type convertedString struct {
	s StringSchema
	t reflect.Type
}

func (c convertedString) GoType() reflect.Type { return c.t }
func (c convertedString) optional() bool       { return c.s.optional() }

func (c convertedString) parseAny(p *pctx, path string, in any, present bool) (any, bool, bool) {
	if rv := reflect.ValueOf(in); rv.IsValid() && rv.Kind() == reflect.String {
		in = rv.String()
	}

	v, set, ok := c.s.parseAny(p, path, in, present)
	if !ok || !set {
		return v, set, ok
	}

	return reflect.ValueOf(v).Convert(c.t).Interface(), set, ok
}

func numberFromRules(t reflect.Type, rs ruleSet, optional bool) (AnySchema, error) {
	var bounds []func(float64) error

	positive := false
	kw := map[string]any{}

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
		default:
			return nil, fmt.Errorf("unknown rule %q for a number", r)
		}
	}

	return numberAny{t: t, bounds: bounds, positive: positive, opt: optional, kw: kw}, nil
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
	kw       map[string]any
}

func (n numberAny) GoType() reflect.Type { return n.t }
func (n numberAny) optional() bool       { return n.opt }

func (n numberAny) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	var s AnySchema

	switch n.t.Kind() {
	case reflect.Float32, reflect.Float64:
		s = NumberSchema[float64]{o: opts[float64]{optional: n.opt}}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		s = NumberSchema[uint64]{o: opts[uint64]{optional: n.opt}}
	default:
		s = NumberSchema[int64]{o: opts[int64]{optional: n.opt}}
	}

	v, set, ok := s.parseAny(c, path, in, present)
	if !ok || !set {
		return v, set, ok
	}

	rv := reflect.ValueOf(v)
	if !rv.CanConvert(n.t) || rv.Convert(n.t).Convert(rv.Type()).Interface() != v {
		c.add(path, CodeInvalidType, render(defaultMessages[CodeInvalidType], path,
			map[string]any{"expected": "a number in range"}), nil)

		return nil, false, false
	}

	f, _ := toNumber[float64](v, false)

	if n.positive && f <= 0 {
		c.add(
			path,
			CodeTooSmall,
			render(defaultMessages["too_small.number"], path, map[string]any{"min": "greater than 0"}),
			map[string]any{"min": 0},
		)

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
		c.add(path, code, render(defaultMessages[key], path, params), params)

		return nil, false, false
	}

	return rv.Convert(n.t).Interface(), true, true
}

// structAny is a nested tag-derived struct.
type structAny struct {
	core structCore
}

func (s structAny) GoType() reflect.Type { return s.core.rt }
func (s structAny) optional() bool       { return s.core.o.optional }

func (s structAny) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	o := s.core.o

	return parse(&o, c, path, in, present, false, func(c *pctx, path string, in any) (any, bool) {
		rv, ok := s.core.parse(c, path, in)
		if !ok {
			return nil, false
		}

		return rv.Interface(), true
	})
}
