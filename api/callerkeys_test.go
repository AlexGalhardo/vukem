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

type labelsIn struct {
	Values map[string]string `json:"values"`
}

// A map key is caller data: a 422 must not repeat one that could forge a
// line of the message or the logs ("x\nhint: y").
func TestSecurity_ValidationNeverEchoesUnsafeMapKeys(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	api.Register(r, "POST /labels", api.Op{}, func(_ *web.Ctx, in labelsIn) (labelsIn, error) { return in, nil })

	tests := []struct {
		name, body, want string
	}{
		{"unsafe key", `{"values":{"x\nhint: y":1}}`, `"values.?"`},
		{"safe key", `{"values":{"pt-BR":1}}`, `"values.pt-BR"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/labels", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")

			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			body := rec.Body.String()
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, tt.want) {
				t.Errorf("POST /labels = %d %s, want 422 naming %s", rec.Code, body, tt.want)
			}

			if strings.Contains(body, "hint") {
				t.Errorf("POST /labels = %s, want no trace of the caller's key", body)
			}
		})
	}
}
