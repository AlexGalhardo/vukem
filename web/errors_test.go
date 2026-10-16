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

	"github.com/AlexGalhardo/golang-framework/errs"
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
