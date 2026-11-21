package z

import (
	"reflect"
	"strconv"
	"time"
)

// Unrecognized lists the keys of in, a decoded JSON object, that the tag
// schema of t ([For]) does not read, at any depth: nested structs, slices of
// structs and maps of structs included. Each is an Issue with the
// [CodeUnrecognizedKeys] code and the dotted path of the key ("tags.1.nmae").
// It returns nil when every key is known, in is not an object, or t is not a
// struct. Free-form values (any, map[string]any) and time.Time have no
// declared keys and are not walked.
//
// [StructSchema.Strict] refuses unknown keys too, but only at its own level
// and over the whole input; this checks a body apart from the query string.
func Unrecognized(t reflect.Type, in any) Issues {
	var out Issues

	unrecognized(&out, t, in, "")

	return out
}

func unrecognized(out *Issues, t reflect.Type, in any, path string) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		if t == reflect.TypeFor[time.Time]() {
			return
		}

		m, ok := in.(map[string]any)
		if !ok {
			return
		}

		unrecognizedStruct(out, t, m, path)
	case reflect.Slice:
		list, ok := in.([]any)
		if !ok {
			return
		}

		for i, v := range list {
			unrecognized(out, t.Elem(), v, joinPath(path, strconv.Itoa(i)))
		}
	case reflect.Map:
		m, ok := in.(map[string]any)
		if !ok || t == reflect.TypeFor[map[string]any]() {
			return
		}

		for _, k := range sortedKeys(m) {
			unrecognized(out, t.Elem(), m[k], joinPath(path, k))
		}
	default:
	}
}

func unrecognizedStruct(out *Issues, t reflect.Type, m map[string]any, path string) {
	byKey := map[string]reflect.Type{}

	for _, f := range reflect.VisibleFields(t) {
		if !f.IsExported() || (f.Anonymous && f.Type.Kind() == reflect.Struct) {
			continue
		}

		for _, k := range fieldKeys(f) {
			if _, taken := byKey[k]; !taken {
				byKey[k] = f.Type
			}
		}
	}

	for _, k := range sortedKeys(m) {
		p := joinPath(path, k)

		ft, known := byKey[k]
		if !known {
			*out = append(*out, Issue{
				Path:    p,
				Code:    CodeUnrecognizedKeys,
				Key:     CodeUnrecognizedKeys,
				Message: render(defaultMessage(CodeUnrecognizedKeys, nil), p, nil),
			})

			continue
		}

		unrecognized(out, ft, m[k], p)
	}
}
