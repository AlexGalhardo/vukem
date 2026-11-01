package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/z"
)

type fakeViews map[string]string

func (f fakeViews) Has(name string) bool { _, ok := f[name]; return ok }

func (f fakeViews) Render(_ context.Context, w io.Writer, name string, data any) error {
	_, err := fmt.Fprintf(w, f[name], data.(errorData).Status)
	return err
}

func (f fakeViews) RenderBlock(_ context.Context, w io.Writer, name, block string, _ any) error {
	_, err := fmt.Fprintf(w, "%s#%s", name, block)
	return err
}

func runError(t *testing.T, err error, setup func(r *http.Request), s *Services) (*httptest.ResponseRecorder, string) {
	t.Helper()

	var logs bytes.Buffer
	if s == nil {
		s = &Services{}
	}
	s.Log = slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))

	r := httptest.NewRequestWithContext(WithRequestID(t.Context(), "req-1"), http.MethodPost, "/posts", http.NoBody)
	if setup != nil {
		setup(r)
	}

	rec := httptest.NewRecorder()
	NewCtx(rec, r, s).Serve(func(*Ctx) error { return err })

	return rec, logs.String()
}

func TestErrorHandlerProblemJSON(t *testing.T) {
	t.Parallel()

	verr := errs.NewValidation().Add("title", "required")
	jsonReq := func(r *http.Request) { r.Header.Set("Accept", "application/json") }

	tests := []struct {
		name       string
		err        error
		s          *Services
		wantStatus int
		wantTitle  string
		wantDetail string
		wantType   string
		wantErrors bool
	}{
		{name: "not found", err: errs.ErrNotFound, wantStatus: 404, wantTitle: "Not Found"},
		{
			name:       "custom message",
			err:        errs.New(409, "Email already taken."),
			wantStatus: 409,
			wantTitle:  "Conflict",
			wantDetail: "Email already taken.",
		},
		{
			name:       "validation",
			err:        verr,
			wantStatus: 422,
			wantTitle:  "Validation failed",
			wantDetail: "1 field(s) are invalid.",
			wantErrors: true,
			wantType:   "about:blank",
		},
		{
			name:       "validation typed",
			err:        verr,
			s:          &Services{ProblemBaseURL: "https://x.dev/problems"},
			wantStatus: 422,
			wantErrors: true,
			wantType:   "https://x.dev/problems/validation",
			wantTitle:  "Validation failed",
			wantDetail: "1 field(s) are invalid.",
		},
		{
			name:       "internal hides details",
			err:        errors.New("sql: password=hunter2"),
			wantStatus: 500,
			wantTitle:  "Internal Server Error",
		},
		{
			name:       "internal debug",
			err:        errors.New("boom"),
			s:          &Services{Debug: true},
			wantStatus: 500,
			wantTitle:  "Internal Server Error",
			wantDetail: "boom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec, _ := runError(t, tt.err, jsonReq, tt.s)
			if rec.Code != tt.wantStatus || rec.Header().Get("Content-Type") != "application/problem+json" {
				t.Fatalf("status=%d ct=%q", rec.Code, rec.Header().Get("Content-Type"))
			}

			var p Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}
			if p.Title != tt.wantTitle || p.Detail != tt.wantDetail || p.Status != tt.wantStatus ||
				p.Instance != "/posts" ||
				p.RequestID != "req-1" {
				t.Fatalf("problem = %+v", p)
			}
			if tt.wantType != "" && p.Type != tt.wantType {
				t.Fatalf("type = %q", p.Type)
			}
			if tt.wantErrors != (p.Errors != nil) {
				t.Fatalf("errors = %v", p.Errors)
			}
		})
	}
}

