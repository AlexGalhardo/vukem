package z

import (
	"fmt"
	"maps"
	"reflect"
)

// shapeOf returns the field schemas of s by key.
func (s structCore) shapeOf() Shape {
	shape := make(Shape, len(s.fields))
	for _, f := range s.fields {
		shape[f.key] = f.schema
	}

	return shape
}

// derive builds the schema of U from shape. Strict is inherited; the
// struct-level Refine, Transform, Default, Message and Passthrough are not:
// they are written for T and cannot be applied to U.
func derive[U, T any](s StructSchema[T], shape Shape) StructSchema[U] {
	core := newStructCore(reflect.TypeFor[U](), shape)
	core.strict = s.core.strict

	return StructSchema[U]{core: core}
}

// Pick returns a schema for U built from the field schemas of s named by
// keys, so the rules of a field are written once. U is the Go struct the
// result is read into; it needs a field for each key, like [Struct]. Pick
// panics when s has no such key. Struct-level Refine, Transform and Default
// of s are not carried over.
func Pick[U, T any](s StructSchema[T], keys ...string) StructSchema[U] {
	all := s.core.shapeOf()
	shape := make(Shape, len(keys))

	for _, k := range keys {
		schema, ok := all[k]
		if !ok {
			panic(fmt.Sprintf("z: Pick: %s has no key %q", s.core.rt, k))
		}

		shape[k] = schema
	}

	return derive[U](s, shape)
}

// Omit returns a schema for U built from every field schema of s except
// keys. It panics when s has no such key. Struct-level Refine, Transform and
// Default of s are not carried over.
func Omit[U, T any](s StructSchema[T], keys ...string) StructSchema[U] {
	shape := s.core.shapeOf()

	for _, k := range keys {
		if _, ok := shape[k]; !ok {
			panic(fmt.Sprintf("z: Omit: %s has no key %q", s.core.rt, k))
		}

		delete(shape, k)
	}

	return derive[U](s, shape)
}

// Extend returns a schema for U with the field schemas of s plus extra; a
// key in extra replaces the one in s. Struct-level Refine, Transform and
// Default of s are not carried over.
func Extend[U, T any](s StructSchema[T], extra Shape) StructSchema[U] {
	shape := s.core.shapeOf()
	maps.Copy(shape, extra)

	return derive[U](s, shape)
}

// Partial returns a schema for U in which every field of s may be absent:
// the schema of an update built from the one of a create, with the same
// rules for the fields that are sent. Give U pointer fields (*string) to
// tell an absent field from a zero one; a plain field keeps its zero value.
// Struct-level Refine, Transform and Default of s are not carried over.
func Partial[U, T any](s StructSchema[T]) StructSchema[U] {
	shape := s.core.shapeOf()
	for k, schema := range shape {
		shape[k] = schema.asOptional()
	}

	return derive[U](s, shape)
}
