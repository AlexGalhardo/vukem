package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
)

// Problem is an RFC 9457 problem details document.
type Problem struct {
	Type      string              `json:"type"`
	Title     string              `json:"title"`
	Status    int                 `json:"status"`
	Detail    string              `json:"detail,omitempty"`
	Instance  string              `json:"instance,omitempty"`
	RequestID string              `json:"request_id,omitempty"`
	Errors    map[string][]string `json:"errors,omitempty"`
	// Issues lists each validation problem with its path, code, message key,
	// parameters and origin; errors carries the messages alone.
	Issues []errs.Issue `json:"issues,omitempty"`
	// Extensions are extra members (RFC 9457 section 3.2) that the error
	// handler writes next to the standard ones; a key naming a standard
	// member is ignored. Plain json.Marshal leaves them out.
	Extensions map[string]any `json:"-"`
}

// encodeProblem writes the standard members, then the extensions that do
// not collide with them. Not a MarshalJSON method: the OpenAPI generator
// documents Problem from its struct fields, which a Marshaler would hide.
func encodeProblem(p *Problem) ([]byte, error) {
	b, err := marshalJSON(p)
	if err != nil || len(p.Extensions) == 0 {
		return b, err
	}

	// RawMessage keeps the standard members byte-exact (no float64 trip).
	members := map[string]any{}

	var std map[string]json.RawMessage
	if err := unmarshalJSONBytes(b, &std); err != nil {
		return nil, err
	}

	for k, v := range std {
		members[k] = v
	}

	for k, v := range p.Extensions {
		switch k {
		// Reserved even when omitted from this response.
		case "type", "title", "status", "detail", "instance", "request_id", "errors", "issues":
		default:
			members[k] = v
		}
	}

	return marshalJSON(members)
}

// Problem writes an application/problem+json response.
func (c *Ctx) Problem(status int, title, detail string) error {
	return c.writeProblem(&Problem{Status: status, Title: title, Detail: detail})
}

// problemSlug is the last path segment of a default problem type: "Not Found"
// becomes "not-found" and "I'm a teapot" "im-a-teapot" (only a-z, 0-9 and
// dashes survive).
func problemSlug(status int) string {
	slug := strings.ReplaceAll(strings.ToLower(http.StatusText(status)), " ", "-")

	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' {
			return r
		}

		return -1
	}, slug)
}

// defaultProblemType is the "type" a problem of status gets when nothing
// chose one: ProblemBaseURL + "/" + the status slug when the app set a base
// URL (it asked for type URIs), "about:blank" otherwise.
func (c *Ctx) defaultProblemType(status int) string {
	if c.s.ProblemBaseURL != "" {
		if slug := problemSlug(status); slug != "" {
			return c.s.ProblemBaseURL + "/" + slug
		}
	}

	return "about:blank"
}

func (c *Ctx) writeProblem(p *Problem) error {
	if p.Type == "" {
		p.Type = c.defaultProblemType(p.Status)
	}

	if p.Title == "" {
		p.Title = http.StatusText(p.Status)
	}

	if p.Instance == "" {
		p.Instance = c.r.URL.Path
	}

	if p.RequestID == "" {
		p.RequestID = RequestID(c.Context())
	}

	if c.s.ProblemDecorator != nil {
		c.s.ProblemDecorator(c, p)
	}

	b, err := encodeProblem(p)
	if err != nil && len(p.Issues) > 0 {
		// A param an app put in a FieldErrorKey may not serialize. The
		// problem must still answer with its status, so retry without the
		// params, which are only the machine-readable side of the messages.
		p.Issues = slices.Clone(p.Issues)
		for i := range p.Issues {
			p.Issues[i].Params = nil
		}

		c.Log().WarnContext(c.Context(), "web: encode problem; retrying without issue params", "err", err)

		b, err = encodeProblem(p)
	}

	if err != nil {
		// Whatever cannot be encoded (an extension, what a decorator added),
		// the status must still be answered, never an empty 200.
		c.Log().WarnContext(c.Context(), "web: encode problem; answering the bare problem", "err", err)

		b, err = marshalJSON(map[string]any{"type": p.Type, "title": p.Title, "status": p.Status})
		if err != nil {
			return fmt.Errorf("web: encode problem: %w", err)
		}
	}

	return c.Blob(p.Status, "application/problem+json", b)
}

// ValidationHook lets session-aware code (flash + redirect back) handle
// validation errors for classic HTML form posts. It reports whether it
// wrote a response.
type ValidationHook func(c *Ctx, verr *errs.ValidationError) bool

type validationHookKey struct{}