func TestSecurity_A10_InternalErrorsNotLeaked(t *testing.T) {
	t.Parallel()

	secret := errors.New("pq: password authentication failed for user admin")

	for _, setup := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("Accept", "application/json") },
		func(r *http.Request) { r.Header.Set("Accept", "text/html") },
		func(r *http.Request) { r.Header.Set("HX-Request", "true") },
	} {
		rec, logs := runError(t, secret, setup, nil)
		if rec.Code != 500 || strings.Contains(rec.Body.String(), "password") {
			t.Fatalf("response leaked internals: %d %q", rec.Code, rec.Body.String())
		}
		if !strings.Contains(logs, "password authentication failed") || !strings.Contains(logs, "level=ERROR") {
			t.Fatalf("error not logged server-side: %q", logs)
		}
	}
}

func TestErrorHandlerHTML(t *testing.T) {
	t.Parallel()

	rec, _ := runError(t, errs.ErrForbidden.WithHeader("X-Reason", "policy"), nil, nil)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "<h1>403 Forbidden</h1>") ||
		rec.Header().Get("X-Reason") != "policy" {
		t.Fatalf("HTML page = %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("error page cacheable")
	}

	views := fakeViews{"errors/404": "custom %d"}
	rec, _ = runError(t, errs.ErrNotFound, nil, &Services{Views: views})
	if rec.Body.String() != "custom 404" {
		t.Fatalf("custom view = %q", rec.Body.String())
	}

	verr := errs.NewValidation().Add("title", "<script>")
	rec, _ = runError(t, verr, nil, nil)
	if rec.Code != 422 || strings.Contains(rec.Body.String(), "<script>") ||
		!strings.Contains(rec.Body.String(), "&lt;script&gt;") {
		t.Fatalf("validation page not escaped: %q", rec.Body.String())
	}
}

func TestErrorHandlerHTMXFragment(t *testing.T) {
	t.Parallel()

	verr := errs.NewValidation().Add("title", "required")
	rec, _ := runError(t, verr, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, nil)

	if rec.Code != 422 || !strings.HasPrefix(rec.Body.String(), `<div class="error"`) ||
		!strings.Contains(rec.Body.String(), `data-field="title"`) {
		t.Fatalf("htmx fragment = %d %q", rec.Code, rec.Body.String())
	}
}

func TestErrorHandlerValidationHook(t *testing.T) {
	t.Parallel()

	called := false
	hook := ValidationHook(func(c *Ctx, v *errs.ValidationError) bool {
		called = v.First("title") == "required"
		_ = c.Redirect("/form")
		return true
	})

	rec, _ := runError(t, errs.NewValidation().Add("title", "required"), func(r *http.Request) {
		*r = *r.WithContext(WithValidationHook(r.Context(), hook))
	}, nil)

	if !called || rec.Code != 303 || rec.Header().Get("Location") != "/form" {
		t.Fatalf("hook not used: called=%v %d", called, rec.Code)
	}
}

func TestErrorHandlerAfterWrite(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	s := &Services{Log: slog.New(slog.NewTextHandler(&logs, nil))}
	rec := httptest.NewRecorder()
	c := NewCtx(rec, httptest.NewRequestWithContext(context.Background(), "GET", "/", http.NoBody), s)

	c.Serve(func(c *Ctx) error {
		_ = c.String(200, "partial")
		return errors.New("late failure")
	})

	if rec.Code != 200 || rec.Body.String() != "partial" || !strings.Contains(logs.String(), "late failure") {
		t.Fatalf("late error handling = %d %q %q", rec.Code, rec.Body.String(), logs.String())
	}
}

func TestCustomErrorHandler(t *testing.T) {
	t.Parallel()

	var got error
	s := &Services{Errors: func(_ *Ctx, err error) { got = err }}
	NewCtx(httptest.NewRecorder(), httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody), s).
		Serve(func(*Ctx) error { return errs.ErrConflict })

	if !errors.Is(got, errs.ErrConflict) {
		t.Fatalf("custom handler got %v", got)
	}
}

