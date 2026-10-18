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
//
// Parse returns every problem at once as [Issues] (see [AsIssues]), which
// web.Parse turns into a 422 response. Builders cover strings, numbers,
// booleans, times, enums, files, slices, maps and [Struct] (or [FromTags]
// from `validate` tags); [JSONSchema] exports a schema for the OpenAPI
// document.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/validation.md.
package z

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
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
	// "too_small.string", "string.email"...), or the key given to
	// FieldErrorKey, or "invalid" for a Refine or Custom error that is not a
	// FieldError; empty for other custom messages (Message overrides,
	// FieldError). Localize uses it.
	Key string `json:"-"`
	// Params holds the values used by the message, e.g. {"min": 3}.
	Params map[string]any `json:"params,omitempty"`
	// Err is the error a Refine callback or Custom parser returned when it
	// was not a FieldError: its text may be internal (a database error), so
	// Message is the generic "invalid" message and Err is for server logs
	// only. Return FieldError or FieldErrorKey for a client-facing message.
	Err error `json:"-"`
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

// HTTPStatus reports 422, so errs.Status and the web error handler treat
// Issues returned from a handler as a validation error.
func (is Issues) HTTPStatus() int { return http.StatusUnprocessableEntity }

// AsIssues extracts Issues from err. An error from [FieldError] or
// [FieldErrorKey] yields its single issue.
func AsIssues(err error) (Issues, bool) {
	if is, ok := errors.AsType[Issues](err); ok {
		return is, true
	}

	if _, ok := errors.AsType[*fieldError](err); ok {
		var c pctx
		c.addErr("", err)

		return c.issues, true
	}

	return nil, false
}

// FieldError returns an error for Refine callbacks that points at a field
// path relative to the refined value. Its message is never translated; use
// [FieldErrorKey] for a translatable one.
func FieldError(path, message string) error {
	return &fieldError{path: path, message: message}
}

// FieldErrorKey returns a translatable error for Refine callbacks (and
// [Custom] parsers). The issue points at path relative to the refined value
// ("" for the value itself), gets Code [CodeCustom], Key key and Params
// params, and its Message is fallback with {field} and params filled in.
// [Issues.Localize] rebuilds the message from the template for key, so with
// package i18n a key "confirmed" is looked up as "validation.confirmed".
func FieldErrorKey(path, key, fallback string, params map[string]any) error {
	return &fieldError{path: path, message: fallback, key: key, params: cloneParams(params)}
}

type fieldError struct {
	path    string
	message string
	key     string
	params  map[string]any
}

func (e *fieldError) Error() string { return e.path + ": " + e.message }

// HTTPStatus reports 422: a FieldError returned from a handler (not only
// from Refine) is a validation error.
func (*fieldError) HTTPStatus() int { return http.StatusUnprocessableEntity }

// keyInvalid is the message key of Refine and Custom errors that are not a
// FieldError.
const keyInvalid = "invalid"

var defaultMessages = map[string]string{
	keyInvalid:           "The {field} field is invalid.",
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
	"too_big.file":       "The {field} field must not be greater than {max_kb} kilobytes.",
	"file.type":          "The {field} field must be a file of type: {types}.",
	"file.extension":     "The {field} field must have one of these extensions: {extensions}.",
}

// singularMessages replace a default message whose count parameter is 1
// ("at least 1 character", not "1 characters"). The issue key stays the
// same, so translation catalogs keep a single entry per rule.
var singularMessages = map[string]struct{ param, message string }{
	"too_small.string": {"min", "The {field} field must be at least {min} character."},
	"too_big.string":   {"max", "The {field} field must not be greater than {max} character."},
	"exact.string":     {"len", "The {field} field must be {len} character."},
	"too_small.array":  {"min", "The {field} field must have at least {min} item."},
	"too_big.array":    {"max", "The {field} field must not have more than {max} item."},
	"exact.array":      {"len", "The {field} field must have {len} item."},
}

// defaultMessage returns the built-in template for key, in the singular
// when its count parameter is 1.
func defaultMessage(key string, params map[string]any) string {
	if one, ok := singularMessages[key]; ok && fmt.Sprint(params[one.param]) == "1" {
		return one.message
	}

	return defaultMessages[key]
}

// Localize returns a copy of is with each built-in message rebuilt from the
// template lookup returns for the issue's Key, then its Code (for example a
// catalog's "validation.too_small.string" and "validation.too_small").
// {field} becomes lookup("attributes.<name>") when present, where name is
// the last path segment ("first_name"), or the humanized name otherwise.
// Issues without a template, and custom messages without a key, are kept
// unchanged.
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
		// Keyed custom issues (FieldErrorKey) fall back to their own
		// message, not to a generic "custom" template.
		if !ok && iss.Code != iss.Key && iss.Code != CodeCustom {
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
