// Package errs defines the error values handlers return. A central handler
// (see package web) turns them into HTML pages, redirects with flashed
// errors, HTMX fragments or RFC 9457 problem details.
//
// [HTTPError] pairs a status with a client-safe message and an internal
// cause that is logged, never shown; sentinels such as [ErrNotFound] and
// [ErrForbidden] cover the common cases. [ValidationError] carries field
// messages (422), and [Status] maps any error to its HTTP status.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/http.md.
package errs

import (
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
)

// HTTPError is an error with an HTTP status and a client-safe message.
type HTTPError struct {
	// Status is the HTTP status code.
	Status int
	// Message is safe to show to clients. Empty means the status text.
	Message string
	// Err is the internal cause; it is logged, never shown to clients.
	Err error
	// Headers are added to the response (e.g. Allow, Retry-After).
	Headers http.Header
	// Key is the i18n message key Message translates (see NewKey); empty
	// means Message is shown as is.
	Key string
	// Params fill the {name} placeholders of the translated Key.
	Params map[string]any

	stack []uintptr
}

// New returns an *HTTPError with status and a client-safe message.
func New(status int, message string) *HTTPError {
	return &HTTPError{Status: status, Message: message, stack: callers(1)}
}

// Wrap returns an *HTTPError with status whose internal cause is err.
func Wrap(status int, err error) *HTTPError {
	return &HTTPError{Status: status, Err: err, stack: callers(1)}
}

// Error implements error.
func (e *HTTPError) Error() string {
	msg := e.Text()
	if e.Err != nil {
		return fmt.Sprintf("%d %s: %v", e.Status, msg, e.Err)
	}

	return fmt.Sprintf("%d %s", e.Status, msg)
}

// Text returns Message or, when empty, the standard status text.
func (e *HTTPError) Text() string {
	if e.Message != "" {
		return e.Message
	}

	return http.StatusText(e.Status)
}

// Unwrap returns the internal cause.
func (e *HTTPError) Unwrap() error { return e.Err }

// Is matches another *HTTPError with the same status, so sentinel errors
// work with errors.Is even when wrapped or re-created with a cause.
func (e *HTTPError) Is(target error) bool {
	t, ok := target.(*HTTPError)
	return ok && t.Status == e.Status && (t.Message == "" || t.Message == e.Message)
}

// WithHeader returns a copy of e with an extra response header.
func (e *HTTPError) WithHeader(key, value string) *HTTPError {
	c := *e
	c.Headers = e.Headers.Clone()

	if c.Headers == nil {
		c.Headers = http.Header{}
	}

	c.Headers.Add(key, value)

	return &c
}

// Sentinel errors for common statuses. Compare with errors.Is.
var (
	ErrBadRequest           = &HTTPError{Status: http.StatusBadRequest}
	ErrUnauthorized         = &HTTPError{Status: http.StatusUnauthorized}
	ErrForbidden            = &HTTPError{Status: http.StatusForbidden}
	ErrNotFound             = &HTTPError{Status: http.StatusNotFound}
	ErrMethodNotAllowed     = &HTTPError{Status: http.StatusMethodNotAllowed}
	ErrConflict             = &HTTPError{Status: http.StatusConflict}
	ErrPreconditionFailed   = &HTTPError{Status: http.StatusPreconditionFailed}
	ErrRequestTooLarge      = &HTTPError{Status: http.StatusRequestEntityTooLarge}
	ErrUnsupportedMediaType = &HTTPError{Status: http.StatusUnsupportedMediaType}
	ErrUnprocessable        = &HTTPError{Status: http.StatusUnprocessableEntity}
	ErrTooManyRequests      = &HTTPError{Status: http.StatusTooManyRequests}
	ErrInternal             = &HTTPError{Status: http.StatusInternalServerError}
)

// ValidationError reports invalid input, keyed by field path.
type ValidationError struct {
	Fields map[string][]string
}

// NewValidation returns an empty ValidationError.
func NewValidation() *ValidationError {
	return &ValidationError{Fields: map[string][]string{}}
}

// Add appends a message for field and returns e for chaining:
//
//	return errs.NewValidation().Add("email", "taken").Add("name", "required")
//
// Because the result is an error type, errcheck flags a bare v.Add(...)
// statement; use [ValidationError.Append] there.
func (e *ValidationError) Add(field, message string) *ValidationError {
	e.Append(field, message)
	return e
}

// Append appends a message for field. It is Add without a result, for
// collecting messages in statements (loops, conditionals) without an
// unchecked-error lint warning.
func (e *ValidationError) Append(field, message string) {
	if e.Fields == nil {
		e.Fields = map[string][]string{}
	}

	e.Fields[field] = append(e.Fields[field], message)
}

// Empty reports whether no field has errors.
func (e *ValidationError) Empty() bool { return len(e.Fields) == 0 }

// First returns the first message for field, or "".
func (e *ValidationError) First(field string) string {
	if msgs := e.Fields[field]; len(msgs) > 0 {
		return msgs[0]
	}

	return ""
}

// Error implements error.
func (e *ValidationError) Error() string {
	keys := slices.Sorted(maps.Keys(e.Fields))

	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+": "+strings.Join(e.Fields[k], "; "))
	}

	return "validation failed: " + strings.Join(parts, ", ")
}

// Status returns the HTTP status for err: the status of an *HTTPError, 422
// for a *ValidationError, the status of a StatusCoder, and 500 otherwise.
func Status(err error) int {
	if he, ok := errors.AsType[*HTTPError](err); ok {
		return he.Status
	}

	if _, ok := errors.AsType[*ValidationError](err); ok {
		return http.StatusUnprocessableEntity
	}

	if sc, ok := errors.AsType[StatusCoder](err); ok {
		return sc.HTTPStatus()
	}

	return http.StatusInternalServerError
}

// StatusCoder is implemented by errors that carry their own HTTP status,
// such as errors from packages that should not import errs.
type StatusCoder interface {
	error
	HTTPStatus() int
}
