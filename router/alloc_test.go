package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AlexGalhardo/vukem/web"
)

type nopWriter struct{ h http.Header }

func (w nopWriter) Header() http.Header             { return w.h }
func (nopWriter) WriteHeader(int)                   {}
func (nopWriter) Write(b []byte) (int, error)       { return len(b), nil }
func (nopWriter) WriteString(s string) (int, error) { return len(s), nil }

// TestRouteAllocations pins the per-request allocations of a matched route:
// the services context (value + request copy), the Ctx with its embedded
// ResponseWriter, and the Content-Type header value.
func TestRouteAllocations(t *testing.T) {
	r := newRouter()
	r.Get("/hello", func(c *web.Ctx) error { return c.String(http.StatusOK, "ok") })

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/hello", http.NoBody)
	w := nopWriter{h: http.Header{}}

	got := testing.AllocsPerRun(100, func() {
		clear(w.h)
		h.ServeHTTP(w, req)
	})
	if got != 4 {
		t.Errorf("GET /hello allocations = %v, want 4", got)
	}
}
