package studio

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeSource is a Source with no database.
type fakeSource struct{ pingErr error }

func (fakeSource) Name() string                 { return "db" }
func (fakeSource) Kind() Kind                   { return KindSQL }
func (fakeSource) Label() string                { return "sqlite  storage/app.db" }
func (f fakeSource) Ping(context.Context) error { return f.pingErr }
func (fakeSource) Close() error                 { return nil }

func newStudio(t *testing.T) *Studio {
	t.Helper()

	s, err := New(Config{Sources: []Source{fakeSource{}}, Version: "v0.0.0-test"})
	if err != nil {
		t.Fatal(err)
	}

	return s
}

// request builds a request from a loopback peer to a loopback host.
func request(method, target string) *http.Request {
	r := httptest.NewRequest(method, "http://127.0.0.1:4984"+target, http.NoBody)
	r.RemoteAddr = "127.0.0.1:50000"

	return r
}

func serve(s *Studio, r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, r)

	return rec
}

// session logs in with the start-URL token and returns the cookie.
func session(t *testing.T, s *Studio) *http.Cookie {
	t.Helper()

	rec := serve(s, request(http.MethodGet, "/?token="+s.Token()))
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}

	t.Fatalf("no session cookie after the token request (status %d)", rec.Code)

	return nil
}

func TestSecurity_A01_StudioRejectsNonLoopbackPeer(t *testing.T) {
	t.Parallel()

	s := newStudio(t)

	for _, peer := range []string{"203.0.113.5:1234", "[2001:db8::1]:1234", "10.0.0.7:80", "garbage"} {
		r := request(http.MethodGet, "/?token="+s.Token())
		r.RemoteAddr = peer

		if rec := serve(s, r); rec.Code != http.StatusForbidden {
			t.Errorf("peer %q: status = %d, want 403", peer, rec.Code)
		}
	}

	r := request(http.MethodGet, "/?token="+s.Token())
	r.RemoteAddr = "[::1]:1234"

	if rec := serve(s, r); rec.Code == http.StatusForbidden {
		t.Error("an IPv6 loopback peer was refused")
	}
}

func TestSecurity_A01_StudioRejectsProxyHeaders(t *testing.T) {
	t.Parallel()

	s := newStudio(t)

	for _, h := range proxyHeaders {
		r := request(http.MethodGet, "/?token="+s.Token())
		r.Header.Set(h, "203.0.113.5")

		if rec := serve(s, r); rec.Code != http.StatusForbidden {
			t.Errorf("header %s: status = %d, want 403", h, rec.Code)
		}
	}
}

func TestSecurity_A01_StudioRejectsForeignHost(t *testing.T) {
	t.Parallel()

	s := newStudio(t)

	for _, host := range []string{"evil.example", "evil.example:4984", "127.0.0.1.evil.example", "localhost.evil.example"} {
		r := request(http.MethodGet, "/?token="+s.Token())
		r.Host = host

		if rec := serve(s, r); rec.Code != http.StatusForbidden {
			t.Errorf("Host %q: status = %d, want 403 (DNS rebinding)", host, rec.Code)
		}
	}

	for _, host := range []string{"localhost:4984", "127.0.0.1:4984", "[::1]:4984", "studio.localhost:4984"} {
		r := request(http.MethodGet, "/?token="+s.Token())
		r.Host = host

		if rec := serve(s, r); rec.Code == http.StatusForbidden {
			t.Errorf("Host %q was refused", host)
		}
	}
}

func TestSecurity_A07_StudioRequiresSessionToken(t *testing.T) {
	t.Parallel()

	s := newStudio(t)

	for name, target := range map[string]string{
		"no token":    "/",
		"wrong token": "/?token=nope",
		"empty token": "/?token=",
		"asset":       "/assets/studio.css",
	} {
		if rec := serve(s, request(http.MethodGet, target)); rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, rec.Code)
		}
	}

	r := request(http.MethodGet, "/")
	r.AddCookie(&http.Cookie{Name: sessionCookie, Value: "forged"})

	if rec := serve(s, r); rec.Code != http.StatusUnauthorized {
		t.Errorf("forged cookie: status = %d, want 401", rec.Code)
	}

	// The 401 page says where the address is, and never shows the token.
	rec := serve(s, request(http.MethodGet, "/"))
	if !strings.Contains(rec.Body.String(), "terminal") || strings.Contains(rec.Body.String(), s.Token()) {
		t.Errorf("401 body = %q", rec.Body.String())
	}
}

func TestSecurity_A07_StudioTokenBecomesCookie(t *testing.T) {
	t.Parallel()

	s := newStudio(t)

	rec := serve(s, request(http.MethodGet, "/ui/s/db?token="+s.Token()+"&x=1"))
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}

	if loc := rec.Header().Get("Location"); strings.Contains(loc, "token") || loc != "/ui/s/db?x=1" {
		t.Errorf("Location = %q, want the same URL without the token", loc)
	}

	var cookie *http.Cookie

	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}

	if cookie == nil || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode || cookie.Path != "/" {
		t.Fatalf("cookie = %+v, want HttpOnly, SameSite=Strict, Path=/", cookie)
	}

	if cookie.Value == s.Token() {
		t.Error("the cookie carries the URL token itself")
	}

	// The cookie opens the interface.
	r := request(http.MethodGet, "/ui/")
	r.AddCookie(cookie)

	if rec := serve(s, r); rec.Code != http.StatusOK {
		t.Errorf("with the cookie: status = %d, want 200", rec.Code)
	}
}

