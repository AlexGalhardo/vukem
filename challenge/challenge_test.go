package challenge_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/AlexGalhardo/vukem/challenge"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// provider fakes a siteverify endpoint: "good" passes, "other-host" passes
// on another hostname, "broken" answers 500, anything else fails.
func provider(t *testing.T, calls *atomic.Int32) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		if r.PostFormValue("secret") != "s3cret" || r.PostFormValue("remoteip") != "192.0.2.1" {
			_ = json.NewEncoder(w).
				Encode(map[string]any{"success": false, "error-codes": []string{"invalid-input-secret"}})
			return
		}

		switch r.PostFormValue("response") {
		case "good":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "hostname": "example.com"})
		case "other-host":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "hostname": "evil.example"})
		case "broken":
			w.WriteHeader(http.StatusInternalServerError)
		default:
			_ = json.NewEncoder(w).
				Encode(map[string]any{"success": false, "error-codes": []string{"invalid-input-response"}})
		}
	}))
	t.Cleanup(srv.Close)

	return srv
}

func serve(t *testing.T, v challenge.Verifier, when func(*web.Ctx) bool) http.Handler {
	t.Helper()

	r := router.New(&web.Services{})
	r.Group("", func(g *router.Group) {
		g.Use(challenge.Middleware(v, when))

		ok := func(c *web.Ctx) error { return c.String(http.StatusOK, "ok") }
		g.Get("/register", ok)
		g.Post("/register", ok)
	})

	return r
}

func post(h http.Handler, form url.Values, header string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/register", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	if header != "" {
		req.Header.Set(challenge.Header, header)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestSecurity_A07_ChallengeFailsClosed(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	srv := provider(t, &calls)
	v := challenge.Turnstile(config.NewSecret("s3cret"))
	v.URL, v.Client, v.Hostname = srv.URL, srv.Client(), "example.com"

	wrongSecret := challenge.Turnstile(config.NewSecret("nope"))
	wrongSecret.URL, wrongSecret.Client = srv.URL, srv.Client()

	tests := []struct {
		name   string
		v      challenge.Verifier
		form   url.Values
		header string
		want   int
	}{
		{"form field", v, url.Values{"cf-turnstile-response": {"good"}}, "", http.StatusOK},
		{"header", v, nil, "good", http.StatusOK},
		{"missing", v, nil, "", http.StatusUnprocessableEntity},
		{"wrong", v, url.Values{"cf-turnstile-response": {"bad"}}, "", http.StatusUnprocessableEntity},
		{"too long", v, nil, strings.Repeat("a", 5000), http.StatusUnprocessableEntity},
		{"other site", v, nil, "other-host", http.StatusUnprocessableEntity},
		{"provider down", v, nil, "broken", http.StatusServiceUnavailable},
		{"bad secret", wrongSecret, nil, "good", http.StatusServiceUnavailable},
		{"no secret", challenge.Turnstile(config.Secret{}), nil, "good", http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := post(serve(t, tt.v, nil), tt.form, tt.header).Code; got != tt.want {
				t.Errorf("POST /register = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestMiddlewareSkipsSafeMethodsAndWhen(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	srv := provider(t, &calls)
	v := challenge.HCaptcha(config.NewSecret("s3cret"))
	v.URL, v.Client = srv.URL, srv.Client()

	rec := httptest.NewRecorder()
	serve(t, v, nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/register", http.NoBody))

	if rec.Code != http.StatusOK {
		t.Errorf("GET /register = %d, want 200", rec.Code)
	}

	never := func(*web.Ctx) bool { return false }
	if got := post(serve(t, v, never), nil, "").Code; got != http.StatusOK {
		t.Errorf("POST with when=false = %d, want 200", got)
	}

	if got := post(serve(t, v, nil), url.Values{"h-captcha-response": {"good"}}, "").Code; got != http.StatusOK {
		t.Errorf("POST hCaptcha field = %d, want 200", got)
	}

	if n := calls.Load(); n != 1 {
		t.Errorf("provider calls = %d, want 1", n)
	}
}
