package inertia_test

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

func catalog(tb testing.TB, keys int) *i18n.Bundle {
	tb.Helper()

	bundle, err := i18n.New(i18n.Options{Default: "en"})
	if err != nil {
		tb.Fatal(err)
	}

	msgs := make(map[string]any, keys)
	for i := range keys {
		msgs["key"+strconv.Itoa(i)] = "message " + strconv.Itoa(i)
	}

	if err := bundle.Add("en", msgs); err != nil {
		tb.Fatal(err)
	}

	return bundle
}

func TestI18nSharesMessagesOnFirstVisit(t *testing.T) {
	t.Parallel()

	bundle := catalog(t, 3)

	hs := newHarness(t, baseConfig(), func(r *router.Router) {
		r.Get("/", func(c *web.Ctx) error { return inertia.Render(c, "Home", nil) }).Use(
			func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					next.ServeHTTP(w, req.WithContext(i18n.WithBundle(req.Context(), bundle)))
				})
			},
			inertia.I18n(),
		)
	})

	props, _ := page(t, hs.do(t, http.MethodGet, "/", xhr()))["props"].(map[string]any)

	want := map[string]any{"key0": "message 0", "key1": "message 1", "key2": "message 2"}
	if diff := cmp.Diff(want, props["messages"]); diff != "" {
		t.Errorf("messages prop mismatch (-want +got):\n%s", diff)
	}
}

// The client already has the catalog: the middleware must not build it.
func BenchmarkI18nMiddleware(b *testing.B) {
	bundle := catalog(b, 500)

	in, err := inertia.New(baseConfig())
	if err != nil {
		b.Fatal(err)
	}

	h := in.Middleware(inertia.I18n()(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})))

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("X-Inertia", "true")
	req.Header.Set("X-Inertia-Version", "v1")
	req.Header.Set("X-Inertia-Except-Once-Props", "i18n:en")
	req = req.WithContext(i18n.WithBundle(req.Context(), bundle))

	b.ReportAllocs()

	for b.Loop() {
		h.ServeHTTP(httptest.NewRecorder(), req)
	}
}
