package z

import (
	"fmt"
	"reflect"
	"strconv"
)

// TupleSchema parses a fixed-length array into the fields of a struct.
type TupleSchema[T any] struct {
	o      opts[T]
	fields []structField // key is the position, as text
}

// Tuple parses a fixed-length array into the exported fields of the struct
// T, in declaration order: the item at position i goes to the i-th exported
// field. Go has no tuples, so a small struct is the type to read into:
//
//	type Point struct{ Lat, Lng float64 }
//
//	z.Tuple[Point](z.Float().Min(-90).Max(90), z.Float().Min(-180).Max(180))
//
// An item error points at the position ("location.0"). Only the exported
// fields declared directly in T count; an embedded struct is one field. It
// panics when T is not a struct, when the number of items differs from the
// number of exported fields, or when an item's Go type does not fit its
// field.
func Tuple[T any](items ...AnySchema) TupleSchema[T] {
	rt := reflect.TypeFor[T]()
	if rt.Kind() != reflect.Struct {
		panic(fmt.Sprintf("z: Tuple needs a struct type, got %s", rt))
	}

	var exported []reflect.StructField

	for f := range rt.Fields() {
		if f.IsExported() {
			exported = append(exported, f)
		}
	}

	if len(exported) != len(items) {
		panic(fmt.Sprintf("z: %s has %d exported fields but the Tuple has %d items", rt, len(exported), len(items)))
	}

	fields := make([]structField, len(items))

	for i, item := range items {
		sf, ok := fitField(strconv.Itoa(i), exported[i].Index, item, exported[i].Type)
		if !ok {
			panic(
				fmt.Sprintf(
					"z: %s.%s is %s but item %d produces %s",
					rt,
					exported[i].Name,
					exported[i].Type,
					i,
					item.GoType(),
				),
			)
		}

		fields[i] = sf
	}

	return TupleSchema[T]{fields: fields}
}

// GoType implements AnySchema.
func (TupleSchema[T]) GoType() reflect.Type { return reflect.TypeFor[T]() }

func (s TupleSchema[T]) optional() bool { return s.o.optional || s.o.hasDef }

func (s TupleSchema[T]) asOptional() AnySchema { return s.Optional() }

func (s TupleSchema[T]) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	v, set, ok := parse(&s.o, c, path, in, present, false, func(c *pctx, path string, in any) (T, bool) {
		var zero T

		// A typed value, as Check passes it, is read field by field.
		if t, isT := in.(T); isT {
			list, err := s.positional(reflect.ValueOf(t))
			if err != nil {
				s.o.issue(c, path, CodeInvalidType, CodeInvalidType,
					map[string]any{"expected": "a list", "received": typeName(in)})

				return zero, false
			}

			in = list
		}

		items, isList := sliceCore{o: opts[any]{messages: s.o.messages}}.listItems(c, path, in)
		if !isList {
			return zero, false
		}

		if len(items) != len(s.fields) {
			code := CodeTooSmall
			if len(items) > len(s.fields) {
				code = CodeTooBig
			}

			s.o.issue(c, path, code, "exact.array", map[string]any{"len": len(s.fields)})

			return zero, false
		}

		before := len(c.issues)
		out := reflect.New(reflect.TypeFor[T]()).Elem()

		for i, f := range s.fields {
			v, set, ok := f.schema.parseAny(c, joinPath(path, f.key), items[i], true)
			if ok && set {
				f.store(out, v)
			}
		}

		res, _ := reflect.TypeAssert[T](out)

		return res, len(c.issues) == before
	})

	return v, set, ok
}

// positional lists the fields of a T value in order, each normalized to the
// generic form a decoded input would have.
func (s TupleSchema[T]) positional(rv reflect.Value) ([]any, error) {
	list := make([]any, len(s.fields))

	for i, f := range s.fields {
		item, err := normalizeValue(rv.FieldByIndex(f.index).Interface())
		if err != nil {
			return nil, err
		}

		list[i] = item
	}

	return list, nil
}

// Optional allows the key to be absent.
func (s TupleSchema[T]) Optional() TupleSchema[T] { s.o = s.o.withOptional(); return s }

// Nullable allows null.
func (s TupleSchema[T]) Nullable() TupleSchema[T] { s.o = s.o.withNullable(); return s }

// Default uses v when the key is absent.
func (s TupleSchema[T]) Default(v T) TupleSchema[T] { s.o = s.o.withDefault(v); return s }

// Refine adds a custom check on the parsed value.
func (s TupleSchema[T]) Refine(fn func(T) error) TupleSchema[T] {
	s.o = s.o.withRefine(fn)

	return s
}

// Transform maps the parsed value.
func (s TupleSchema[T]) Transform(fn func(T) T) TupleSchema[T] {
	s.o = s.o.withTransform(fn)

	return s
}

// Message overrides the message for an issue code or detailed key.
func (s TupleSchema[T]) Message(code, message string) TupleSchema[T] {
	s.o = s.o.withMessage(code, message)

	return s
}

// Describe sets a description used by schema exports.
func (s TupleSchema[T]) Describe(text string) TupleSchema[T] {
	s.o = s.o.withDescribe(text)

	return s
}

// Parse implements Schema.
func (s TupleSchema[T]) Parse(in any) (T, error) { return parseRoot[T](s, in) }

// Check implements Schema. The value is read field by field: the JSON form
// of a struct is an object, which is not what the tuple describes.
func (s TupleSchema[T]) Check(v T) error {
	list, err := s.positional(reflect.ValueOf(v))
	if err != nil {
		return fmt.Errorf("z: check: %w", err)
	}

	c := &pctx{coerce: false}
	s.parseAny(c, "", list, true)

	if len(c.issues) > 0 {
		return c.issues
	}

	return nil
}

func (s TupleSchema[T]) jsonSchema(jc *jsctx) map[string]any {
	items := make([]any, len(s.fields))
	for i, f := range s.fields {
		items[i] = jc.of(f.schema)
	}

	m := map[string]any{"prefixItems": items, "items": false, "minItems": len(items), "maxItems": len(items)}

	return s.o.decorate(m, "array")
}