// Keyed HTTP errors are shown in the request locale (errs.WithTranslator,
// installed by i18n.Middleware); the English message is the fallback.
func TestErrorHandlerTranslatesKeyedErrors(t *testing.T) {
	t.Parallel()

	keyed := errs.NewKey(http.StatusForbidden, "auth.unverified", "Your email address is not verified.", nil)
	pt := func(key string, _ map[string]any) (string, bool) {
		return "Seu endereço de e-mail não foi verificado.", key == "auth.unverified"
	}

	tests := []struct {
		name       string
		translator errs.Translator
		want       string
	}{
		{"translated", pt, "Seu endereço de e-mail não foi verificado."},
		{"no translator", nil, "Your email address is not verified."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec, _ := runError(t, keyed, func(r *http.Request) {
				r.Header.Set("Accept", "application/json")
				if tt.translator != nil {
					*r = *r.WithContext(errs.WithTranslator(r.Context(), tt.translator))
				}
			}, nil)

			var p Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}

			if p.Detail != tt.want {
				t.Errorf("detail = %q, want %q", p.Detail, tt.want)
			}
		})
	}
}

func TestErrorHandlerZValidationErrors(t *testing.T) {
	t.Parallel()

	_, issuesErr := z.Struct[struct {
		Title string `json:"title"`
	}](z.Shape{"title": z.String().Min(3)}).Parse(map[string]any{"title": "ab"})

	jsonReq := func(r *http.Request) { r.Header.Set("Accept", "application/json") }
	localized := func(r *http.Request) {
		jsonReq(r)
		*r = *r.WithContext(WithIssueLocalizer(r.Context(), func(is z.Issues) z.Issues {
			for i := range is {
				if is[i].Key == "taken" {
					is[i].Message = "já usado"
				}
			}

			return is
		}))
	}

	tests := []struct {
		name  string
		err   error
		setup func(r *http.Request)
		want  map[string][]string
	}{
		{
			"field error", z.FieldError("email", "Already taken."), jsonReq,
			map[string][]string{"email": {"Already taken."}},
		},
		{
			"wrapped field error", fmt.Errorf("checkout: %w", z.FieldError("items.0.qty", "Out of stock.")), jsonReq,
			map[string][]string{"items.0.qty": {"Out of stock."}},
		},
		{
			"keyed field error is localized",
			z.FieldErrorKey("email", "taken", "The {field} is taken.", nil),
			localized,
			map[string][]string{"email": {"já usado"}},
		},
		{
			"issues from Parse", issuesErr, jsonReq,
			map[string][]string{"title": {"The title field must be at least 3 characters."}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec, _ := runError(t, tt.err, tt.setup, nil)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %q)", rec.Code, rec.Body.String())
			}

			var p Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tt.want, p.Errors); diff != "" {
				t.Errorf("problem errors (-want +got):\n%s", diff)
			}
		})
	}

	t.Run("html page", func(t *testing.T) {
		t.Parallel()

		rec, _ := runError(t, z.FieldError("email", "Already taken."), nil, nil)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Already taken.") {
			t.Fatalf("HTML page = %d %q", rec.Code, rec.Body.String())
		}
	})

	t.Run("validation hook", func(t *testing.T) {
		t.Parallel()

		var got *errs.ValidationError

		hook := ValidationHook(func(c *Ctx, v *errs.ValidationError) bool {
			got = v
			_ = c.Redirect("/form")

			return true
		})

		rec, _ := runError(t, z.FieldError("email", "Already taken."), func(r *http.Request) {
			*r = *r.WithContext(WithValidationHook(r.Context(), hook))
		}, nil)

		if rec.Code != http.StatusSeeOther || got == nil || got.First("email") != "Already taken." {
			t.Fatalf("hook = %v, status %d; want the field error and a redirect", got, rec.Code)
		}
	})
}

