package graphql

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/AlexGalhardo/vukem/z"
)

// NoArgs is the Args type of fields without arguments.
type NoArgs struct{}

// Validator is implemented by Args types that check themselves after
// decoding; a returned error is reported as BAD_USER_INPUT.
type Validator interface {
	Validate() error
}

// Field binds a resolver to typeName.fieldName. P is the parent value as
// returned by the parent field's resolver (T and *T convert both ways),
// A is the arguments decoded into a Go value through their json tags and
// checked with `validate` tags (package z) or a Validator, and O is
// serialized per the selection set.
func Field[P, A, O any](
	typeName, fieldName string, fn func(ctx context.Context, parent P, args A) (O, error), opts ...FieldOption,
) Option {
	return optionFunc(func(b *builder) error {
		decode, names, err := argsDecoder[A]()
		if err != nil {
			return fmt.Errorf("graphql: binding %s.%s: %w", typeName, fieldName, err)
		}

		bd := binding{typeName: typeName, field: fieldName, argNames: names}
		bd.resolve = func(ctx context.Context, parent any, args map[string]any) (any, error) {
			p, ok := convertParent[P](parent)
			if !ok {
				return nil, fmt.Errorf(
					"resolver %s.%s: parent is %T, want %s",
					typeName,
					fieldName,
					parent,
					reflect.TypeFor[P](),
				)
			}

			a, err := decode(args)
			if err != nil {
				return nil, err
			}

			return fn(ctx, p, a)
		}

		for _, o := range opts {
			o(&bd)
		}

		b.bindings = append(b.bindings, bd)

		return nil
	})
}

// Query binds a field of the query root type.
func Query[A, O any](fieldName string, fn func(ctx context.Context, args A) (O, error), opts ...FieldOption) Option {
	return root(opQuery, fieldName, fn, opts)
}

// Mutation binds a field of the mutation root type. Mutation root fields
// run one after another, in document order.
func Mutation[A, O any](fieldName string, fn func(ctx context.Context, args A) (O, error), opts ...FieldOption) Option {
	return root(opMutation, fieldName, fn, opts)
}

func root[A, O any](k opKind, fieldName string, fn func(context.Context, A) (O, error), opts []FieldOption) Option {
	return optionFunc(func(b *builder) error {
		wrapped := func(ctx context.Context, _ any, a A) (O, error) { return fn(ctx, a) }

		return Field(b.rootName(k), fieldName, wrapped, opts...).apply(b)
	})
}

// argsDecoder returns a decoder from coerced arguments to A, and the
// argument names A declares (to check them against the SDL at boot).
func argsDecoder[A any]() (func(map[string]any) (A, error), []string, error) {
	rt := reflect.TypeFor[A]()

	if rt == reflect.TypeFor[NoArgs]() || (rt.Kind() == reflect.Struct && rt.NumField() == 0) {
		return func(map[string]any) (A, error) { var a A; return a, nil }, nil, nil
	}

	if rt == reflect.TypeFor[map[string]any]() {
		return func(m map[string]any) (A, error) {
			a, _ := any(m).(A)
			return a, nil
		}, nil, nil
	}

	if rt.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("args type %s must be a struct, map[string]any or graphql.NoArgs", rt)
	}

	names := jsonNames(rt)

	var schema z.Schema[A]
	if hasValidateTags(rt) {
		s, err := tagSchema[A]()
		if err != nil {
			return nil, nil, err
		}

		schema = s
	}

	return func(m map[string]any) (A, error) {
		var a A

		raw, err := json.Marshal(m)
		if err != nil {
			return a, fmt.Errorf("encode arguments: %w", err)
		}

		if err := json.Unmarshal(raw, &a); err != nil {
			return a, inputError("Invalid arguments: " + jsonErrorText(err))
		}

		if schema != nil {
			if err := schema.Check(a); err != nil {
				return a, err
			}
		}

		if v, ok := any(&a).(Validator); ok {
			if err := v.Validate(); err != nil {
				return a, asInputError(err)
			}
		} else if v, ok := any(a).(Validator); ok {
			if err := v.Validate(); err != nil {
				return a, asInputError(err)
			}
		}

		return a, nil
	}, names, nil
}

// asInputError keeps typed client errors and marks plain ones as
// BAD_USER_INPUT (a Validator speaks about its own input).
func asInputError(err error) error {
	if isIssues(err) || isValidation(err) {
		return err
	}

	if _, ok := err.(*Error); ok { //nolint:errorlint // only an unwrapped *Error keeps its own code
		return err
	}

	return &Error{Message: err.Error(), Extensions: map[string]any{"code": CodeBadUserInput}, cause: err}
}

func tagSchema[A any]() (s z.Schema[A], err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("validate tags: %v", r)
		}
	}()

	return z.For[A](), nil
}