func TestSecurity_A05_StudioSendsStrictCSP(t *testing.T) {
	t.Parallel()

	s := newStudio(t)
	cookie := session(t, s)

	r := request(http.MethodGet, "/")
	r.AddCookie(cookie)

	// Also on a refusal: the headers do not depend on the outcome.
	for name, rec := range map[string]*httptest.ResponseRecorder{
		"page": serve(s, r), "401": serve(s, request(http.MethodGet, "/")),
	} {
		if got := rec.Header().Get("Content-Security-Policy"); got != strictCSP {
			t.Errorf("%s: CSP = %q, want %q", name, got, strictCSP)
		}

		if rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
			rec.Header().Get("Referrer-Policy") != "no-referrer" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: headers = %v", name, rec.Header())
		}
	}
}

func TestSecurity_A01_StudioRejectsCrossOriginPost(t *testing.T) {
	t.Parallel()

	s := newStudio(t)
	cookie := session(t, s)

	r := request(http.MethodPost, "/api/v1/sources/db/query")
	r.Header.Set("Origin", "https://evil.example")
	r.AddCookie(cookie)

	if rec := serve(s, r); rec.Code != http.StatusForbidden {
		t.Errorf("cross-origin POST: status = %d, want 403", rec.Code)
	}
}

func TestSecurity_A07_StudioTokensAreRandom(t *testing.T) {
	t.Parallel()

	a, b := newStudio(t), newStudio(t)
	if a.Token() == b.Token() || len(a.Token()) != 43 {
		t.Errorf("tokens %q and %q: want distinct, 43 characters (32 bytes, base64url)", a.Token(), b.Token())
	}
}

func TestSecurity_A07_StudioTokenIsSingleUse(t *testing.T) {
	t.Parallel()

	s := newStudio(t)
	target := "/?token=" + s.Token()

	first := serve(s, request(http.MethodGet, target))
	if first.Code != http.StatusSeeOther {
		t.Fatalf("first use = %d, want 303", first.Code)
	}

	// Another browser holding the same URL gets nothing.
	again := serve(s, request(http.MethodGet, target))
	if again.Code != http.StatusUnauthorized || len(again.Result().Cookies()) != 0 {
		t.Errorf("second use = %d with %d cookies, want 401 and none", again.Code, len(again.Result().Cookies()))
	}

	// The browser that used it first keeps its session.
	r := request(http.MethodGet, "/")
	for _, c := range first.Result().Cookies() {
		r.AddCookie(c)
	}

	if got := serve(s, r).Code; got == http.StatusUnauthorized {
		t.Error("the first browser lost its session when the token was refused")
	}
}

func TestRootRedirectsToUI(t *testing.T) {
	t.Parallel()

	s := newStudio(t)
	cookie := session(t, s)

	r := request(http.MethodGet, "/")
	r.AddCookie(cookie)

	rec := serve(s, r)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/ui/" {
		t.Errorf("GET / = %d to %q, want 303 to /ui/", rec.Code, rec.Header().Get("Location"))
	}

	// Without the session the root is as closed as every other path.
	if rec := serve(s, request(http.MethodGet, "/")); rec.Code != http.StatusUnauthorized {
		t.Errorf("GET / without a session = %d, want 401", rec.Code)
	}
}

// TestLegacyPagesAreGone: the pages of the old server-rendered interface no longer exist.
// Behind the session they answer 404; without it, 401 like every other path.
func TestLegacyPagesAreGone(t *testing.T) {
	t.Parallel()

	s := newStudio(t)
	cookie := session(t, s)

	for _, target := range []string{
		"/s/db", "/s/db/t/users", "/s/db/t/users/r?k=1", "/s/db/console", "/s/mongo/c/users", "/s/redis/k/a",
		"/s/db/t/users/export", "/events?view=/s/db", "/assets/app.js", "/assets/studio.js", "/assets/studio.css",
	} {
		r := request(http.MethodGet, target)
		r.AddCookie(cookie)

		if rec := serve(s, r); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d, want 404", target, rec.Code)
		}

		if rec := serve(s, request(http.MethodGet, target)); rec.Code != http.StatusUnauthorized {
			t.Errorf("GET %s without a session = %d, want 401", target, rec.Code)
		}
	}

	for _, target := range []string{"/s/db/t/users", "/s/db/console", "/s/db/t/users/r?k=1"} {
		r := request(http.MethodPost, target)
		r.AddCookie(cookie)

		if rec := serve(s, r); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST %s = %d, want 404", target, rec.Code)
		}
	}
}
