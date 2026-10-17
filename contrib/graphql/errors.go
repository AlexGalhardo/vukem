package graphql

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/z"
)

// Error codes set in an error's extensions.code. They follow the names
// Apollo Server and most GraphQL clients already understand.
const (
	CodeParseFailed           = "GRAPHQL_PARSE_FAILED"
	CodeValidationFailed      = "GRAPHQL_VALIDATION_FAILED"
	CodeBadUserInput          = "BAD_USER_INPUT"
	CodeBadRequest            = "BAD_REQUEST"
	CodeUnauthenticated       = "UNAUTHENTICATED"
	CodeForbidden             = "FORBIDDEN"
	CodeNotFound              = "NOT_FOUND"
	CodeConflict              = "CONFLICT"
	CodeTooManyRequests       = "TOO_MANY_REQUESTS"
	CodeInternal              = "INTERNAL_SERVER_ERROR"
	CodeOperationNotFound     = "OPERATION_RESOLUTION_FAILURE"
	CodePersistedNotFound     = "PERSISTED_QUERY_NOT_FOUND"
	CodePersistedNotSupported = "PERSISTED_QUERY_NOT_SUPPORTED"
	CodePersistedRequired     = "PERSISTED_QUERY_REQUIRED"
	CodeLimitExceeded         = "QUERY_LIMIT_EXCEEDED"
)

// Location is a 1-based line and column in the request document.
type Location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// Error is a GraphQL error as it appears in a response's "errors" list
// (spec §7.1.2).
type Error struct {
	Message    string         `json:"message"`
	Locations  []Location     `json:"locations,omitempty"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`

	// cause is the resolver's original error, kept for logging only.
	cause error
}

// Error implements error.
func (e *Error) Error() string {
	if len(e.Locations) > 0 {
		return fmt.Sprintf("%s (%d:%d)", e.Message, e.Locations[0].Line, e.Locations[0].Column)
	}

	return e.Message
}

// Unwrap returns the resolver error the GraphQL error was built from.
func (e *Error) Unwrap() error { return e.cause }

// Code returns extensions.code, or "".
func (e *Error) Code() string {
	c, _ := e.Extensions["code"].(string)
	return c
}

func syntaxError(loc Location, msg string) *Error {
	return &Error{
		Message:    "Syntax Error: " + msg,
		Locations:  []Location{loc},
		Extensions: map[string]any{"code": CodeParseFailed},
	}
}

func validationError(msg string, locs ...Location) *Error {
	return &Error{Message: msg, Locations: locs, Extensions: map[string]any{"code": CodeValidationFailed}}
}

func inputError(msg string, locs ...Location) *Error {
	return &Error{Message: msg, Locations: locs, Extensions: map[string]any{"code": CodeBadUserInput}}
}

// Extender is implemented by resolver errors that add their own
// extensions (merged over the derived "code").
type Extender interface {
	error
	Extensions() map[string]any
}

// internalMessage replaces the text of unexpected errors (CWE-209).
const internalMessage = "Internal server error"

// fromResolverError converts an error returned by a resolver. Client-safe
// errors (errs.HTTPError below 500, validation errors, *Error) keep their
// message; anything else becomes "Internal server error" unless debug.
func fromResolverError(ctx context.Context, err error, debug bool) *Error {
	if ge, ok := errors.AsType[*Error](err); ok {
		c := *ge
		c.Extensions = maps.Clone(ge.Extensions)
		c.cause = err

		return &c
	}

	out := &Error{Extensions: map[string]any{}, cause: err}

	switch {
	case isIssues(err):
		is, _ := z.AsIssues(err)
		out.Message = "Invalid input"
		out.Extensions["code"] = CodeBadUserInput
		out.Extensions["fields"] = is.Fields()
	case isValidation(err):
		ve, _ := errors.AsType[*errs.ValidationError](err)
		out.Message = "Invalid input"
		out.Extensions["code"] = CodeBadUserInput
		out.Extensions["fields"] = ve.Fields
	default:
		status := errs.Status(err)
		out.Extensions["code"] = codeFor(status)

		if status >= http.StatusInternalServerError {
			out.Message = internalMessage
			if debug {
				out.Message = err.Error()
			}
		} else if he, ok := errors.AsType[*errs.HTTPError](err); ok {
			out.Message = he.LocalizedText(ctx)
		} else {
			out.Message = clientMessage(err, status, debug)
		}
	}

	if ex, ok := errors.AsType[Extender](err); ok {
		maps.Copy(out.Extensions, ex.Extensions())
	}

	return out
}

func isIssues(err error) bool {
	_, ok := z.AsIssues(err)
	return ok
}

func isValidation(err error) bool {
	_, ok := errors.AsType[*errs.ValidationError](err)
	return ok
}

// clientMessage is the text of a non-HTTPError 4xx error. A leaf
// StatusCoder is the app's own client error and keeps its text; one that
// wraps a cause (orm's unique-constraint conflict wraps the driver error)
// or any other error gets the status text only (CWE-209).
func clientMessage(err error, status int, debug bool) string {
	if debug {
		return err.Error()
	}

	if sc, ok := errors.AsType[errs.StatusCoder](err); ok && errors.Unwrap(sc) == nil {
		return sc.Error()
	}

	return http.StatusText(status)
}

func codeFor(status int) string {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return CodeBadUserInput
	case http.StatusUnauthorized:
		return CodeUnauthenticated
	case http.StatusForbidden:
		return CodeForbidden
	case http.StatusNotFound:
		return CodeNotFound
	case http.StatusConflict:
		return CodeConflict
	case http.StatusTooManyRequests:
		return CodeTooManyRequests
	}

	if status >= http.StatusInternalServerError {
		return CodeInternal
	}

	return strings.ToUpper(strings.ReplaceAll(http.StatusText(status), " ", "_"))
}

// NewError returns a client-visible error with extensions, for resolvers
// that want full control over the response.
func NewError(message string, extensions map[string]any) *Error {
	return &Error{Message: message, Extensions: extensions}
}
