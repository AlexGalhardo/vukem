package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/errs"
)

// insufficientFundsError is a domain error the app maps to a 402.
type insufficientFundsError struct{ Balance int }

func (e *insufficientFundsError) Error() string {
	return fmt.Sprintf("balance %d: internal ledger detail", e.Balance)
}

func TestMapError(t *testing.T) {
	t.Parallel()

	jsonReq := func(r *http.Request) { r.Header.Set("Accept", "application/json") }
	toPaymentRequired := func(_ *Ctx, e *insufficientFundsError) error {
		return errs.New(http.StatusPaymentRequired, "Not enough balance.").WithExtension("balance", e.Balance)
	}

	tests := []struct {
		name       string
		register   func(s *Services)
		err        error
		wantStatus int
		wantDetail string
		wantLog    string
	}{
		{
			"mapped to a status", func(s *Services) { MapError(s, toPaymentRequired) },
			&insufficientFundsError{Balance: 3}, 402, "Not enough balance.", "internal ledger detail",
		},
		{
			"found in a wrapped chain",
			func(s *Services) { MapError(s, toPaymentRequired) },
			fmt.Errorf(
				"pay: %w",
				&insufficientFundsError{Balance: 3},
			),
			402,
			"Not enough balance.",
			"internal ledger detail",
		},
		{
			"the first registered wins", func(s *Services) {
				MapError(s, toPaymentRequired)
				MapError(s, func(*Ctx, *insufficientFundsError) error { return errs.New(http.StatusTeapot, "second") })
			},
			&insufficientFundsError{}, 402, "Not enough balance.", "",
		},
		{
			"a nil result leaves the error alone", func(s *Services) {
				MapError(s, func(*Ctx, *insufficientFundsError) error { return nil })
			},
			&insufficientFundsError{}, 500, "", "internal ledger detail",
		},
		{
			"an error that does not match is untouched", func(s *Services) { MapError(s, toPaymentRequired) },
			errs.ErrNotFound, 404, "", "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			s := &Services{}
			tt.register(s)

			rec, logs := runError(t, tt.err, jsonReq, s)
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}

			var p Problem
			if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
				t.Fatal(err)
			}

			if tt.wantDetail != "" && p.Detail != tt.wantDetail {
				t.Errorf("detail = %q, want %q", p.Detail, tt.wantDetail)
			}

			// The log has the original error, not the one rendered.
			if tt.wantLog != "" && !strings.Contains(logs, tt.wantLog) {
				t.Errorf("log %q lacks the original error %q", logs, tt.wantLog)
			}
		})
	}

	t.Run("extensions of the mapped error are sent", func(t *testing.T) {
		t.Parallel()

		s := &Services{}
		MapError(s, toPaymentRequired)

		rec, _ := runError(t, &insufficientFundsError{Balance: 9}, jsonReq, s)

		var got map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got["balance"] != float64(9) {
			t.Errorf("body = %s (%v)", rec.Body.String(), err)
		}
	})
}

// A mapper chooses the response, not the privacy: what it returns with a 5xx
// status is masked like any other server fault.
func TestSecurity_A10_MappedErrorCannotExposeServerFaults(t *testing.T) {
	t.Parallel()

	s := &Services{}
	MapError(s, func(*Ctx, *insufficientFundsError) error {
		return errs.Wrap(http.StatusInternalServerError, errors.New("dsn=postgres://admin:hunter2@db/app"))
	})

	for _, setup := range []func(*http.Request){
		func(r *http.Request) { r.Header.Set("Accept", "application/json") },
		func(r *http.Request) { r.Header.Set("Accept", "text/html") },
		func(r *http.Request) { r.Header.Set("HX-Request", "true") },
	} {
		rec, _ := runError(t, &insufficientFundsError{}, setup, s)
		if rec.Code != 500 || strings.Contains(rec.Body.String(), "dsn=") ||
			strings.Contains(rec.Body.String(), "hunter2") {
			t.Errorf("mapped 5xx = %d %q, want it masked", rec.Code, rec.Body.String())
		}
	}
}

