package z

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
)

func listFromRules(t reflect.Type, rs ruleSet, optional bool, depth int) (AnySchema, error) {
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
}

func mapFromRules(t reflect.Type, rs ruleSet, optional bool, depth int) (AnySchema, error) {
	if t.Key() != reflect.TypeFor[string]() {
		return nil, fmt.Errorf(
			"unsupported map type %s: the key must be string (a JSON object has string keys); "+
				"accepted kinds are string, numbers, bool, time.Time, any, map[string]V, slices and structs",
			t,
		)
	}

	val, err := schemaForType(t.Elem(), rs.dive, depth+1)
	if err != nil {
		return nil, err
	}

	s := Map(val)

	for _, r := range rs.rules {
		name, arg, _ := strings.Cut(r, "=")

		switch name {
		case "min", "max":
			n, err := strconv.Atoi(arg)
			if err != nil {
				return nil, fmt.Errorf("rule %q needs an integer", r)
			}

			if name == "min" {
				s = s.Min(n)
			} else {
				s = s.Max(n)
			}
		default:
			return nil, unknownRuleError(r, scopeMap)
		}
	}

	if optional {
		s = s.Optional()
	}

	return s, nil
}
