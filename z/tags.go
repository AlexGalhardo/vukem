package z

import (
	"encoding/json"
	"errors"
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
// Rules, grouped by the field kind they apply to:
//
//	any:    required, omitempty (accepted and ignored), dive (the rules
//	        after it apply to slice elements)
//	string: min=N, max=N, len=N, email, url, uuid, ulid, alpha, alphanum,
//	        numeric, oneof=a b c, regex=PATTERN (last rule), startswith=,
//	        endswith=, contains=, trim, lowercase
//	number: min=N, max=N, positive
//	list:   min=N, max=N, len=N, unique
//
// Fields without "required" are optional; "required" on a string means
// non-empty, on a list present and not null (use min=1 for a non-empty
// list); pointers are optional and nullable; nested structs are parsed
// recursively. A bool, a time.Time and a nested struct take no rule but
// "required". It panics on unknown rules or bad arguments, so mistakes
// surface at boot. The same table is in docs/validation.md; both are checked
// against the implementation by TestTagRules_* in this package.
func FromTags[T any]() StructSchema[T] {
	return StructSchema[T]{core: tagCore(reflect.TypeFor[T](), 0)}
}

// tagCache memoizes FromTags per type for For; values are immutable schemas.
var tagCache sync.Map //nolint:gochecknoglobals // memoization of immutable values only

// For returns the tag-derived schema for T, built once and cached. It
// panics like FromTags.
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

// tagScope is the family of field kinds a `validate` rule applies to.
type tagScope string

const (
	scopeAny    tagScope = "any" // consumed before the per-kind rules
	scopeString tagScope = "string"
	scopeNumber tagScope = "number"
	scopeList   tagScope = "list"
)

// tagRule is one `validate` rule FromTags understands.
type tagRule struct {
	name   string
	arg    string // example argument, empty when the rule takes none
	scopes []tagScope
}

// tagRules lists every `validate` rule FromTags understands. It is the one
// list the FromTags doc comment, the rule table in docs/validation.md, the
// unknown-rule errors and TestTagRules_* all agree on: adding a case to
// stringFromRules, numberFromRules or the list branch without adding it here
// fails TestTagRules_MatchImplementation.
func tagRules() []tagRule {
	return []tagRule{
		{name: "required", scopes: []tagScope{scopeAny}},
		{name: "omitempty", scopes: []tagScope{scopeAny}},
		{name: "dive", scopes: []tagScope{scopeAny}},
		{name: "min", arg: "1", scopes: []tagScope{scopeString, scopeNumber, scopeList}},
		{name: "max", arg: "5", scopes: []tagScope{scopeString, scopeNumber, scopeList}},
		{name: "len", arg: "3", scopes: []tagScope{scopeString, scopeList}},
		{name: "email", scopes: []tagScope{scopeString}},
		{name: "url", scopes: []tagScope{scopeString}},
		{name: "uuid", scopes: []tagScope{scopeString}},
		{name: "ulid", scopes: []tagScope{scopeString}},
		{name: "alpha", scopes: []tagScope{scopeString}},
		{name: "alphanum", scopes: []tagScope{scopeString}},
		{name: "numeric", scopes: []tagScope{scopeString}},
		{name: "oneof", arg: "a b c", scopes: []tagScope{scopeString}},
		{name: "regex", arg: "^a$", scopes: []tagScope{scopeString}},
		{name: "startswith", arg: "a", scopes: []tagScope{scopeString}},
		{name: "endswith", arg: "a", scopes: []tagScope{scopeString}},
		{name: "contains", arg: "a", scopes: []tagScope{scopeString}},
		{name: "trim", scopes: []tagScope{scopeString}},
		{name: "lowercase", scopes: []tagScope{scopeString}},
		{name: "positive", scopes: []tagScope{scopeNumber}},
		{name: "unique", scopes: []tagScope{scopeList}},
	}
}

// ruleNamesFor lists the rules usable on a scope, "name=" when the rule
// takes an argument, for an unknown-rule error.
func ruleNamesFor(scope tagScope) []string {
	var names []string

	for _, r := range tagRules() {
		for _, s := range r.scopes {
			if s != scope {
				continue
			}

			if r.arg == "" {
				names = append(names, r.name)
			} else {
				names = append(names, r.name+"=")
			}

			break
		}
	}

	return names
}

// unknownRuleError reports rule r as unusable on scope and lists what is.
func unknownRuleError(r string, scope tagScope) error {
	return fmt.Errorf("unknown rule %q for a %s, want one of: %s",
		r, scope, strings.Join(ruleNamesFor(scope), ", "))
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
	// Before the slice case: json.RawMessage is a []byte, so it would be
	// taken for a list of numbers — the document would say "array of
	// integers" and the validator would refuse every object body.
	case t == reflect.TypeFor[json.RawMessage]():
		return nil, errRawMessage
	case t == reflect.TypeFor[any]():
		s := JSON()
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
	case t == reflect.TypeFor[map[string]any]():
		s := JSONObject()
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
				return nil, unknownRuleError(r, scopeList)
			}
		}

		// required means present and not null, as in OpenAPI; an empty list
		// passes. Non-empty lists use min=1.
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
		s = s.required()
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
			return nil, unknownRuleError(r, scopeString)
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
			return nil, unknownRuleError(r, scopeNumber)
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

// errRawMessage refuses json.RawMessage with the alternative named. Parse
// takes already-decoded input, so the original bytes are gone by the time
// a schema could fill the field: re-encoding them changes key order and
// number formatting, and a type whose contract is "the bytes the client
// sent" must not be filled with different ones. map[string]any and any
// carry the same payload without that promise.
var errRawMessage = errors.New(
	"json.RawMessage cannot be validated: Parse receives decoded JSON, so the original bytes are gone. " +
		"Use map[string]any for an object or any for any JSON value (z.JSONObject, z.JSON), " +
		"and decode into your own type in the handler",
)
