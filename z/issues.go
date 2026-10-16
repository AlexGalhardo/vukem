// Package z parses untrusted input into typed Go values with Zod-inspired
// schemas, and checks outgoing values against the same contracts.
//
//	var Login = z.Struct[LoginInput](z.Shape{
//		"email":    z.String().Trim().Email(),
//		"password": z.String().Min(12),
//	})
//
//	in, err := Login.Parse(map[string]any{"email": "a@b.co", "password": "..."})
//
// Schemas are immutable values: every modifier returns a new schema, so a
// schema can be shared by any number of goroutines.
package z

import (
	"errors"
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var placeholder = regexp.MustCompile(`\{[a-z_]+\}`)

// Issue codes.
const (
	CodeRequired         = "required"
	CodeInvalidType      = "invalid_type"
	CodeTooSmall         = "too_small"
	CodeTooBig           = "too_big"
	CodeInvalidString    = "invalid_string"
	CodeInvalidEnum      = "invalid_enum"
	CodeUnrecognizedKeys = "unrecognized_keys"
	CodeNotMultiple      = "not_multiple_of"
	CodeInvalidDate      = "invalid_date"
	CodeNotUnique        = "not_unique"
	CodeCustom           = "custom"
)

// Issue is one problem found while parsing.
type Issue struct {
	// Path is the dotted path to the value: "author.email", "tags.2".
	Path string `json:"path"`
	// Code is a stable machine-readable code (see Code* constants).
	Code string `json:"code"`
	// Message is a human-readable message.
	Message string `json:"message"`
	// Key is the message key Message was built from ("required",
	// "too_small.string", "string.email"...); empty for custom messages
	// (Message overrides, Refine, Custom). Localize uses it.
	Key string `json:"-"`
	// Params holds the values used by the message, e.g. {"min": 3}.
	Params map[string]any `json:"params,omitempty"`
}

// Issues is the error returned by Parse. It lists every issue found, not
// only the first.
type Issues []Issue //nolint:errname // mirrors Zod; reads naturally as a list of issues

// Error implements error.
func (is Issues) Error() string {
	parts := make([]string, 0, len(is))
	for _, i := range is {
		p := i.Path
		if p == "" {
			p = "(root)"
		}

		parts = append(parts, p+": "+i.Message)
	}

	return "z: " + strings.Join(parts, "; ")
}

// Fields groups messages by path, the shape used by validation responses.
func (is Issues) Fields() map[string][]string {
	out := make(map[string][]string, len(is))
	for _, i := range is {
		key := i.Path
		if key == "" {
			key = "_"
		}

		out[key] = append(out[key], i.Message)
	}

	return out
}

// First returns the first message for path, or "".
func (is Issues) First(path string) string {
	for _, i := range is {
		if i.Path == path {
			return i.Message
		}
	}

	return ""
}

// AsIssues extracts Issues from err.
func AsIssues(err error) (Issues, bool) {
	var is Issues
	ok := errors.As(err, &is)

	return is, ok
}

// FieldError returns an error for Refine callbacks that points at a field
// path relative to the refined value.
func FieldError(path, message string) error {
	return &fieldError{path: path, message: message}
}

type fieldError struct {
	path    string
	message string
}

func (e *fieldError) Error() string { return e.path + ": " + e.message }

var defaultMessages = map[string]string{
	CodeRequired:         "The {field} field is required.",
	CodeInvalidType:      "The {field} field must be {expected}.",
	CodeInvalidEnum:      "The selected {field} is invalid.",
	CodeUnrecognizedKeys: "The {field} field is not allowed.",
	CodeNotMultiple:      "The {field} field must be a multiple of {multiple}.",
	CodeInvalidDate:      "The {field} field must be a valid date.",
	CodeNotUnique:        "The {field} field must not contain duplicates.",
	"too_small.string":   "The {field} field must be at least {min} characters.",
	"too_small.number":   "The {field} field must be at least {min}.",
	"too_small.positive": "The {field} field must be greater than 0.",
	"too_small.array":    "The {field} field must have at least {min} items.",
	"too_small.date":     "The {field} field must be after {min}.",
	"too_big.string":     "The {field} field must not be greater than {max} characters.",
	"too_big.number":     "The {field} field must not be greater than {max}.",
	"too_big.array":      "The {field} field must not have more than {max} items.",
	"too_big.date":       "The {field} field must be before {max}.",
	"exact.string":       "The {field} field must be {len} characters.",
	"exact.array":        "The {field} field must have {len} items.",
	"string.email":       "The {field} field must be a valid email address.",
	"string.url":         "The {field} field must be a valid URL.",
	"string.uuid":        "The {field} field must be a valid UUID.",
	"string.ulid":        "The {field} field must be a valid ULID.",
	"string.regex":       "The {field} field format is invalid.",
	"string.alpha":       "The {field} field must only contain letters.",
	"string.alphanum":    "The {field} field must only contain letters and numbers.",
	"string.numeric":     "The {field} field must be a number.",
	"string.starts_with": "The {field} field must start with {prefix}.",
	"string.ends_with":   "The {field} field must end with {suffix}.",
	"string.includes":    "The {field} field must contain {substring}.",
	"string.utf8":        "The {field} field must be valid text.",
}

// Localize returns a copy of is with each built-in message rebuilt from the
// template lookup returns for the issue's Key, then its Code (for example a
// catalog's "validation.too_small.string" and "validation.too_small").
// {field} becomes lookup("attributes.<name>") when present, where name is
// the last path segment ("first_name"), or the humanized name otherwise.
// Issues without a template, and custom messages, are kept unchanged.
func (is Issues) Localize(lookup func(key string) (string, bool)) Issues {
	out := slices.Clone(is)
	if lookup == nil {
		return out
	}

	for i, iss := range out {
		if iss.Key == "" {
			continue
		}

		tmpl, ok := lookup(iss.Key)
		if !ok && iss.Code != iss.Key {
			tmpl, ok = lookup(iss.Code)
		}

		if !ok {
			continue
		}

		field := fieldName(iss.Path)
		if segs := strings.Split(iss.Path, "."); iss.Path != "" {
			if name, found := lookup("attributes." + segs[len(segs)-1]); found {
				field = name
			}
		}

		out[i].Message = renderField(tmpl, field, iss.Params)
	}

	return out
}

// render fills {placeholders} in tmpl.
func render(tmpl, path string, params map[string]any) string {
	return renderField(tmpl, fieldName(path), params)
}

func renderField(tmpl, field string, params map[string]any) string {
	return placeholder.ReplaceAllStringFunc(tmpl, func(m string) string {
		key := m[1 : len(m)-1]
		if key == "field" {
			return field
		}

		if v, ok := params[key]; ok {
			return fmt.Sprint(v)
		}

		return m
	})
}

// fieldName turns "author.first_name" into "first name"; list indexes keep
// the parent name ("tags.2" → "tags.2").
func fieldName(path string) string {
	if path == "" {
		return "value"
	}

	segs := strings.Split(path, ".")
	last := segs[len(segs)-1]

	if isIndex(last) && len(segs) > 1 {
		last = segs[len(segs)-2] + "." + last
	}

	return strings.ReplaceAll(last, "_", " ")
}

func isIndex(s string) bool {
	if s == "" {
		return false
	}

	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}

	return true
}

func joinPath(base, key string) string {
	if base == "" {
		return key
	}

	return base + "." + key
}

func cloneParams(p map[string]any) map[string]any {
	if p == nil {
		return nil
	}

	return maps.Clone(p)
}

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }
