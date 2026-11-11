package z

import "reflect"

// CodecSchema reads a wire form In and produces a domain value Out, and can
// go back.
type CodecSchema[In, Out any] struct {
	o      opts[Out]
	wire   Schema[In]
	decode func(In) (Out, error)
	encode func(Out) (In, error)
}

// Codec returns a schema that parses the wire form with wire, then turns it
// into the domain type Out with decode; encode goes the other way. It is the
// transform that changes type: [Transform] keeps T, and a different output
// type needs the way back for Check and for the JSON Schema, which a codec
// declares.
//
//	// Seconds on the wire, a time.Duration in the domain.
//	var TTL = z.Codec(z.Int64().Min(0),
//		func(sec int64) (time.Duration, error) { return time.Duration(sec) * time.Second, nil },
//		func(d time.Duration) (int64, error) { return int64(d / time.Second), nil },
//	)
//
//	d, err := TTL.Parse(json.Number("90")) // 1m30s
//	sec, err := TTL.Encode(d)              // 90
//
// An error from decode becomes an issue like a Refine error does:
// [FieldError] and [FieldErrorKey] are honored, any other error gets the
// generic "invalid" message and stays in Issue.Err for the server logs. The
// wire schema's own issues are reported as they are. A Default is a value of
// Out and is not decoded; modifiers go on the codec, not on the wire schema.
// The JSON Schema is the wire schema's, because that is the form that
// travels. It panics when wire, decode or encode is nil.
func Codec[In, Out any](
	wire Schema[In],
	decode func(In) (Out, error),
	encode func(Out) (In, error),
) CodecSchema[In, Out] {
	if wire == nil || decode == nil || encode == nil {
		panic("z: Codec needs a wire schema, decode and encode")
	}

	return CodecSchema[In, Out]{wire: wire, decode: decode, encode: encode}
}

// GoType implements AnySchema.
func (CodecSchema[In, Out]) GoType() reflect.Type { return reflect.TypeFor[Out]() }

func (s CodecSchema[In, Out]) optional() bool { return s.o.optional || s.o.hasDef }

func (s CodecSchema[In, Out]) asOptional() AnySchema { return s.Optional() }

func (s CodecSchema[In, Out]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (Out, bool) {
		var zero Out

		wire, set, ok := s.wire.parseAny(c, path, in, true)
		if !ok || !set {
			return zero, false
		}

		decoded, _ := wire.(In)

		out, err := s.decode(decoded)
		if err != nil {
			c.addErr(path, err)

			return zero, false
		}

		return out, true
	})

	return v, set, ok
}

// Optional allows the key to be absent.
func (s CodecSchema[In, Out]) Optional() CodecSchema[In, Out] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s CodecSchema[In, Out]) Nullable() CodecSchema[In, Out] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent; it is a value of Out, so it does not
// go through decode.
func (s CodecSchema[In, Out]) Default(v Out) CodecSchema[In, Out] { s.o = s.o.withDefault(v); return s }

// Refine adds a custom check on the decoded value.
func (s CodecSchema[In, Out]) Refine(fn func(Out) error) CodecSchema[In, Out] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the decoded value.
func (s CodecSchema[In, Out]) Transform(fn func(Out) Out) CodecSchema[In, Out] {
	s.o = s.o.withTransform(fn)

	return s
}

// Message overrides the message for an issue code of the codec itself (the
// wire schema keeps its own).
func (s CodecSchema[In, Out]) Message(code, message string) CodecSchema[In, Out] {
	s.o = s.o.withMessage(code, message)

	return s
}

// Describe sets a description used by schema exports.
func (s CodecSchema[In, Out]) Describe(text string) CodecSchema[In, Out] {
	s.o = s.o.withDescribe(text)

	return s
}

// Parse implements Schema: the wire schema, then decode.
func (s CodecSchema[In, Out]) Parse(in any) (Out, error) { return parseRoot[Out](s, in) }

// Encode turns a domain value into its wire form with encode and validates
// the result with the wire schema, so a value that could not have been read
// is not sent either.
func (s CodecSchema[In, Out]) Encode(v Out) (In, error) {
	wire, err := s.encode(v)
	if err != nil {
		var zero In

		return zero, err
	}

	if err := s.wire.Check(wire); err != nil {
		var zero In

		return zero, err
	}

	return wire, nil
}

// Check implements Schema: v is valid when it encodes to a valid wire value.
func (s CodecSchema[In, Out]) Check(v Out) error {
	_, err := s.Encode(v)

	return err
}

func (s CodecSchema[In, Out]) jsonSchema(jc *jsctx) map[string]any {
	m := jc.of(s.wire)
	if s.o.desc != "" {
		m["description"] = s.o.desc
	}

	return m
}
