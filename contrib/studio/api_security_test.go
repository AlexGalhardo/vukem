package studio

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type apiCall struct{ method, target, body string }

// apiRequest builds an API request from a loopback peer.
func apiRequest(c apiCall) *http.Request {
	r := httptest.NewRequest(c.method, "http://127.0.0.1:4984"+c.target, strings.NewReader(c.body))
	r.RemoteAddr = "127.0.0.1:50000"

	if c.body != "" {
		r.Header.Set("Content-Type", "application/json")
	}

	return r
}

// everyAPIRoute lists a request for each route of the API, read and write.
func everyAPIRoute() []apiCall {
	routes := []apiCall{
		{"GET", "/api/v1/bootstrap", ""},
		{"GET", "/api/v1/sources/db/tables", ""},
		{"GET", "/api/v1/sources/db/tables/users", ""},
		{"GET", "/api/v1/sources/db/tables/users/rows", ""},
		{"GET", "/api/v1/sources/db/tables/users/rows/1", ""},
		{"GET", "/api/v1/sources/db/tables/users/references", ""},
		{"GET", "/api/v1/sources/db/tables/users/count", ""},
		{"GET", "/api/v1/sources/db/schema", ""},
		{"GET", "/api/v1/sources/db/tables/users/create", ""},
		{"GET", "/api/v1/sources/db/tables/users/export", ""},
		{"GET", "/api/v1/sources/mongo/collections/users/export", ""},
		{"POST", "/api/v1/sources/db/query", `{"sql":"SELECT 1"}`},
		{"POST", "/api/v1/sources/db/explain", `{"sql":"SELECT 1"}`},
		{"GET", "/api/v1/sources/mongo/collections", ""},
		{"GET", "/api/v1/sources/mongo/collections/users/documents", ""},
		{"POST", "/api/v1/sources/mongo/find", `{"collection":"users","filter":"{}"}`},
		{"GET", "/api/v1/sources/redis/keys", ""},
		{"GET", "/api/v1/sources/redis/keys/session:abc", ""},
		{"POST", "/api/v1/sources/redis/command", `{"line":"GET a"}`},
	}

	for _, w := range writeRoutes() {
		routes = append(routes, apiCall(w))
	}

	return routes
}

