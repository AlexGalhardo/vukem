package z

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// crossTag is a cross-field rule found on the field read from owner.
type crossTag struct {
	owner string // input key of the field the tag is on
	rule  string // "name" or "name=arg"
}

// splitCross separates the cross-field rules of a field from the rest. They
// are not rules of the field's own schema but of the struct around it, and
// they cite other fields by their input key.
func splitCross(rules []string) (own, cross []string) {
	for _, r := range rules {
		name, _, _ := strings.Cut(strings.TrimSpace(r), "=")

		switch name {
		case "confirmed", "same", "different", "required_if",
			"required_with", "required_with_all", "required_without", "required_without_all",
			"gtfield", "gtefield", "ltfield", "ltefield":
			cross = append(cross, strings.TrimSpace(r))
		default:
			own = append(own, r)
		}
	}

	return own, cross
}

// crossRule turns a cross-field tag into a struct rule. It panics, like the
// builder methods, for a key that is not in the shape.
func (s structCore) crossRule(ct crossTag) (structRule, error) {
	empty := structRule{}

	name, arg, _ := strings.Cut(ct.rule, "=")
	keys := strings.Fields(arg)

	needsArg := name != "confirmed"
	if needsArg && len(keys) == 0 {
		return empty, fmt.Errorf("rule %q needs the key of another field", name)
	}

	switch name {
	case "confirmed":
		return s.confirmed(ct.owner), nil
	case "same":
		return s.sameOrDifferent("same", ct.owner, keys[0], true), nil
	case "different":
		return s.sameOrDifferent("different", ct.owner, keys[0], false), nil
	case "required_if":
		if len(keys) < 2 {
			return empty, fmt.Errorf("rule %q needs a field and at least one value: required_if=kind company", name)
		}

		values := make([]any, len(keys)-1)
		for i, v := range keys[1:] {
			values[i] = v
		}

		return s.requiredIf(ct.owner, keys[0], values), nil
	case "required_with":
		return s.requiredWith(name, "required_with", ct.owner, keys, anyOf), nil
	case "required_with_all":
		return s.requiredWith(name, "required_with", ct.owner, keys, all), nil
	case "required_without":
		return s.requiredWith(name, "required_without", ct.owner, keys,
			func(present []bool) bool { return !all(present) }), nil
	case "required_without_all":
		return s.requiredWith(name, "required_without", ct.owner, keys, none), nil
	case "gtfield":
		return s.dateRule(name, "date.after_field", CodeTooSmall, ct.owner, keys[0],
			func(a, b time.Time) bool { return !a.After(b) }), nil
	case "gtefield":
		return s.dateRule(name, "date.after_or_equal_field", CodeTooSmall, ct.owner, keys[0],
			func(a, b time.Time) bool { return a.Before(b) }), nil
	case "ltfield":
		return s.dateRule(name, "date.before_field", CodeTooBig, ct.owner, keys[0],
			func(a, b time.Time) bool { return !a.Before(b) }), nil
	case "ltefield":
		return s.dateRule(name, "date.before_or_equal_field", CodeTooBig, ct.owner, keys[0],
			func(a, b time.Time) bool { return a.After(b) }), nil
	}

	return empty, fmt.Errorf("unknown cross-field rule %q", name)
}

// withCrossRules adds the rules of the tags to the core, in the order of the
// fields' input keys, so the issues come out in a stable order.
func (s structCore) withCrossRules(rt string, tags []crossTag) structCore {
	slices.SortStableFunc(tags, func(a, b crossTag) int { return strings.Compare(a.owner, b.owner) })

	for _, ct := range tags {
		rule, err := s.crossRule(ct)
		if err != nil {
			panic(fmt.Sprintf("z: %s.%s: %v", rt, ct.owner, err))
		}

		s = s.with(rule)
	}

	return s
}
