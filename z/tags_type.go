package z

import (
	"encoding/json"
	"fmt"
	"reflect"
	"time"
)

func schemaForType(t reflect.Type, raw []string, depth int) (AnySchema, error) {
	rs := parseRules(raw)
	if err := rs.coherent(); err != nil {
		return nil, err
	}

	nullable := false

	if t.Kind() == reflect.Pointer {
		nullable = true
		t = t.Elem()
	}

	optional := !rs.required || nullable

	if rs.omitEmpty && t.Kind() != reflect.String && t != reflect.TypeFor[json.RawMessage]() {
		// Refused rather than ignored. "omitempty" has an unambiguous
		// meaning only where the empty value is not itself a datum people
		// bound: on a number it would make "omitempty,min=1" accept 0, and
		// on a slice "omitempty,min=1" accept the empty list, which is the
		// opposite of what the tag says. Accepting it silently is how an
		// optional enum came to be rejected in the first place (A111).
		return nil, fmt.Errorf(
			"rule \"omitempty\" applies to a string, not %s "+
				"(it skips the later rules when the value is empty; drop it, or use a pointer for an absent value)",
			t,
		)
	}

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
		return listFromRules(t, rs, optional, depth)
	case t.Kind() == reflect.Map:
		return mapFromRules(t, rs, optional, depth)
	case t.Kind() == reflect.Struct:
		if err := noRules(rs.rules); err != nil {
			return nil, err
		}

		core := tagCore(t, depth+1)
		core.o.optional = optional

		return structAny{core: core}, nil
	default:
		return nil, fmt.Errorf(
			"unsupported type %s, want a string, number, bool, time.Time, any, map[string]any, "+
				"map[string]V, slice or struct (or a pointer to one)", t)
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
