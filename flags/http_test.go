package flags_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func TestRequire(t *testing.T) {
	t.Parallel()

	store := newMemStore()
	f := newFlags(t, flags.Config{Store: store})

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })

	status := func(h http.Handler, scope flags.Scope) int {
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
		if scope != "" {
			req = req.WithContext(flags.WithScope(req.Context(), scope))
		}

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		return rec.Code
	}

	h := f.Require(checkout)(ok)
	if got := status(h, ""); got != http.StatusNotFound {
		t.Errorf("Require(off) = %d, want 404", got)
	}

	store.put("user:1", "new_checkout", "true")

	if got := status(h, "user:1"); got != http.StatusNoContent {
		t.Errorf("Require() for a scope with the flag on = %d, want 204", got)
	}

	if got := status(f.Require(flags.Bool("ghost", true))(ok), ""); got != http.StatusNotFound {
		t.Errorf("Require(unregistered) = %d, want 404", got)
	}
}

func TestShareAddsInertiaProp(t *testing.T) {
	t.Parallel()

	f := newFlags(t, flags.Config{})

	in, err := inertia.New(inertia.Config{})
	if err != nil {
		t.Fatal(err)
	}

	r := router.New(&web.Services{})
	r.Use(in.Middleware, f.Share(checkout, limit))
	r.Get("/", func(c *web.Ctx) error { return inertia.Render(c, "Home", nil) })

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	req.Header.Set("X-Inertia", "true")

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var page struct {
		Props map[string]any `json:"props"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("body %q: %v", rec.Body.String(), err)
	}

	want := map[string]any{"new_checkout": false, "upload_limit_mb": float64(10)}
	if diff := cmp.Diff(want, page.Props["flags"]); diff != "" {
		t.Errorf("flags prop (-want +got):\n%s", diff)
	}
}
