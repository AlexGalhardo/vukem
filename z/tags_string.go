package z

import (
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"
)

func stringFromRules(t reflect.Type, rs ruleSet, optional, nullable bool) (AnySchema, error) {
	s := String()

	var def *string

	if rs.required {
		s = s.required()
	}

	if rs.omitEmpty {
		s = s.OmitEmpty()
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
		case "default":
			def = &arg
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

	// A default is returned for every absent key without being parsed, so
	// one the field's own rules refuse would pass silently forever. Checked
	// here, where it is a boot error naming the rule it breaks.
	if def != nil {
		if err := s.Check(*def); err != nil {
			return nil, fmt.Errorf("default %q does not satisfy this field's own rules: %w", *def, err)
		}

		s = s.Default(*def)
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