// WithValidationHook installs hook for requests carrying ctx.
func WithValidationHook(ctx context.Context, hook ValidationHook) context.Context {
	return context.WithValue(ctx, validationHookKey{}, hook)
}

// ErrorMapper replaces an error before it is rendered. It reports false when
// the error is not its concern.
type ErrorMapper func(c *Ctx, err error) (error, bool)

// MapError registers how errors of type E are rendered, once, for the app: fn
// receives the first E in err's chain (errors.AsType, so a wrapped one is
// found) and returns the error to render in its place, usually an
// *errs.HTTPError. The returned error goes through DefaultErrorHandler like
// any other, so the negotiation (JSON, htmx, HTML, Inertia), the headers and
// the masking are the same:
//
//	web.MapError(s, func(c *web.Ctx, e *billing.InsufficientFunds) error {
//		return errs.New(http.StatusPaymentRequired, "Not enough balance.")
//	})
//
// The first registered mapper that matches wins; they do not chain. The
// original error is the one that is logged. A mapper cannot expose a server
// fault: an error it returns with a 5xx status is rendered like any other
// 5xx. A mapper that returns nil leaves the original error alone. Errors
// written with Fail from a middleware are mapped too. Register mappers while
// the app is built, before it serves requests: the list is read on every
// request and is not locked.
func MapError[E error](s *Services, fn func(c *Ctx, e E) error) {
	s.ErrorMappers = append(s.ErrorMappers, func(c *Ctx, err error) (error, bool) {
		e, ok := errors.AsType[E](err)
		if !ok {
			return nil, false
		}

		return fn(c, e), true
	})
}

// mapError returns the error the registered mappers render err as, or err
// itself when none matches.
func (c *Ctx) mapError(err error) error {
	for _, m := range c.s.ErrorMappers {
		if mapped, ok := m(c, err); ok && mapped != nil {
			return mapped
		}
	}

	return err
}

// DefaultErrorHandler renders err according to the client:
//   - JSON clients get RFC 9457 problem+json (422 includes field errors;
//     z.Issues and z.FieldError errors count as validation errors);
//   - classic form posts with a validation error go to the validation hook
//     (flash errors and redirect back) when one is installed;
//   - htmx requests get an HTML fragment with the status (htmx 4 swaps 4xx);
//   - Services.ErrorPage, when set, renders the page in its own format
//     (an Inertia page for Inertia requests);
//   - browsers get an HTML status page (views "errors/<status>" if present).
//
// 5xx details are never sent to clients unless Services.Debug is set
// (docs/security.md, A10).
func DefaultErrorHandler(c *Ctx, err error) {
	// What is rendered may be a mapped error; what is logged, and shown on
	// the debug page, is the one that happened.
	original := err
	err = c.mapError(err)

	status := errs.Status(err)
	he, _ := errors.AsType[*errs.HTTPError](err)
	verr, _ := errors.AsType[*errs.ValidationError](err)
	level := slog.LevelDebug

	if verr == nil {
		// z.FieldError or Issues returned by the handler: logged at Info, as
		// they may be a bug (internal data validated without errs.Wrap(500)).
		if verr = issuesValidation(c.Context(), c.Log(), err, ""); verr != nil {
			level = slog.LevelInfo
		}
	}

	// A 5xx that wraps field errors is a server fault: its fields are
	// internal detail (docs/security.md, A10).
	if status >= http.StatusInternalServerError {
		verr = nil
	}

	logError(c, status, original, level)

	if c.s.Debug && status >= 500 {
		c.writeDebugReport(status, original)
	}

	if c.w.Written() {
		return // headers already sent; nothing safe to do but log
	}

	if he != nil {
		for k, vs := range he.Headers {
			for _, v := range vs {
				c.w.Header().Add(k, v)
			}
		}
	}

	title := http.StatusText(status)
	detail := ""

	switch {
	case he != nil && status < 500:
		detail = he.LocalizedText(c.Context())
	case verr != nil:
		detail = fmt.Sprintf("%d field(s) are invalid.", len(verr.Fields))
		title = "Validation failed"
	case c.s.Debug:
		detail = original.Error()
	}

	if he != nil && status < 500 && he.Title != "" && verr == nil {
		title = he.Title
	}

	if detail == title {
		detail = ""
	}

	switch {
	case c.WantsJSON():
		p := &Problem{Status: status, Title: title, Detail: detail}
		if he != nil && status < 500 {
			// A clone: the decorator may edit it and he can be a shared sentinel.
			p.Extensions = maps.Clone(he.Extensions)
			p.Type = he.Type
		}

		if verr != nil {
			p.Errors = verr.Fields
			p.Issues = verr.List()
			if c.s.ProblemBaseURL != "" {
				p.Type = c.s.ProblemBaseURL + "/validation"
			}
		}

		_ = c.writeProblem(p)
	// GET/HEAD never redirect back: the previous page may be this one.
	case verr != nil && !c.IsHTMX() && c.r.Method != http.MethodGet && c.r.Method != http.MethodHead &&
		c.validationHook(verr):
	case c.s.Debug && status >= 500 && !c.IsHTMX():
		c.renderDebugPage(status, original)
	case c.s.ErrorPage != nil && !c.IsHTMX() && c.s.ErrorPage(c, safeErrorPage(status, title, detail, verr)):
	default:
		c.renderErrorPage(status, title, detail, verr, original)
	}
}

