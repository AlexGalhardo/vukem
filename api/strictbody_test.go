package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type strictLine struct {
	SKU string `json:"sku" validate:"required"`
}

type strictOrder struct {
	Note  string       `json:"note"`
	Page  int          `json:"page"  validate:"min=0"`
	Lines []strictLine `json:"lines"`
}

func TestSecurity_StrictBodyRefusesUnknownKeys(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	handler := func(_ *web.Ctx, in strictOrder) (strictOrder, error) { return in, nil }
	api.Register(r, "POST /strict", api.Op{StrictBody: true}, handler)
	api.Register(r, "POST /lax", api.Op{}, handler)
	api.Register(r, "GET /strict", api.Op{StrictBody: true}, handler)

	tests := []struct {
		name, method, target, body string
		status                     int
		wantPath                   string
	}{
		{"known keys pass", "POST", "/strict", `{"note":"a","lines":[{"sku":"x"}]}`, 201, ""},
		{"unknown top-level key", "POST", "/strict", `{"note":"a","extra":1}`, 422, `"path":"extra"`},
		{
			"unknown nested key",
			"POST",
			"/strict",
			`{"lines":[{"sku":"x"},{"sku":"y","qty":2}]}`,
			422,
			`"path":"lines.1.qty"`,
		},
		{"query stays free", "POST", "/strict?utm=1&page=2", `{"note":"a"}`, 201, ""},
		{"query cannot hide a body key", "POST", "/strict?note=a", `{"extra":1}`, 422, `"path":"extra"`},
		{"default keeps ignoring unknown keys", "POST", "/lax", `{"note":"a","extra":1}`, 201, ""},
		{"no body on GET", "GET", "/strict?utm=1", "", 200, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.target, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != tt.status || !strings.Contains(rec.Body.String(), tt.wantPath) {
				t.Fatalf(
					"%s %s = %d %s, want %d containing %q",
					tt.method,
					tt.target,
					rec.Code,
					rec.Body,
					tt.status,
					tt.wantPath,
				)
			}

			if tt.status == http.StatusUnprocessableEntity && !strings.Contains(rec.Body.String(), `"on":"body"`) {
				t.Fatalf("%s %s = %s, want the issue marked as coming from the body", tt.method, tt.target, rec.Body)
			}
		})
	}
}
