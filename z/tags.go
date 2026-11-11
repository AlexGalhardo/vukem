package z

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
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
//	any:    required, dive (the rules after it apply to slice elements
//	        or map values)
//	string: min=N, max=N, len=N, email, url, uuid, ulid, alpha, alphanum,
//	        numeric, ip, ipv4, ipv6, cidr, mac, hostname, e164, slug, hex,
//	        base64, base64url, jwt (shape only), iso_date, iso_time,
//	        iso_datetime, iso_duration, oneof=a b c, regex=PATTERN (last
//	        rule), startswith=, endswith=, contains=, trim, lowercase,
//	        uppercase, normalize_email, escape, omitempty (skips the
//	        rules above when the value is empty, so "omitempty,oneof=a b"
//	        accepts "a", "b" and ""), default=V (used when the key is
//	        absent, or empty with omitempty; it also reaches the OpenAPI
//	        document)
//	number: min=N, max=N, positive, default=V
//	list:   min=N, max=N, len=N, unique
//	map:    min=N, max=N (entry count); the key must be string
//	cross:  confirmed, same=KEY, different=KEY, required_if=KEY V1 V2,
//	        required_with=K1 K2 (any present), required_with_all=K1 K2,
//	        required_without=K1 K2 (any missing), required_without_all=K1 K2,
//	        gtfield=KEY, gtefield=KEY, ltfield=KEY, ltefield=KEY (times);
//	        on any field, citing other fields by input key; the field the
//	        rule is on must be optional for the required_* rules
//
// Fields without "required" are optional; "required" on a string means
// non-empty, on a list present and not null (use min=1 for a non-empty
// list); pointers are optional and nullable; nested structs are parsed
// recursively. A bool, a time.Time and a nested struct take no rule but
// "required", and so do any (a JSON value of any shape) and
// map[string]any (a JSON object). It panics on unknown rules or bad
// arguments, so mistakes surface at boot. The same table is in
// docs/validation.md; both are checked against the implementation by TestTagRules_* in this package.
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
	scopeMap    tagScope = "map"
	// scopeCross rules belong to the struct around the field: they cite other
	// fields by input key and are read by tagCore, not by the field's schema.
	scopeCross tagScope = "cross"
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
		{name: "omitempty", scopes: []tagScope{scopeString}},
		{name: "default", arg: "1", scopes: []tagScope{scopeString, scopeNumber}},
		{name: "dive", scopes: []tagScope{scopeAny}},
		{name: "min", arg: "1", scopes: []tagScope{scopeString, scopeNumber, scopeList, scopeMap}},
		{name: "max", arg: "5", scopes: []tagScope{scopeString, scopeNumber, scopeList, scopeMap}},
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
		{name: "ip", scopes: []tagScope{scopeString}},
		{name: "ipv4", scopes: []tagScope{scopeString}},
		{name: "ipv6", scopes: []tagScope{scopeString}},
		{name: "cidr", scopes: []tagScope{scopeString}},
		{name: "mac", scopes: []tagScope{scopeString}},
		{name: "hostname", scopes: []tagScope{scopeString}},
		{name: "e164", scopes: []tagScope{scopeString}},
		{name: "slug", scopes: []tagScope{scopeString}},
		{name: "hex", scopes: []tagScope{scopeString}},
		{name: "base64", scopes: []tagScope{scopeString}},
		{name: "base64url", scopes: []tagScope{scopeString}},
		{name: "jwt", scopes: []tagScope{scopeString}},
		{name: "iso_date", scopes: []tagScope{scopeString}},
		{name: "iso_time", scopes: []tagScope{scopeString}},
		{name: "iso_datetime", scopes: []tagScope{scopeString}},
		{name: "iso_duration", scopes: []tagScope{scopeString}},
		{name: "normalize_email", scopes: []tagScope{scopeString}},
		{name: "escape", scopes: []tagScope{scopeString}},
		{name: "uppercase", scopes: []tagScope{scopeString}},
		{name: "confirmed", scopes: []tagScope{scopeCross}},
		{name: "same", arg: "other", scopes: []tagScope{scopeCross}},
		{name: "different", arg: "other", scopes: []tagScope{scopeCross}},
		{name: "required_if", arg: "kind company", scopes: []tagScope{scopeCross}},
		{name: "required_with", arg: "a b", scopes: []tagScope{scopeCross}},
		{name: "required_with_all", arg: "a b", scopes: []tagScope{scopeCross}},
		{name: "required_without", arg: "a b", scopes: []tagScope{scopeCross}},
		{name: "required_without_all", arg: "a b", scopes: []tagScope{scopeCross}},
		{name: "gtfield", arg: "other", scopes: []tagScope{scopeCross}},
		{name: "gtefield", arg: "other", scopes: []tagScope{scopeCross}},
		{name: "ltfield", arg: "other", scopes: []tagScope{scopeCross}},
		{name: "ltefield", arg: "other", scopes: []tagScope{scopeCross}},
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

	var cross []crossTag

	for _, f := range reflect.VisibleFields(rt) {
		if !f.IsExported() || (f.Anonymous && f.Type.Kind() == reflect.Struct) {
			continue
		}

		keys := fieldKeys(f)
		if len(keys) == 0 {
			continue // json:"-"
		}

		rules, crossRules := splitCross(SplitRules(f.Tag.Get("validate")))
		for _, r := range crossRules {
			cross = append(cross, crossTag{owner: keys[0], rule: r})
		}

		s, err := schemaForType(f.Type, rules, depth)
		if err != nil {
			panic(fmt.Sprintf("z: %s.%s: %v", rt, f.Name, err))
		}

		shape[keys[0]] = s
	}

	return newStructCore(rt, shape).withCrossRules(rt.String(), cross)
}

// SplitRules splits a `validate` tag into its rules: "required,min=3,regex=^a,b$"
// gives required, min=3 and regex=^a,b$. A regex= rule is last and keeps its
// commas.
func SplitRules(tag string) []string {
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
	required  bool
	omitEmpty bool
	rules     []string // rules for this value
	dive      []string // rules for slice elements
}

// coherent refuses rule combinations that contradict each other. Picking
// one silently is how "omitempty" came to be accepted and ignored, which
// made an optional enum reject the empty value it was written for (A111).
func (rs ruleSet) coherent() error {
	if !rs.required {
		return nil
	}

	for _, r := range rs.rules {
		if name, _, _ := strings.Cut(r, "="); name == "default" {
			return errors.New(`"required" and "default" contradict each other: ` +
				`required means the caller must provide the value, a default means they need not`)
		}
	}

	if rs.omitEmpty {
		return errors.New(`"required" and "omitempty" contradict each other: ` +
			`required rejects the empty value, omitempty accepts it`)
	}

	return nil
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
			rs.omitEmpty = true
		case "dive":
			rs.dive = rules[i+1:]
			return rs
		default:
			rs.rules = append(rs.rules, r)
		}
	}

	return rs
}
