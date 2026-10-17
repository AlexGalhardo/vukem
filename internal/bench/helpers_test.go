package bench

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// discardWriter is a reusable ResponseWriter: httptest.ResponseRecorder
// would grow its body buffer for the whole run and skew allocations.
type discardWriter struct {
	h    http.Header
	code int
}

func newDiscardWriter() *discardWriter { return &discardWriter{h: http.Header{}} }

func (w *discardWriter) Header() http.Header  { return w.h }
func (w *discardWriter) WriteHeader(code int) { w.code = code }
func (w *discardWriter) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}

	return len(b), nil
}

func (w *discardWriter) WriteString(s string) (int, error) {
	if w.code == 0 {
		w.code = http.StatusOK
	}

	return len(s), nil
}

func (w *discardWriter) reset() {
	clear(w.h)
	w.code = 0
}

// impl is one contender of a comparison; benchstat pivots on /impl=.
type impl struct {
	name    string
	handler http.Handler
}

// run checks that every impl answers GET target with 200, then benchmarks
// each one with that request.
func run(b *testing.B, impls []impl, target string) {
	b.Helper()

	for _, im := range impls {
		b.Run("impl="+im.name, func(b *testing.B) {
			req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, target, http.NoBody)
			w := newDiscardWriter()

			im.handler.ServeHTTP(w, req)

			if w.code != http.StatusOK {
				b.Fatalf("%s: GET %s = %d, want 200", im.name, target, w.code)
			}

			b.ReportAllocs()

			for b.Loop() {
				w.reset()
				im.handler.ServeHTTP(w, req)
			}
		})
	}
}

func vukemRouter() *router.Router {
	return router.New(&web.Services{Log: slog.New(slog.DiscardHandler)})
}

func mustHandler(b *testing.B, r *router.Router) http.Handler {
	b.Helper()

	h, err := r.Handler()
	if err != nil {
		b.Fatal(err)
	}

	return h
}

func writeText(w http.ResponseWriter, s string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, s)
}

func noop(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { next.ServeHTTP(w, r) })
}
