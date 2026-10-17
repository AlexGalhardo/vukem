package web

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/z"
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

	return parseWith(c.Context(), c.Log(), schema, input)
}

// Bind parses the request input into T using the schema derived from T's
// `validate` struct tags (z.For).
func Bind[T any](c *Ctx) (T, error) { return Parse(c, z.For[T]()) }

// ParseQuery parses only the query string.
func ParseQuery[T any](c *Ctx, schema z.Schema[T]) (T, error) {
	return parseWith(c.Context(), c.Log(), schema, c.QueryInput())
}

// ParseParams parses the named path parameters ({id}, {slug}...).
func ParseParams[T any](c *Ctx, schema z.Schema[T], names ...string) (T, error) {
	input := make(map[string]any, len(names))
	for _, n := range names {
		if v := c.Param(n); v != "" {
			input[n] = v
		}
	}

	v, err := parseWith(c.Context(), c.Log(), schema, input)

	// Malformed path parameters name no resource.
	if _, invalid := err.(*errs.ValidationError); invalid { //nolint:errorlint // returned unwrapped by parseWith
		return v, errs.ErrNotFound
	}

	return v, err
}

// ParseInput validates an already assembled input map (validation errors
// become a 422 ValidationError). Messages stay in English: it has no
// request context to localize them (see WithIssueLocalizer).
func ParseInput[T any](schema z.Schema[T], input map[string]any) (T, error) {
	return parseWith(context.Background(), nil, schema, input)
}

func parseWith[T any](ctx context.Context, log *slog.Logger, schema z.Schema[T], input any) (T, error) {
	v, err := schema.Parse(input)
	if err == nil {
		return v, nil
	}

	if verr := issuesValidation(ctx, log, err); verr != nil {
		return v, verr
	}

	return v, err
}

// issuesValidation turns z Issues (or a z.FieldError) in err into a
// localized *errs.ValidationError, or returns nil. Errors wrapped with
// another status (errs.Wrap(500, issues) for internal data) stay as they
// are. The raw errors of plain Refine/Custom failures go to log (nil: not
// logged), never to the client.
func issuesValidation(ctx context.Context, log *slog.Logger, err error) *errs.ValidationError {
	if errs.Status(err) != http.StatusUnprocessableEntity {
		return nil
	}

	is, ok := z.AsIssues(err)
	if !ok {
		return nil
	}

	if log != nil {
		for _, i := range is {
			if i.Err != nil {
				log.WarnContext(ctx, "validation callback error hidden from the client", "path", i.Path, "err", i.Err)
			}
		}
	}

	if localize := IssueLocalizer(ctx); localize != nil {
		is = localize(is)
	}

	return &errs.ValidationError{Fields: is.Fields()}
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
