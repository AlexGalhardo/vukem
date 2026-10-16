package graphql

import "fmt"

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

// NewError returns a client-visible error with extensions, for resolvers
// that want full control over the response.
func NewError(message string, extensions map[string]any) *Error {
	return &Error{Message: message, Extensions: extensions}
}
