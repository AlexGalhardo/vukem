package api_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

type noteIn struct {
	Text string `json:"text"`
}

// globalLimit wraps h with a router-wide body limit, as DefaultMiddleware does.
func globalLimit(h http.Handler, n int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, web.LimitBody(w, r, n))
	})
}

func TestSecurity_RouteMaxBodyOverridesGlobalLimit(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	handler := func(_ *web.Ctx, in noteIn) (noteIn, error) { return in, nil }
	api.Register(r, "POST /raise", api.Op{MaxBody: 4096}, handler)
	api.Register(r, "POST /lower", api.Op{MaxBody: 32}, handler)
	api.Register(r, "POST /keep", api.Op{}, handler)

	body := func(n int) string { return `{"text":"` + strings.Repeat("a", n) + `"}` }

	tests := []struct {
		name, target string
		global       int64
		body         string
		chunked      bool
		want         int
	}{
		{"raises above the global limit", "/raise", 64, body(1000), false, http.StatusCreated},
		{"raised limit still refuses", "/raise", 64, body(5000), false, http.StatusRequestEntityTooLarge},
		{"raised limit refuses undeclared length", "/raise", 64, body(5000), true, http.StatusRequestEntityTooLarge},
		{"lowers below the global limit", "/lower", 4096, body(100), false, http.StatusRequestEntityTooLarge},
		{"lower limit accepts a small body", "/lower", 4096, body(5), false, http.StatusCreated},
		{"no MaxBody keeps the global limit", "/keep", 64, body(1000), false, http.StatusRequestEntityTooLarge},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, tt.target, strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Accept", "application/json")

			if tt.chunked {
				req.ContentLength = -1
			}

			rec := httptest.NewRecorder()
			globalLimit(r, tt.global).ServeHTTP(rec, req)

			if rec.Code != tt.want {
				t.Fatalf("POST %s = %d %s, want %d", tt.target, rec.Code, rec.Body, tt.want)
			}
		})
	}
}

func TestRegisterRejectsNegativeMaxBody(t *testing.T) {
	t.Parallel()

	defer func() {
		if recover() == nil {
			t.Fatal("Register with a negative MaxBody did not panic")
		}
	}()

	api.Register(router.New(nil), "POST /x", api.Op{MaxBody: -1},
		func(_ *web.Ctx, in noteIn) (noteIn, error) { return in, nil })
}

func TestMaxBodyReachesOpenAPI(t *testing.T) {
	t.Parallel()

	r := router.New(nil)
	api.Register(r, "POST /big", api.Op{Name: "big", MaxBody: 1 << 20},
		func(_ *web.Ctx, in noteIn) (noteIn, error) { return in, nil })
	api.Register(r, "POST /plain", api.Op{Name: "plain"},
		func(_ *web.Ctx, in noteIn) (noteIn, error) { return in, nil })

	doc, err := openapi.Build(r.Routes(), openapi.Info{Title: "T", Version: "1"})
	if err != nil {
		t.Fatalf("Build() = %v", err)
	}

	if got := doc.Paths["/big"]["post"].MaxBodyBytes; got != 1<<20 {
		t.Fatalf("/big x-max-body-bytes = %d, want %d", got, 1<<20)
	}

	if got := doc.Paths["/plain"]["post"].MaxBodyBytes; got != 0 {
		t.Fatalf("/plain x-max-body-bytes = %d, want 0", got)
	}
}
