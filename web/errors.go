package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"

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
}

// Problem writes an application/problem+json response.
func (c *Ctx) Problem(status int, title, detail string) error {
	return c.writeProblem(&Problem{Status: status, Title: title, Detail: detail})
}

func (c *Ctx) writeProblem(p *Problem) error {
	if p.Type == "" {
		p.Type = "about:blank"
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

	b, err := json.Marshal(p)
	if err != nil {
		return fmt.Errorf("web: encode problem: %w", err)
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
	status := errs.Status(err)
	he, _ := errors.AsType[*errs.HTTPError](err)
	verr, _ := errors.AsType[*errs.ValidationError](err)
	level := slog.LevelDebug

	if verr == nil {
		// z.FieldError or Issues returned by the handler: logged at Info, as
		// they may be a bug (internal data validated without errs.Wrap(500)).
		if verr = issuesValidation(c.Context(), c.Log(), err); verr != nil {
			level = slog.LevelInfo
		}
	}

	logError(c, status, err, level)

	if c.s.Debug && status >= 500 {
		c.writeDebugReport(status, err)
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
		detail = err.Error()
	}

	if detail == title {
		detail = ""
	}

	switch {
	case c.WantsJSON():
		p := &Problem{Status: status, Title: title, Detail: detail}
		if verr != nil {
			p.Errors = verr.Fields
			if c.s.ProblemBaseURL != "" {
				p.Type = c.s.ProblemBaseURL + "/validation"
			}
		}

		_ = c.writeProblem(p)
	// GET/HEAD never redirect back: the previous page may be this one.
	case verr != nil && !c.IsHTMX() && c.r.Method != http.MethodGet && c.r.Method != http.MethodHead &&
		c.validationHook(verr):
	case c.s.Debug && status >= 500 && !c.IsHTMX():
		c.renderDebugPage(status, err)
	case c.s.ErrorPage != nil && !c.IsHTMX() && c.s.ErrorPage(c, safeErrorPage(status, title, detail, verr)):
	default:
		c.renderErrorPage(status, title, detail, verr)
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
	}

	return e
}

func (c *Ctx) validationHook(verr *errs.ValidationError) bool {
	hook, ok := c.Context().Value(validationHookKey{}).(ValidationHook)
	return ok && hook(c, verr)
}

func logError(c *Ctx, status int, err error, level slog.Level) {
	attrs := []any{"status", status, "method", c.r.Method, "path", c.r.URL.Path, "err", err}
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
	Error  error
	// Nonce is the request's CSP nonce: the built-in page's <style> carries
	// it, so the page renders under the default nonce-only CSP.
	Nonce string
}

func (c *Ctx) renderErrorPage(status int, title, detail string, verr *errs.ValidationError) {
	data := errorData{Status: status, Title: title, Detail: detail, Nonce: CSPNonce(c.Context())}
	if verr != nil {
		data.Errors = verr.Fields
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
