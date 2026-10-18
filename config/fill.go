package config

import (
	"encoding"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

// Lookuper resolves configuration keys. *Source implements it.
type Lookuper interface {
	Lookup(key string) (string, bool)
}

// LookupErr reads key from src like Lookup, and also returns the error of
// an "@file:" process variable whose file cannot be read when src is a
// *Source (Lookup then reports the key set and empty). Code that reads
// configuration outside Load and Fill uses it to fail closed.
func LookupErr(src Lookuper, key string) (string, bool, error) {
	if s, ok := src.(*Source); ok {
		return s.lookupErr(key)
	}

	v, ok := src.Lookup(key)

	return v, ok, nil
}

// Load returns a T filled from src. See [Fill].
func Load[T any](src Lookuper) (T, error) {
	var v T
	err := Fill(&v, src)

	return v, err
}

// Fill sets the exported fields of the struct pointed to by dst from src.
//
// A field is read from the key in its `env` tag, falling back to its
// `default` tag. Struct fields without an `env` tag are filled
// recursively. Supported kinds: string, bool, ints, uints, floats,
// time.Duration, []string (comma-separated), *url.URL, Secret and any
// encoding.TextUnmarshaler. All conversion errors are returned together.
func Fill(dst any, src Lookuper) error {
	rv := reflect.ValueOf(dst)
	if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
		return fmt.Errorf("config: Fill needs a pointer to a struct, got %T", dst)
	}

	var errs []error

	fillStruct(rv.Elem(), src, &errs)

	return errors.Join(errs...)
}

var (
	textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()
	durationType        = reflect.TypeFor[time.Duration]()
	urlPtrType          = reflect.TypeFor[*url.URL]()
	secretType          = reflect.TypeFor[Secret]()
)

func fillStruct(v reflect.Value, src Lookuper, errs *[]error) {
	t := v.Type()

	for i := range t.NumField() {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}

		fv := v.Field(i)
		key, hasKey := f.Tag.Lookup("env")

		if !hasKey {
			if f.Type.Kind() == reflect.Struct && f.Type != secretType {
				fillStruct(fv, src, errs)
			}

			continue
		}

		raw, ok, err := LookupErr(src, key)
		if err != nil {
			*errs = append(*errs, err)
			continue
		}

		if !ok || raw == "" {
			def, hasDef := f.Tag.Lookup("default")
			if !hasDef {
				continue
			}

			raw = def
		}

		if err := setValue(fv, raw); err != nil {
			*errs = append(*errs, fmt.Errorf("config: %s (%s): %w", key, f.Name, err))
		}
	}
}

func setValue(v reflect.Value, raw string) error {
	switch {
	case v.Type() == secretType:
		v.Set(reflect.ValueOf(NewSecret(raw)))
		return nil
	case v.Type() == durationType:
		d, err := time.ParseDuration(raw)
		if err != nil {
			return err
		}

		v.SetInt(int64(d))

		return nil
	case v.Type() == urlPtrType:
		u, err := url.Parse(raw)
		if err != nil {
			return err
		}

		v.Set(reflect.ValueOf(u))

		return nil
	case reflect.PointerTo(v.Type()).Implements(textUnmarshalerType):
		u, _ := reflect.TypeAssert[encoding.TextUnmarshaler](v.Addr())
		return u.UnmarshalText([]byte(raw))
	}

	switch v.Kind() {
	case reflect.String:
		v.SetString(raw)
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("invalid boolean %q", raw)
		}

		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid integer %q", raw)
		}

		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 10, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid unsigned integer %q", raw)
		}

		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		n, err := strconv.ParseFloat(raw, v.Type().Bits())
		if err != nil {
			return fmt.Errorf("invalid number %q", raw)
		}

		v.SetFloat(n)
	case reflect.Slice:
		if v.Type().Elem().Kind() != reflect.String {
			return fmt.Errorf("unsupported slice type %s", v.Type())
		}

		var items []string

		for p := range strings.SplitSeq(raw, ",") {
			if p = strings.TrimSpace(p); p != "" {
				items = append(items, p)
			}
		}

		v.Set(reflect.ValueOf(items))
	default:
		return fmt.Errorf("unsupported type %s", v.Type())
	}

	return nil
}
