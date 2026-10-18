package z

import "reflect"

// JSONValueSchema accepts a JSON value the application decodes itself: the
// body of an endpoint whose shape varies by a discriminator field, a
// settings blob, a webhook payload forwarded verbatim. JSON accepts any
// value; JSONObject requires an object.
//
// It is deliberately the weakest schema in the package. Everything inside
// the value is unvalidated and reaches the generated clients as an open
// object, so reach for it only where the shape genuinely is not known at
// the boundary, and validate the decoded value in the handler.
type JSONValueSchema struct {
	o      opts[any]
	object bool
}

// JSON returns a schema for any JSON value: an object, an array, a string,
// a number, or a boolean. It produces any.
func JSON() JSONValueSchema { return JSONValueSchema{} }

// JSONObject returns a schema for a JSON object with unvalidated contents.
// It produces map[string]any, so a struct field of that type takes it.
func JSONObject() JSONValueSchema { return JSONValueSchema{object: true} }

// GoType implements AnySchema.
func (s JSONValueSchema) GoType() reflect.Type {
	if s.object {
		return reflect.TypeFor[map[string]any]()
	}

	return reflect.TypeFor[any]()
}

func (s JSONValueSchema) optional() bool { return s.o.optional || s.o.nullable }

func (s JSONValueSchema) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	// emptyIsAbsent is false: "" is a JSON string, and for an object it is
	// a type error worth reporting rather than a missing key.
	return parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (any, bool) {
		if !s.object {
			return in, true
		}

		if m, ok := in.(map[string]any); ok {
			return m, true
		}

		s.o.issue(
			c,
			path,
			CodeInvalidType,
			CodeInvalidType,
			map[string]any{"expected": "an object", "received": typeName(in)},
		)

		return nil, false
	})
}

// Optional allows the key to be absent.
func (s JSONValueSchema) Optional() JSONValueSchema {
	s.o = s.o.clone()
	s.o.optional = true

	return s
}

// Nullable allows null.
func (s JSONValueSchema) Nullable() JSONValueSchema {
	s.o = s.o.clone()
	s.o.nullable = true

	return s
}

// Refine adds a custom check on the decoded value. It is the place to
// validate a payload whose shape the discriminator has just decided.
func (s JSONValueSchema) Refine(fn func(any) error) JSONValueSchema {
	s.o = s.o.clone()
	s.o.refines = append(s.o.refines, fn)

	return s
}

// Message overrides the message for an issue code.
func (s JSONValueSchema) Message(code, message string) JSONValueSchema {
	s.o = s.o.clone()
	if s.o.messages == nil {
		s.o.messages = map[string]string{}
	}

	s.o.messages[code] = message

	return s
}

// Describe sets a description used by schema exports. Use it: the schema
// says nothing about the contents, so the description is the only place a
// reader learns what belongs in the value.
func (s JSONValueSchema) Describe(text string) JSONValueSchema {
	s.o = s.o.clone()
	s.o.desc = text

	return s
}

// Parse implements Schema.
func (s JSONValueSchema) Parse(in any) (any, error) { return parseRoot[any](s, in) }

// Check implements Schema.
func (s JSONValueSchema) Check(v any) error { return checkValue(s, v) }
