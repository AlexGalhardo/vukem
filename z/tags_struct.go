package z

import (
	"errors"
	"reflect"
)

// structAny is a nested tag-derived struct.
type structAny struct {
	core structCore
}

func (s structAny) GoType() reflect.Type { return s.core.rt }
func (s structAny) optional() bool       { return s.core.o.optional }

func (s structAny) asOptional() AnySchema { s.core.o.optional = true; return s }

func (s structAny) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	o := s.core.o

	return parse(&o, c, path, in, present, false, func(c *pctx, path string, in any) (any, bool) {
		rv, ok := s.core.parse(c, path, in)
		if !ok {
			return nil, false
		}

		return rv.Interface(), true
	})
}

// sliceAny is a tag-derived list: the element type is only known as a
// reflect.Type, so it cannot be a SliceSchema[E]. core.o carries the
// modifiers (optional) as well as the message overrides.
type sliceAny struct {
	core sliceCore
}

func (s sliceAny) GoType() reflect.Type { return s.core.goType() }
func (s sliceAny) optional() bool       { return s.core.o.optional }

func (s sliceAny) asOptional() AnySchema { s.core.o.optional = true; return s }

func (s sliceAny) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	o := s.core.o

	return parse(&o, c, path, in, present, false, func(c *pctx, path string, in any) (any, bool) {
		rv, ok := s.core.parse(c, path, in)
		if !ok {
			return nil, false
		}

		return rv.Interface(), true
	})
}

// mapAny is a tag-derived map[string]V, like sliceAny.
type mapAny struct {
	core mapCore
}

func (s mapAny) GoType() reflect.Type { return s.core.goType() }
func (s mapAny) optional() bool       { return s.core.o.optional }

func (s mapAny) asOptional() AnySchema { s.core.o.optional = true; return s }

func (s mapAny) parseAny(c *pctx, path string, in any, present bool) (any, bool, bool) {
	o := s.core.o

	return parse(&o, c, path, in, present, false, func(c *pctx, path string, in any) (any, bool) {
		rv, ok := s.core.parse(c, path, in)
		if !ok {
			return nil, false
		}

		return rv.Interface(), true
	})
}

// errRawMessage refuses json.RawMessage with the alternative named. Parse
// takes already-decoded input, so the original bytes are gone by the time
// a schema could fill the field: re-encoding them changes key order and
// number formatting, and a type whose contract is "the bytes the client
// sent" must not be filled with different ones. map[string]any and any
// carry the same payload without that promise.
var errRawMessage = errors.New(
	"json.RawMessage cannot be validated: Parse receives decoded JSON, so the original bytes are gone. " +
		"Use map[string]any for an object or any for any JSON value (z.JSONObject, z.JSON), " +
		"and decode into your own type in the handler",
)