// safeErrorPage is what Services.ErrorPage receives: for 5xx only the status
// text, even when the error wraps field errors or carries a message.
func safeErrorPage(status int, title, detail string, verr *errs.ValidationError) ErrorPage {
	if status >= http.StatusInternalServerError {
		return ErrorPage{Status: status, Title: http.StatusText(status)}
	}

	e := ErrorPage{Status: status, Title: title, Detail: detail}
	if verr != nil {
		e.Errors = verr.Fields
		e.Issues = verr.List()
	}

	return e
}

func (c *Ctx) validationHook(verr *errs.ValidationError) bool {
	hook, ok := c.Context().Value(validationHookKey{}).(ValidationHook)
	return ok && hook(c, verr)
}

func logError(c *Ctx, status int, err error, level slog.Level) {
	attrs := []any{"status", status, "method", c.r.Method, "path", LogPath(c.r), "err", err}
	if id := RequestID(c.Context()); id != "" {
		attrs = append(attrs, "request_id", id)
	}

	if status >= 500 {
		level = slog.LevelError
	}

	c.Log().Log(c.Context(), level, "request error", attrs...)
}

var errorPage = template.Must(template.New("error").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>{{.Status}} {{.Title}}</title>
<meta name="viewport" content="width=device-width, initial-scale=1">
<style{{with .Nonce}} nonce="{{.}}"{{end}}>body{font-family:system-ui,sans-serif;max-width:40rem;margin:4rem auto;padding:0 1rem}</style></head>
<body>
<h1>{{.Status}} {{.Title}}</h1>{{with .Detail}}<p>{{.}}</p>{{end}}
{{with .Errors}}<ul>{{range $field, $msgs := .}}{{range $msgs}}<li><strong>{{$field}}</strong>: {{.}}</li>{{end}}{{end}}</ul>{{end}}
</body></html>
`))

var errorFragment = template.Must(template.New("fragment").Parse(
	`<div class="error" role="alert" data-status="{{.Status}}"><strong>{{.Title}}</strong>` +
		`{{with .Detail}} {{.}}{{end}}{{with .Errors}}<ul>{{range $f, $msgs := .}}{{range $msgs}}` +
		`<li data-field="{{$f}}">{{.}}</li>{{end}}{{end}}</ul>{{end}}</div>`))

type errorData struct {
	Status int
	Title  string
	Detail string
	Errors map[string][]string
	// Issues lists the same problems with code, key and params, for a view
	// that renders them its own way.
	Issues []errs.Issue
	// Error is the handler's error, set only when Services.Debug is on:
	// it may hold SQL, paths or secrets (CWE-209).
	Error error
	// Nonce is the request's CSP nonce: the built-in page's <style> carries
	// it, so the page renders under the default nonce-only CSP.
	Nonce string
}

func (c *Ctx) renderErrorPage(status int, title, detail string, verr *errs.ValidationError, err error) {
	data := errorData{Status: status, Title: title, Detail: detail, Nonce: CSPNonce(c.Context())}
	if verr != nil {
		data.Errors = verr.Fields
		data.Issues = verr.List()
	}

	if c.s.Debug {
		data.Error = err
	}

	c.w.Header().Set("Content-Type", "text/html; charset=utf-8")
	c.w.Header().Set("Cache-Control", "no-store")

	if c.IsHTMX() {
		c.w.WriteHeader(status)
		_ = errorFragment.Execute(c.w, data)

		return
	}

	if v := c.s.Views; v != nil {
		name := fmt.Sprintf("errors/%d", status)
		if v.Has(name) {
			c.w.WriteHeader(status)

			if err := v.Render(c.Context(), c.w, name, data); err != nil {
				c.Log().ErrorContext(c.Context(), "render error page", "err", err)
			}

			return
		}
	}

	c.w.WriteHeader(status)
	_ = errorPage.Execute(c.w, data)
}

type requestIDKey struct{}

// WithRequestID stores the request id in ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request id stored in ctx, or "".
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

func errNotFound() error { return errs.ErrNotFound }
