package api_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/middleware"
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

func serve(t *testing.T, h http.Handler, contentType, body string, chunked bool) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/note", strings.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	if chunked {
		req.ContentLength = -1
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func maxBodyRouter(n int64) *router.Router {
	r := router.New(nil)
	api.Register(r, "POST /note", api.Op{MaxBody: n},
		func(_ *web.Ctx, in noteIn) (noteIn, error) { return in, nil })

	return r
}

func TestSecurity_MaxBodyKeepsABodyReplacedByMiddleware(t *testing.T) {
	t.Parallel()

	// A webhook verifier reads the body under the router limit, checks the
	// signature and puts the verified bytes back.
	replace := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)
				return
			}

			r.Body = io.NopCloser(bytes.NewReader(body))
			next.ServeHTTP(w, r)
		})
	}

	h := globalLimit(replace(maxBodyRouter(4096)), 2048)
	payload := `{"text":"` + strings.Repeat("a", 100) + `"}`

	rec := serve(t, h, "application/json", payload, false)
	if rec.Code != http.StatusCreated || !strings.Contains(rec.Body.String(), strings.Repeat("a", 100)) {
		t.Fatalf("POST /note = %d %s, want 201 with the verified body", rec.Code, rec.Body)
	}

	over := `{"text":"` + strings.Repeat("a", 5000) + `"}`
	if rec := serve(
		t,
		replace(globalLimit(maxBodyRouter(4096), 1<<20)),
		"application/json",
		over,
		false,
	); rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("POST /note over MaxBody = %d, want 413", rec.Code)
	}
}

func TestSecurity_MaxBodyAfterPeekJSON(t *testing.T) {
	t.Parallel()

	peek := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = web.PeekJSON(r)
			next.ServeHTTP(w, r)
		})
	}

	body := func(n int) string { return `{"text":"` + strings.Repeat("a", n) + `"}` }

	tests := []struct {
		name    string
		global  int64
		maxBody int64
		body    string
		want    int
	}{
		{"raised above the global limit", 64, 4096, body(1000), http.StatusCreated},
		{"raised limit still refuses", 64, 4096, body(5000), http.StatusRequestEntityTooLarge},
		{"lowered below the global limit", 4096, 32, body(100), http.StatusRequestEntityTooLarge},
		{"lowered limit accepts a small body", 4096, 64, body(10), http.StatusCreated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := globalLimit(peek(maxBodyRouter(tt.maxBody)), tt.global)

			rec := serve(t, h, "application/json", tt.body, false)
			if rec.Code != tt.want {
				t.Fatalf("POST /note = %d %s, want %d", rec.Code, rec.Body, tt.want)
			}

			if tt.want == http.StatusCreated && !strings.Contains(rec.Body.String(), `"text":"aaaa`) {
				t.Fatalf("POST /note body = %s, want the peeked body intact", rec.Body)
			}
		})
	}
}

func TestSecurity_MaxBodyLowersAFormParsedByMethodOverride(t *testing.T) {
	t.Parallel()

	h := globalLimit(middleware.MethodOverride()(maxBodyRouter(32)), 1<<20)
	form := "application/x-www-form-urlencoded"

	tests := []struct {
		name, contentType, body string
		chunked                 bool
		want                    int
	}{
		{"urlencoded over MaxBody", form, "text=" + strings.Repeat("a", 100), false, http.StatusRequestEntityTooLarge},
		{"chunked urlencoded fails closed", form, "text=a", true, http.StatusRequestEntityTooLarge},
		{"urlencoded under MaxBody", form, "text=a", false, http.StatusCreated},
		{"chunked JSON under MaxBody", "application/json", `{"text":"a"}`, true, http.StatusCreated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := serve(t, h, tt.contentType, tt.body, tt.chunked)
			if rec.Code != tt.want {
				t.Fatalf("POST /note = %d %s, want %d", rec.Code, rec.Body, tt.want)
			}
		})
	}
}
