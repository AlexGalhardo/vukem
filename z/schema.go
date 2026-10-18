package z

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"reflect"
	"slices"
)

// AnySchema is implemented by every schema in this package. Composite
// schemas (Slice, Map, Shape fields) accept any schema through it.
type AnySchema interface {
	// GoType is the Go type the schema produces.
	GoType() reflect.Type
	parseAny(c *pctx, path string, in any, present bool) (v any, set, ok bool)
	optional() bool
}

// Schema is a schema that produces values of type T.
type Schema[T any] interface {
	AnySchema
	// Parse converts untrusted input (decoded JSON, form maps, strings)
	// into T. The error is an Issues value listing every problem.
	Parse(in any) (T, error)
	// Check validates an already typed value, e.g. data about to be sent
	// in a response. It uses the same rules as Parse.
	Check(v T) error
}

// pctx carries the state of one Parse call.
type pctx struct {
	issues Issues
	coerce bool
}

func (c *pctx) add(path, code string, msg string, params map[string]any) {
	c.addKeyed(path, code, "", msg, params)
}

// addErr records the error a Refine callback or Custom parser returned at
// path, honoring FieldError and FieldErrorKey; any other error gets the
// generic "invalid" message and is kept in Issue.Err.
func (c *pctx) addErr(path string, err error) {
	fe, ok := errors.AsType[*fieldError](err)
	if !ok {
		// Fail closed: the text of an arbitrary error may be internal.
		c.issues = append(c.issues, Issue{
			Path:    path,
			Code:    CodeCustom,
			Key:     keyInvalid,
			Message: render(defaultMessages[keyInvalid], path, nil),
			Err:     err,
		})

		return
	}

	if fe.path != "" {
		path = joinPath(path, fe.path)
	}

	if fe.key == "" {
		c.add(path, CodeCustom, fe.message, nil)
		return
	}

	c.addKeyed(path, CodeCustom, fe.key, render(fe.message, path, fe.params), cloneParams(fe.params))
}

// addKeyed records an issue whose message came from defaultMessages[key],
// so Issues.Localize can rebuild it from a translated template.
func (c *pctx) addKeyed(path, code, key, msg string, params map[string]any) {
	c.issues = append(c.issues, Issue{Path: path, Code: code, Key: key, Message: msg, Params: params})
}

// opts are the modifiers shared by every schema.
type opts[T any] struct {
	optional   bool
	nullable   bool
	hasDef     bool
	def        T
	refines    []func(T) error
	transforms []func(T) T
	messages   map[string]string
	desc       string
	kw         map[string]any // JSON Schema keywords recorded by builders
}

func (o opts[T]) clone() opts[T] {
	o.refines = slices.Clip(o.refines)
	o.transforms = slices.Clip(o.transforms)

	if o.messages != nil {
		o.messages = maps.Clone(o.messages)
	}

	o.kw = maps.Clone(o.kw)

	return o
}

// issue records a problem with a message from the schema's overrides
// (by code or by detailed key) or the defaults.
func (o *opts[T]) issue(c *pctx, path, code, key string, params map[string]any) {
	tmpl, ok := o.messages[key]
	if !ok {
		tmpl, ok = o.messages[code]
	}

	if ok {
		c.add(path, code, render(tmpl, path, params), cloneParams(params))
		return
	}

	if _, ok = defaultMessages[key]; !ok {
		key = code
	}

	c.addKeyed(path, code, key, render(defaultMessage(key, params), path, params), cloneParams(params))
}

// parse runs the common pipeline around core: absence/null handling,
// defaults, the type-specific core, transforms and refinements.
func parse[T any](o *opts[T], c *pctx, path string, in any, present bool, emptyIsAbsent bool,
	core func(c *pctx, path string, in any) (T, bool),
) (T, bool, bool) {
	var zero T

	if s, isStr := in.(string); isStr && s == "" && emptyIsAbsent {
		present = false
	}

	if !present || in == nil {
		switch {
		case present && in == nil && o.nullable:
			return zero, false, true
		case o.hasDef:
			return o.def, true, true
		case o.optional || o.nullable:
			return zero, false, true
		}

		o.issue(c, path, CodeRequired, CodeRequired, nil)

		return zero, false, false
	}

	v, ok := core(c, path, in)
	if !ok {
		return zero, false, false
	}

	for _, t := range o.transforms {
		v = t(v)
	}

	before := len(c.issues)

	for _, r := range o.refines {
		if err := r(v); err != nil {
			c.addErr(path, err)
		}
	}

	return v, true, len(c.issues) == before
}

// parseRoot runs a schema on a root value and converts the result to T.
func parseRoot[T any](s AnySchema, in any) (T, error) {
	c := &pctx{coerce: true}

	v, set, ok := s.parseAny(c, "", in, true)
	if !ok || len(c.issues) > 0 {
		var zero T
		return zero, c.issues
	}

	if !set {
		var zero T
		return zero, nil
	}

	out, _ := v.(T)

	return out, nil
}

// normalizeValue round-trips v through JSON into generic input.
func normalizeValue(v any) (any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}

	return decodeJSON(bytes.NewReader(b))
}

// checkValue validates a typed value by round-tripping it through JSON, so
// outgoing data is checked against the same rules as incoming data.
func checkValue(s AnySchema, v any) error {
	in, err := normalizeValue(v)
	if err != nil {
		return fmt.Errorf("z: check: %w", err)
	}

	c := &pctx{coerce: false}
	s.parseAny(c, "", in, true)

	if len(c.issues) > 0 {
		return c.issues
	}

	return nil
}

func decodeJSON(r io.Reader) (any, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber()

	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}

	return v, nil
}

// ParseJSON decodes JSON from r and parses it with s.
func ParseJSON[T any](r io.Reader, s Schema[T]) (T, error) {
	in, err := decodeJSON(r)
	if err != nil {
		var zero T
		return zero, Issues{{Code: CodeInvalidType, Message: "The value must be valid JSON."}}
	}

	return s.Parse(in)
}

// typeName describes a JSON value's type for invalid_type messages.
func typeName(in any) string {
	switch in.(type) {
	case string:
		return "string"
	case bool:
		return "boolean"
	case json.Number, float64, float32, int, int64, int32, uint, uint64:
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", in)
	}
}
