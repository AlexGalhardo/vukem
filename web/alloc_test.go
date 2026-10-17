package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// nopWriter keeps allocation counts free of recorder buffers.
type nopWriter struct{ h http.Header }

func (w nopWriter) Header() http.Header             { return w.h }
func (nopWriter) WriteHeader(int)                   {}
func (nopWriter) Write(b []byte) (int, error)       { return len(b), nil }
func (nopWriter) WriteString(s string) (int, error) { return len(s), nil }

// TestCtxStringAllocations pins the per-request cost of a Ctx response.
func TestCtxStringAllocations(t *testing.T) {
	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	w := nopWriter{h: http.Header{}}
	s := &Services{}
	body := string([]byte("hello, world")) // not a constant: forces the conversion path

	got := testing.AllocsPerRun(100, func() {
		clear(w.h)
		_ = NewCtx(w, req, s).String(http.StatusOK, body)
	})
	// One for the Ctx, one for the Content-Type header value slice.
	if got != 2 {
		t.Errorf("Ctx.String allocations = %v, want 2", got)
	}
}

func BenchmarkCtxString(b *testing.B) {
	req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/", http.NoBody)
	w := nopWriter{h: http.Header{}}
	s := &Services{}

	b.ReportAllocs()

	for b.Loop() {
		clear(w.h)
		_ = NewCtx(w, req, s).String(http.StatusOK, "ok")
	}
}

func BenchmarkCtxJSON(b *testing.B) {
	type item struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}

	data := make([]item, 20)
	for i := range data {
		data[i] = item{ID: i, Name: "item"}
	}

	req := httptest.NewRequestWithContext(b.Context(), http.MethodGet, "/", http.NoBody)
	w := nopWriter{h: http.Header{}}
	s := &Services{}

	b.ReportAllocs()

	for b.Loop() {
		clear(w.h)
		_ = NewCtx(w, req, s).JSON(http.StatusOK, data)
	}
}