func TestProblemDefaultType(t *testing.T) {
	t.Parallel()

	const base = "https://api.example.com/problems"

	validation := errs.NewValidation().Add("title", "required")

	tests := []struct {
		name     string
		err      error
		base     string
		wantType string
	}{
		{"404 without a base", errs.ErrNotFound, "", "about:blank"},
		{"404 with a base", errs.ErrNotFound, base, base + "/not-found"},
		{"422 without a base", validation, "", "about:blank"},
		{"422 with a base", validation, base, base + "/validation"},
		{"own type without a base", errs.ErrConflict.WithType("urn:x"), "", "urn:x"},
		{"own type with a base", errs.ErrConflict.WithType("urn:x"), base, "urn:x"},
		{"500 with a base", errors.New("boom"), base, base + "/internal-server-error"},
		{
			"418 keeps only a-z, 0-9 and dashes",
			errs.New(http.StatusTeapot, "short and stout"),
			base,
			base + "/im-a-teapot",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec, _ := runError(t, tt.err, func(r *http.Request) { r.Header.Set("Accept", "application/json") },
				&Services{ProblemBaseURL: tt.base})

			if got := problemType(t, rec.Body.Bytes()); got != tt.wantType {
				t.Errorf("type = %q, want %q", got, tt.wantType)
			}
		})
	}

	t.Run("Ctx.Problem", func(t *testing.T) {
		t.Parallel()

		r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/x", http.NoBody)
		rec := httptest.NewRecorder()
		NewCtx(rec, r, &Services{ProblemBaseURL: base}).Serve(func(c *Ctx) error {
			return c.Problem(http.StatusTeapot, "", "")
		})

		if got := problemType(t, rec.Body.Bytes()); got != base+"/im-a-teapot" {
			t.Errorf("type = %q", got)
		}
	})
}

func problemType(t *testing.T, body []byte) string {
	t.Helper()

	var p struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}

	return p.Type
}

// No format, status or error shape may put a server fault in the body; a 4xx
// belongs to the client and keeps its text.
func TestSecurity_A10_ErrorMaskingMatrix(t *testing.T) {
	t.Parallel()

	const secret = "secret-detail"

	none := func() *Services { return nil }

	formats := map[string]struct {
		setup func(*http.Request)
		s     func() *Services
	}{
		"json": {func(r *http.Request) { r.Header.Set("Accept", "application/json") }, none},
		"html": {func(r *http.Request) { r.Header.Set("Accept", "text/html") }, none},
		"htmx": {func(r *http.Request) { r.Header.Set("HX-Request", "true") }, none},
		"error page": {func(r *http.Request) { r.Header.Set("Accept", "text/html") }, func() *Services {
			return &Services{ErrorPage: func(c *Ctx, e ErrorPage) bool {
				_ = c.JSON(e.Status, map[string]any{
					"title": e.Title, "detail": e.Detail, "errors": e.Errors, "issues": e.Issues,
				})

				return true
			}}
		}},
	}

	faults := map[string]error{
		"plain":             errors.New(secret),
		"wrapped 500":       errs.Wrap(http.StatusInternalServerError, errors.New(secret)),
		"503 with message":  errs.New(http.StatusServiceUnavailable, secret),
		"validation in 5xx": errs.Wrap(http.StatusInternalServerError, errs.NewValidation().Add("f", secret)),
	}

	for fname, f := range formats {
		for ename, e := range faults {
			t.Run(fname+"/"+ename, func(t *testing.T) {
				t.Parallel()

				rec, _ := runError(t, e, f.setup, f.s())
				if rec.Code < 500 || strings.Contains(rec.Body.String(), secret) {
					t.Errorf("%d %q, want a 5xx without the detail", rec.Code, rec.Body.String())
				}
			})
		}

		t.Run(fname+"/4xx keeps its text", func(t *testing.T) {
			t.Parallel()

			rec, _ := runError(t, errs.New(http.StatusForbidden, "visible"), f.setup, f.s())
			if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "visible") {
				t.Errorf("%d %q, want the 4xx text in the body", rec.Code, rec.Body.String())
			}
		})
	}
}
