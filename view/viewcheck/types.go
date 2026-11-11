package viewcheck

import (
	"reflect"
)

// lookup is the outcome of resolving .name on a type.
type lookup int

const (
	// found: the name resolves and the result type is returned (possibly an
	// interface, which ends the checking of the expression).
	found lookup = iota
	// missing: the type has no such field, method or map key: V002.
	missing
	// unknown: the type is not known statically (interface or nil).
	unknown
)

var errorType = reflect.TypeFor[error]() //nolint:gochecknoglobals // read-only

// isUnknown reports whether t gives the analyser nothing to check against.
func isUnknown(t reflect.Type) bool { return t == nil || t.Kind() == reflect.Interface }

// deref strips pointers; nil stays nil.
func deref(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	return t
}

// member resolves .name on t the way text/template does: an exported
// struct field (embedded ones included), a method of t or *t, or a map key.
// It reads types only and never calls anything.
func member(t reflect.Type, name string) (reflect.Type, lookup) {
	t = deref(t)
	if isUnknown(t) {
		return nil, unknown
	}

	if t.Kind() == reflect.Struct {
		if f, ok := t.FieldByName(name); ok && f.IsExported() {
			return f.Type, found
		}
	}

	if m, ok := reflect.PointerTo(t).MethodByName(name); ok {
		return methodResult(m.Type), found
	}

	if t.Kind() == reflect.Map {
		if t.Key().Kind() != reflect.String {
			return nil, unknown
		}

		return t.Elem(), found
	}

	return nil, missing
}

// methodResult is the first result of a method with one result, or two
// when the second is an error; any other shape is not checked.
func methodResult(ft reflect.Type) reflect.Type {
	switch {
	case ft.NumOut() == 1, ft.NumOut() == 2 && ft.Out(1) == errorType:
		return ft.Out(0)
	default:
		return nil
	}
}

// rangeTypes returns the key and element types of {{range}} over t, or
// false when the iteration is not checked (functions, integers, ...).
func rangeTypes(t reflect.Type) (key, elem reflect.Type, ok bool) {
	t = deref(t)
	if t == nil {
		return nil, nil, false
	}

	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		return reflect.TypeFor[int](), t.Elem(), true
	case reflect.Map:
		return t.Key(), t.Elem(), true
	case reflect.Chan:
		return nil, t.Elem(), true
	default:
		return nil, nil, false
	}
}

// funcResult is the first result of a template function, or nil when it
// has none or returns an interface.
func funcResult(fn any) reflect.Type {
	ft := reflect.TypeOf(fn)
	if ft == nil || ft.Kind() != reflect.Func {
		return nil
	}

	if r := methodResult(ft); !isUnknown(r) {
		return r
	}

	return nil
}

// builtinResult is the result type of a text/template builtin.
func builtinResult(name string, args []reflect.Type) reflect.Type {
	switch name {
	case "len":
		return reflect.TypeFor[int]()
	case "printf", "print", "println", "html", "js", "urlquery":
		return reflect.TypeFor[string]()
	case "not", "eq", "ne", "lt", "le", "gt", "ge":
		return reflect.TypeFor[bool]()
	case "index":
		return indexResult(args)
	}

	return nil
}

// indexResult follows one level per index argument: index .Grid 0 1 is
// Grid's element's element. An unknown level makes the result unknown.
func indexResult(args []reflect.Type) reflect.Type {
	if len(args) == 0 {
		return nil
	}

	t := args[0]

	for range args[1:] {
		_, elem, ok := rangeTypes(t)
		if !ok || isUnknown(elem) {
			return nil
		}

		t = elem
	}

	return t
}

// typeName is the type as shown in messages.
func typeName(t reflect.Type) string {
	switch {
	case t == nil:
		return "unknown"
	case t.Kind() == reflect.Interface && t.NumMethod() == 0:
		return "any"
	default:
		return t.String()
	}
}
