package web

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// Form and JSON body fields (passwords above all) never reach the debug
// page or the terminal report, even after the handler parsed them.
func TestSecurity_A09_DebugReportOmitsBodyFields(t *testing.T) {
	t.Parallel()

	form := url.Values{"email": {"ana@example.com"}, "password": {"hunter2-secret"}}.Encode()

	for _, tt := range []struct {
		name, contentType, body string
	}{
		{"form", "application/x-www-form-urlencoded", form},
		{"json", "application/json", `{"password":"hunter2-secret","email":"ana@example.com"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", strings.NewReader(tt.body))
			r.Header.Set("Content-Type", tt.contentType)
			r.Header.Set("Accept", "text/html")

			var out bytes.Buffer

			c, rec := newCtx(t, r, &Services{Log: slog.New(slog.DiscardHandler), Debug: true, DebugOut: &out})

			if _, err := c.Input(); err != nil {
				t.Fatal(err)
			}

			c.HandleError(debugErr())

			for name, got := range map[string]string{"page": rec.Body.String(), "report": out.String()} {
				if got == "" {
					t.Errorf("%s is empty", name)
				}

				for _, leaked := range []string{"hunter2-secret", "ana@example.com", "password"} {
					if strings.Contains(got, leaked) {
						t.Errorf("debug %s shows body data %q", name, leaked)
					}
				}
			}
		})
	}
}