func TestSecurity_A10_ZIssuesFailClosed(t *testing.T) {
	t.Parallel()

	secret := errors.New("db: connection refused to 10.0.0.5")
	schema := z.Struct[struct {
		Name string `json:"name"`
	}](z.Shape{"name": z.String().Refine(func(string) error { return secret })})
	_, issues := schema.Parse(map[string]any{"name": "x"})

	jsonReq := func(r *http.Request) { r.Header.Set("Accept", "application/json") }

	t.Run("internal data wrapped as 500", func(t *testing.T) {
		t.Parallel()

		rec, _ := runError(t, errs.Wrap(http.StatusInternalServerError, issues), jsonReq, nil)

		var p Problem
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}

		if rec.Code != http.StatusInternalServerError || p.Errors != nil {
			t.Errorf("wrapped issues = %d errors %v, want 500 without field errors", rec.Code, p.Errors)
		}
	})

	t.Run("returned issues", func(t *testing.T) {
		t.Parallel()

		rec, logs := runError(t, issues, jsonReq, nil)
		if rec.Code != http.StatusUnprocessableEntity || strings.Contains(rec.Body.String(), "10.0.0.5") ||
			!strings.Contains(rec.Body.String(), "The name field is invalid.") {
			t.Errorf("response = %d %q, want 422 with the generic message", rec.Code, rec.Body.String())
		}

		if !strings.Contains(logs, "level=INFO") || !strings.Contains(logs, "10.0.0.5") {
			t.Errorf("logs = %q, want an INFO line with the raw refine error", logs)
		}
	})

	t.Run("parse", func(t *testing.T) {
		t.Parallel()

		var logs bytes.Buffer

		s := &Services{Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug}))}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/?name=x", http.NoBody)
		r.Header.Set("Accept", "application/json")
		rec := httptest.NewRecorder()

		NewCtx(rec, r, s).Serve(func(c *Ctx) error {
			_, err := ParseQuery(c, schema)
			return err
		})

		if rec.Code != http.StatusUnprocessableEntity || strings.Contains(rec.Body.String(), "10.0.0.5") {
			t.Errorf("response = %d %q, want 422 without the raw error", rec.Code, rec.Body.String())
		}

		if !strings.Contains(logs.String(), "10.0.0.5") {
			t.Errorf("logs = %q, want the raw refine error", logs.String())
		}
	})
}

func TestErrorHandlerValidationHookSkipsGET(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodHead} {
		called := false
		hook := ValidationHook(func(c *Ctx, _ *errs.ValidationError) bool {
			called = true
			_ = c.Redirect("/form")

			return true
		})

		rec, _ := runError(t, errs.NewValidation().Add("q", "too short"), func(r *http.Request) {
			r.Method = method
			*r = *r.WithContext(WithValidationHook(r.Context(), hook))
		}, nil)

		if called || rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: hook called = %t, status %d; want no redirect and 422", method, called, rec.Code)
		}
	}
}

