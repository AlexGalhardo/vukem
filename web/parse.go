package web

import (
	"net/http"

	"github.com/AlexGalhardo/golang-framework/errs"
	"github.com/AlexGalhardo/golang-framework/z"
)

// Parse reads the request input (JSON, form or multipart body merged over
// the query string) and parses it with schema. Validation issues become an
// *errs.ValidationError, so returning the error from a handler yields a 422
// problem+json, an HTMX fragment or a redirect back with flashed errors.
//
//	in, err := web.Parse(c, requests.StorePostSchema)
//	if err != nil {
//		return err
//	}
func Parse[T any](c *Ctx, schema z.Schema[T]) (T, error) {
	input, err := c.Input()
	if err != nil {
		var zero T
		return zero, err
	}

	return parseWith(schema, input)
}

// Bind parses the request input into T using the schema derived from T's
// `validate` struct tags (z.For).
func Bind[T any](c *Ctx) (T, error) { return Parse(c, z.For[T]()) }

// ParseQuery parses only the query string.
func ParseQuery[T any](c *Ctx, schema z.Schema[T]) (T, error) {
	return parseWith(schema, c.QueryInput())
}

// ParseParams parses the named path parameters ({id}, {slug}...).
func ParseParams[T any](c *Ctx, schema z.Schema[T], names ...string) (T, error) {
	input := make(map[string]any, len(names))
	for _, n := range names {
		if v := c.Param(n); v != "" {
			input[n] = v
		}
	}

	v, err := parseWith(schema, input)

	// Malformed path parameters name no resource.
	if _, invalid := err.(*errs.ValidationError); invalid { //nolint:errorlint // returned unwrapped by parseWith
		return v, errs.ErrNotFound
	}

	return v, err
}

func parseWith[T any](schema z.Schema[T], input any) (T, error) {
	v, err := schema.Parse(input)
	if err == nil {
		return v, nil
	}

	if is, ok := z.AsIssues(err); ok {
		return v, &errs.ValidationError{Fields: is.Fields()}
	}

	return v, err
}

// JSONWith writes v as JSON after checking it against schema when
// Services.CheckResponses is enabled (development and test by default).
// A mismatch is a server bug: it is logged and answered with 500 instead
// of sending data that breaks the API contract.
func JSONWith[T any](c *Ctx, status int, schema z.Schema[T], v T) error {
	if c.s.CheckResponses {
		if err := schema.Check(v); err != nil {
			return errs.Wrap(http.StatusInternalServerError, err)
		}
	}

	return c.JSON(status, v)
}