func hasValidateTags(rt reflect.Type) bool {
	for _, f := range reflect.VisibleFields(rt) {
		if _, ok := f.Tag.Lookup("validate"); ok {
			return true
		}
	}

	return false
}

func jsonErrorText(err error) string {
	if te, ok := err.(*json.UnmarshalTypeError); ok { //nolint:errorlint // json returns it unwrapped
		return fmt.Sprintf("%s must be %s", te.Field, te.Type)
	}

	return "cannot decode"
}

// jsonNames lists the JSON names of rt's exported fields.
func jsonNames(rt reflect.Type) []string {
	var names []string

	for _, f := range reflect.VisibleFields(rt) {
		if !f.IsExported() || f.Anonymous {
			continue
		}

		name, skip := jsonName(f)
		if !skip {
			names = append(names, name)
		}
	}

	return names
}

func jsonName(f reflect.StructField) (string, bool) {
	tag := f.Tag.Get("json")
	if tag == "-" {
		return "", true
	}

	if name, _, _ := strings.Cut(tag, ","); name != "" {
		return name, false
	}

	return f.Name, false
}

// convertParent adapts the parent value to P: T ↔ *T, nil for roots.
func convertParent[P any](v any) (P, bool) {
	if p, ok := v.(P); ok {
		return p, true
	}

	var zero P

	if v == nil {
		return zero, reflect.TypeFor[P]().Kind() == reflect.Interface || reflect.TypeFor[P]().Kind() == reflect.Pointer
	}

	pt := reflect.TypeFor[P]()
	rv := reflect.ValueOf(v)

	switch {
	case rv.Kind() == reflect.Pointer && rv.Type().Elem() == pt:
		if rv.IsNil() {
			return zero, false
		}

		p, ok := reflect.TypeAssert[P](rv.Elem())

		return p, ok
	case pt.Kind() == reflect.Pointer && pt.Elem() == rv.Type():
		ptr := reflect.New(rv.Type())
		ptr.Elem().Set(rv)
		p, ok := reflect.TypeAssert[P](ptr)

		return p, ok
	}

	return zero, false
}

// property is the default resolver: a map key, or a struct field matched
// by json name, then by case-insensitive Go name (tagged fields included).
func (s *Schema) property(parent any, name string) (any, error) {
	if parent == nil {
		return nil, nil
	}

	if m, ok := parent.(map[string]any); ok {
		return m[name], nil
	}

	rv := reflect.ValueOf(parent)
	for rv.Kind() == reflect.Pointer || rv.Kind() == reflect.Interface {
		if rv.IsNil() {
			return nil, nil
		}

		rv = rv.Elem()
	}

	switch rv.Kind() { //nolint:exhaustive // the remaining kinds take the common path below
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			break
		}

		v := rv.MapIndex(reflect.ValueOf(name).Convert(rv.Type().Key()))
		if !v.IsValid() {
			return nil, nil
		}

		return v.Interface(), nil
	case reflect.Struct:
		fields := s.fieldIndex(rv.Type())

		idx, ok := fields[name]
		if !ok {
			idx, ok = fields[foldPrefix+strings.ToLower(name)]
		}
		if !ok {
			return nil, fmt.Errorf("no field %q on Go type %s (bind a resolver or add a json tag)", name, rv.Type())
		}

		f, err := rv.FieldByIndexErr(idx)
		if err != nil {
			return nil, nil //nolint:nilerr // a nil embedded pointer means the field is null
		}

		return f.Interface(), nil
	}

	return nil, fmt.Errorf("cannot read field %q from Go type %s", name, rv.Type())
}

// foldPrefix marks case-folded keys; it cannot start a GraphQL name.
const foldPrefix = "~"

func (s *Schema) fieldIndex(rt reflect.Type) map[string][]int {
	if v, ok := s.props.Load(rt); ok {
		m, _ := v.(map[string][]int)
		return m
	}

	m := map[string][]int{}
	folded := map[string][]int{}

	for _, f := range reflect.VisibleFields(rt) {
		if !f.IsExported() || f.Anonymous {
			continue
		}

		name, skip := jsonName(f)
		if skip {
			continue
		}

		if _, dup := m[name]; !dup {
			m[name] = f.Index
		}

		if _, dup := folded[strings.ToLower(f.Name)]; !dup {
			folded[strings.ToLower(f.Name)] = f.Index
		}
	}

	// Go names, tagged or not, also match case-insensitively (ID → id,
	// PriceCents `json:"price_cents"` → priceCents). Exact json names are
	// looked up first, so they win over another field's Go name.
	for k, v := range folded {
		m[foldPrefix+k] = v
	}

	s.props.Store(rt, m)

	return m
}