func TestErrorHandlerErrorPage(t *testing.T) {
	t.Parallel()

	secret := errors.New(`pq: password authentication failed for user "app"`)
	verr := errs.NewValidation().Add("title", "required")
	get := func(r *http.Request) { r.Method = http.MethodGet }

	tests := []struct {
		name  string
		err   error
		setup func(r *http.Request)
		debug bool
		want  []ErrorPage
	}{
		{"4xx message", errs.New(404, "Record not found"), nil, false, []ErrorPage{
			{Status: 404, Title: "Not Found", Detail: "Record not found"},
		}},
		{"status text", errs.ErrForbidden, nil, false, []ErrorPage{{Status: 403, Title: "Forbidden"}}},
		{"5xx hides the cause", secret, nil, false, []ErrorPage{{Status: 500, Title: "Internal Server Error"}}},
		{"5xx hides its message", errs.New(503, "db "+secret.Error()), nil, false, []ErrorPage{
			{Status: 503, Title: "Service Unavailable"},
		}},
		{
			"4xx hides a wrapped cause",
			errs.Wrap(404, secret),
			nil,
			false,
			[]ErrorPage{{Status: 404, Title: "Not Found"}},
		},
		{"field errors on a GET", verr, get, false, []ErrorPage{{
			Status: 422, Title: "Validation failed", Detail: "1 field(s) are invalid.",
			Errors: map[string][]string{"title": {"required"}},
		}}},
		{"5xx wrapping field errors hides them", errs.Wrap(500, verr), nil, false, []ErrorPage{
			{Status: 500, Title: "Internal Server Error"},
		}},
		{"JSON clients get problem+json", errs.ErrNotFound, func(r *http.Request) {
			r.Header.Set("Accept", "application/json")
		}, false, nil},
		{
			"htmx gets the fragment",
			errs.ErrNotFound,
			func(r *http.Request) { r.Header.Set("HX-Request", "true") },
			false,
			nil,
		},
		{"debug 5xx shows the report", secret, nil, true, nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var calls []ErrorPage

			s := &Services{Debug: tt.debug, DebugOut: io.Discard, ErrorPage: func(c *Ctx, e ErrorPage) bool {
				calls = append(calls, e)
				return c.String(e.Status, "custom") == nil
			}}

			rec, _ := runError(t, tt.err, tt.setup, s)

			if diff := cmp.Diff(tt.want, calls); diff != "" {
				t.Errorf("ErrorPage calls (-want +got):\n%s", diff)
			}

			if got := rec.Body.String() == "custom"; got != (tt.want != nil) {
				t.Errorf("body = %q, want the hook's page: %v", rec.Body, tt.want != nil)
			}
		})
	}

	// A hook that writes nothing falls back to the HTML page.
	s := &Services{ErrorPage: func(*Ctx, ErrorPage) bool { return false }}
	if rec, _ := runError(t, errs.ErrNotFound, nil, s); !strings.Contains(rec.Body.String(), "<h1>404 Not Found</h1>") {
		t.Errorf("fallback body = %q, want the HTML page", rec.Body)
	}
}

// captureViews records the data an error view receives.
type captureViews struct{ got *errorData }

func (v captureViews) Has(string) bool { return true }

func (v captureViews) Render(_ context.Context, _ io.Writer, _ string, data any) error {
	d := data.(errorData)
	*v.got = d
	return nil
}

func (v captureViews) RenderBlock(context.Context, io.Writer, string, string, any) error { return nil }

func TestSecurity_A10_ErrorViewErrorOnlyInDebug(t *testing.T) {
	t.Parallel()

	cause := errs.New(http.StatusConflict, "This slug is already taken.")

	for _, debug := range []bool{false, true} {
		var got errorData
		runError(t, cause, nil, &Services{Views: captureViews{got: &got}, Debug: debug})

		if got.Status != http.StatusConflict {
			t.Fatalf("debug %v: view status = %d, want 409", debug, got.Status)
		}
		if debug && !errors.Is(got.Error, cause) {
			t.Errorf("debug: .Error = %v, want the handler's error", got.Error)
		}
		if !debug && got.Error != nil {
			t.Errorf("production: .Error = %v, want nil (CWE-209)", got.Error)
		}
	}
}

func TestErrorHandlerProblemExtensions(t *testing.T) {
	t.Parallel()

	jsonReq := func(r *http.Request) { r.Header.Set("Accept", "application/json") }

	t.Run("members are added next to the standard ones", func(t *testing.T) {
		t.Parallel()

		err := errs.New(http.StatusTooManyRequests, "Daily quota reached.").
			WithExtension("code", "quota-exceeded").
			WithExtension("limit", 1000).
			WithExtension("status", 200). // never overrides a standard member
			WithExtension("request_id", "forged")

		rec, _ := runError(t, err, jsonReq, nil)

		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}

		want := map[string]any{
			"type": "about:blank", "title": "Too Many Requests", "status": float64(429),
			"detail": "Daily quota reached.", "instance": "/posts", "request_id": "req-1",
			"code": "quota-exceeded", "limit": float64(1000),
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("problem diff (-want +got):\n%s", diff)
		}
	})
}

