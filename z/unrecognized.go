package z

import (
	"reflect"
	"strconv"
	"time"
	"unicode/utf8"
)

// Bounds on the walk: the input is attacker-controlled, and a map with one
// huge key holding many unknown keys would otherwise make every issue retain
// that key twice (Path and Message).
const (
	// maxUnrecognized stops the walk after this many issues.
	maxUnrecognized = 100
	// maxPathLen bounds the accumulated path, in bytes; each join copies at
	// most this much whatever the body size.
	maxPathLen = 1024
	clipMark   = "..."
)

// Unrecognized lists the keys of in, a decoded JSON object, that the tag
// schema of t ([For]) does not read, at any depth: nested structs, slices of
// structs and maps of structs included. Each is an Issue with the
// [CodeUnrecognizedKeys] code and the dotted path of the key ("tags.1.nmae").
// It returns nil when every key is known, in is not an object, or t is not a
// struct. Free-form values (any, map[string]any) and time.Time have no
// declared keys and are not walked.
//
// The walk is bounded because the input is untrusted: it stops after 100
// issues, a key longer than 256 bytes is clipped (on a UTF-8 boundary) and
// ends in "...", and a path longer than 1024 bytes is clipped the same way.
//
// [StructSchema.Strict] refuses unknown keys too, but only at its own level
// and over the whole input; this checks a body apart from the query string.
func Unrecognized(t reflect.Type, in any) Issues {
	w := &unrecognizedWalk{}
	w.walk(t, in, "")

	return w.out
}

type unrecognizedWalk struct {
	out Issues
}

func (w *unrecognizedWalk) full() bool { return len(w.out) >= maxUnrecognized }

func (w *unrecognizedWalk) walk(t reflect.Type, in any, path string) {
	if w.full() {
		return
	}

	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}

	switch t.Kind() {
	case reflect.Struct:
		if t == reflect.TypeFor[time.Time]() {
			return
		}

		if m, ok := in.(map[string]any); ok {
			w.walkStruct(t, m, path)
		}
	case reflect.Slice:
		list, ok := in.([]any)
		if !ok {
			return
		}

		for i, v := range list {
			if w.full() {
				return
			}

			w.walk(t.Elem(), v, joinClipped(path, strconv.Itoa(i)))
		}
	case reflect.Map:
		m, ok := in.(map[string]any)
		if !ok || t == reflect.TypeFor[map[string]any]() {
			return
		}

		for _, k := range sortedKeys(m) {
			if w.full() {
				return
			}

			w.walk(t.Elem(), m[k], joinClipped(path, k))
		}
	default:
	}
}

func (w *unrecognizedWalk) walkStruct(t reflect.Type, m map[string]any, path string) {
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
		if w.full() {
			return
		}

		p := joinClipped(path, k)

		ft, known := byKey[k]
		if !known {
			w.out = append(w.out, Issue{
				Path:    p,
				Code:    CodeUnrecognizedKeys,
				Key:     CodeUnrecognizedKeys,
				Message: render(defaultMessage(CodeUnrecognizedKeys, nil), p, nil),
			})

			continue
		}

		w.walk(ft, m[k], p)
	}
}

// joinClipped is joinPath with key shown as callerKey shows it and the
// result clipped to maxPathLen, so it copies a bounded amount whatever the
// input holds.
func joinClipped(base, key string) string {
	return clip(joinPath(base, callerKey(key)), maxPathLen)
}

// clip cuts s to at most n bytes plus clipMark, never inside a UTF-8 rune.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}

	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}

	return s[:n] + clipMark
}