func TestSecurityAPI(t *testing.T) {
	t.Parallel()

	sh, h := mixedStudio(t, true)
	routes := everyAPIRoute()

	t.Run("no cookie gives 401", func(t *testing.T) {
		t.Parallel()

		for _, rt := range routes {
			rec := serve(h.s, apiRequest(rt))
			if rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s without a cookie = %d, want 401", rt.method, rt.target, rec.Code)
			}
		}
	})

	t.Run("a wrong cookie gives 401", func(t *testing.T) {
		t.Parallel()

		for _, rt := range routes {
			r := apiRequest(rt)
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "not-the-session"})

			if rec := serve(h.s, r); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s %s with a wrong cookie = %d, want 401", rt.method, rt.target, rec.Code)
			}
		}
	})

	t.Run("a non-loopback host, proxy header or peer gives 403", func(t *testing.T) {
		t.Parallel()

		mutations := map[string]func(*http.Request){
			"host":  func(r *http.Request) { r.Host = "studio.example.com" },
			"peer":  func(r *http.Request) { r.RemoteAddr = "203.0.113.5:1234" },
			"xff":   func(r *http.Request) { r.Header.Set("X-Forwarded-For", "203.0.113.5") },
			"proxy": func(r *http.Request) { r.Header.Set("Forwarded", "for=203.0.113.5") },
			"via":   func(r *http.Request) { r.Header.Set("Via", "1.1 proxy") },
		}

		for _, rt := range routes {
			for name, mutate := range mutations {
				r := apiRequest(rt)
				r.AddCookie(h.login)
				mutate(r)

				if rec := serve(h.s, r); rec.Code != http.StatusForbidden {
					t.Errorf("%s %s with %s = %d, want 403", rt.method, rt.target, name, rec.Code)
				}
			}
		}
	})

	t.Run("a cross-origin write is refused", func(t *testing.T) {
		t.Parallel()

		for _, rt := range routes {
			if rt.method == "GET" {
				continue
			}

			r := apiRequest(rt)
			r.AddCookie(h.login)
			r.Header.Set("Origin", "https://evil.example")
			r.Header.Set("Sec-Fetch-Site", "cross-site")

			if rec := serve(h.s, r); rec.Code != http.StatusForbidden {
				t.Errorf("cross-origin %s %s = %d, want 403", rt.method, rt.target, rec.Code)
			}
		}
	})

	t.Run("a body that is not JSON is refused", func(t *testing.T) {
		t.Parallel()

		for _, rt := range routes {
			if rt.method == "GET" {
				continue
			}

			for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "multipart/form-data", ""} {
				r := apiRequest(rt)
				r.AddCookie(h.login)
				r.Header.Set("Content-Type", ct)

				if rec := serve(h.s, r); rec.Code != http.StatusUnsupportedMediaType {
					t.Errorf("%s %s as %q = %d, want 415", rt.method, rt.target, ct, rec.Code)
				}
			}
		}
	})

	t.Run("a body over 1 MiB is refused", func(t *testing.T) {
		t.Parallel()

		big := `{"sql":"` + strings.Repeat("a", 1<<20) + `"}`
		rec := apiDo(h.s, h.login, "POST", "/api/v1/sources/db/query", big)

		if rec.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("a 1 MiB+ body = %d, want 413", rec.Code)
		}
	})

	t.Run("unknown fields and trailing data are refused", func(t *testing.T) {
		t.Parallel()

		for name, body := range map[string]string{
			"unknown field": `{"sql":"SELECT 1","extra":true}`,
			"two values":    `{"sql":"SELECT 1"}{"sql":"SELECT 2"}`,
			"not an object": `"SELECT 1"`,
		} {
			rec := apiDo(h.s, h.login, "POST", "/api/v1/sources/db/query", body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("%s = %d, want 422", name, rec.Code)
			}
		}
	})

	t.Run("an unknown name is refused before any SQL runs", func(t *testing.T) {
		before := sh.count.n.Load()

		for _, target := range []string{
			"/api/v1/sources/db/tables/nope",
			"/api/v1/sources/db/tables/nope/rows",
			"/api/v1/sources/db/tables/users;DROP%20TABLE%20users/rows",
			"/api/v1/sources/db/tables/users/rows?sort=nope",
			"/api/v1/sources/db/tables/users/rows?sort=id%3BDROP%20TABLE%20users",
			"/api/v1/sources/db/tables/users/rows?filter=nope:eq:1",
			"/api/v1/sources/db/tables/users/rows?filter=id:nope:1",
			"/api/v1/sources/db/tables/nope/references",
			"/api/v1/sources/db/tables/nope/rows/1",
		} {
			rec := apiGet(h.s, h.login, target)
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusUnprocessableEntity {
				t.Errorf("GET %s = %d, want 404 or 422", target, rec.Code)
			}
		}

		if got := sh.count.n.Load(); got != before {
			t.Errorf("%d queries reached the database for names outside the catalog, want 0", got-before)
		}

		if n := sh.users(t); n != 5 {
			t.Errorf("users has %d rows, want 5", n)
		}
	})

	t.Run("no response has a CORS header", func(t *testing.T) {
		t.Parallel()

		for _, rt := range routes {
			r := apiRequest(rt)
			r.AddCookie(h.login)
			r.Header.Set("Origin", "http://127.0.0.1:4984")

			assertNoCORS(t, rt.method+" "+rt.target, serve(h.s, r))
		}

		pre := apiRequest(apiCall{"OPTIONS", "/api/v1/sources/db/query", ""})
		pre.AddCookie(h.login)
		pre.Header.Set("Origin", "https://evil.example")
		pre.Header.Set("Access-Control-Request-Method", "POST")
		assertNoCORS(t, "OPTIONS preflight", serve(h.s, pre))
	})

	t.Run("responses carry the API headers", func(t *testing.T) {
		t.Parallel()

		rec := apiGet(h.s, h.login, "/api/v1/bootstrap")
		for name, want := range map[string]string{
			"Cache-Control":                "no-store",
			"X-Content-Type-Options":       "nosniff",
			"Cross-Origin-Resource-Policy": "same-origin",
			"Content-Security-Policy":      strictCSP,
		} {
			if got := rec.Header().Get(name); got != want {
				t.Errorf("%s = %q, want %q", name, got, want)
			}
		}
	})

	t.Run("the console refuses a write however it is disguised", func(t *testing.T) {
		t.Parallel()

		for name, write := range map[string]bool{"--write on, write switch off": true, "no --write": false} {
			sh, h := mixedStudio(t, write)
			consoleRefusesDisguisedWrites(t, name, sh, h)
		}
	})

	t.Run("errors never contain database text", func(t *testing.T) {
		t.Parallel()

		leaks := []string{
			"no such table",
			"no such column",
			"syntax error",
			"table_that_is_not_there",
			"sqlite",
			"near",
		}

		for _, sql := range []string{
			"SELECT * FROM table_that_is_not_there",
			"SELECT nope FROM users",
			"SELECT 1 FROM",
		} {
			rec := apiDo(h.s, h.login, "POST", "/api/v1/sources/db/query", queryBodyJSON(t, sql, false))
			body := strings.ToLower(rec.Body.String())

			for _, leak := range leaks {
				if strings.Contains(body, leak) {
					t.Errorf("the error for %q leaks %q: %s", sql, leak, rec.Body)
				}
			}

			if rec.Code < 400 {
				t.Errorf("%q = %d, want an error", sql, rec.Code)
			}
		}
	})
}

func assertNoCORS(t *testing.T, what string, rec *httptest.ResponseRecorder) {
	t.Helper()

	for name := range rec.Header() {
		if strings.HasPrefix(strings.ToLower(name), "access-control-") {
			t.Errorf("%s: response has the CORS header %s", what, name)
		}
	}
}