// TestSecurity_A10_ProblemExtensionsNotSentOn5xx checks that extension
// members of a 5xx error stay out of the response, like its message.
func TestSecurity_A10_ProblemExtensionsNotSentOn5xx(t *testing.T) {
	t.Parallel()

	err := errs.Wrap(http.StatusInternalServerError, errors.New("boom")).WithExtension("query", "SELECT secret")

	rec, _ := runError(t, err, func(r *http.Request) { r.Header.Set("Accept", "application/json") }, nil)
	if strings.Contains(rec.Body.String(), "secret") {
		t.Errorf("5xx problem leaked an extension: %s", rec.Body.String())
	}
}

func TestProblemTypeTitleAndDecorator(t *testing.T) {
	t.Parallel()

	jsonReq := func(r *http.Request) { r.Header.Set("Accept", "application/json") }
	decode := func(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
		t.Helper()

		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}

		return got
	}

	t.Run("WithType and WithTitle set the standard members of a 4xx", func(t *testing.T) {
		t.Parallel()

		err := errs.New(http.StatusTooManyRequests, "Try again tomorrow.").
			WithType("https://example.test/p/cooldown").WithTitle("Export cooldown")

		rec, _ := runError(t, err, jsonReq, nil)
		got := decode(t, rec)
		if got["type"] != "https://example.test/p/cooldown" || got["title"] != "Export cooldown" {
			t.Errorf("problem = %s", rec.Body)
		}
	})

	t.Run("a 5xx keeps about:blank and the status text", func(t *testing.T) {
		t.Parallel()

		err := errs.Wrap(http.StatusInternalServerError, errors.New("boom")).
			WithType("https://example.test/p/secret").WithTitle("secret title")

		rec, _ := runError(t, err, jsonReq, nil)
		if got := decode(t, rec); got["type"] != "about:blank" || got["title"] != "Internal Server Error" {
			t.Errorf("problem = %v", got)
		}
	})

	t.Run("the decorator runs for every problem and cannot touch the error", func(t *testing.T) {
		t.Parallel()

		base := errs.New(http.StatusConflict, "x").WithExtension("code", "orig")
		s := &Services{ProblemDecorator: func(_ *Ctx, p *Problem) {
			code := strings.ToLower(strings.ReplaceAll(http.StatusText(p.Status), " ", "-"))
			if p.Extensions == nil {
				p.Extensions = map[string]any{}
			}

			if _, ok := p.Extensions["code"]; !ok {
				p.Extensions["code"] = code
			}

			p.Extensions["seen"] = true
			p.Type = "https://example.test/p/" + p.Extensions["code"].(string)
		}}

		for _, tt := range []struct {
			err  error
			code string
		}{{errs.ErrNotFound, "not-found"}, {base, "orig"}, {errors.New("boom"), "internal-server-error"}} {
			rec, _ := runError(t, tt.err, jsonReq, s)
			got := decode(t, rec)

			if got["code"] != tt.code || got["type"] != "https://example.test/p/"+tt.code || got["seen"] != true {
				t.Errorf("%v: problem = %v", tt.err, got)
			}
		}

		if _, ok := base.Extensions["seen"]; ok {
			t.Error("decorator mutated the error's extensions")
		}
	})

	t.Run("Ctx.Problem is decorated too", func(t *testing.T) {
		t.Parallel()

		s := &Services{ProblemDecorator: func(_ *Ctx, p *Problem) { p.Type = "https://example.test/p/x" }}
		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
		rec := httptest.NewRecorder()

		NewCtx(rec, r, s).Serve(func(c *Ctx) error { return c.Problem(http.StatusGone, "", "") })

		if got := decode(t, rec); got["type"] != "https://example.test/p/x" {
			t.Errorf("problem = %v", got)
		}
	})
}
