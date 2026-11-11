package liveui

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serve sends one request straight to the handler, as a loopback client unless the test changes it.
func serve(t *testing.T, s *Server, method, target string, edit func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), method, target, http.NoBody)
	req.RemoteAddr = "127.0.0.1:50000"
	req.Host = "127.0.0.1:53124"

	if edit != nil {
		edit(req)
	}

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	return rec
}

// withToken sends a valid session: the cookie a browser holds after it traded the start URL, minted directly so the
// one-use token stays available to the test.
func withToken(s *Server) func(*http.Request) {
	return func(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieName, Value: s.newSession()}) }
}

func TestSecurity_A01_BenchUIRejectsNonLoopbackPeer(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{})

	for _, peer := range []string{"203.0.113.5:1", "10.0.0.7:4000", "[2001:db8::1]:80", "garbage"} {
		rec := serve(t, s, http.MethodGet, "/?token="+s.Token(), func(r *http.Request) { r.RemoteAddr = peer })
		if rec.Code != http.StatusForbidden {
			t.Errorf("peer %q answered %d, want 403 even with the right token", peer, rec.Code)
		}
	}

	for _, peer := range []string{"127.0.0.1:1", "[::1]:1", "[::ffff:127.0.0.1]:1"} {
		if rec := serve(
			t,
			s,
			http.MethodGet,
			"/",
			func(r *http.Request) { r.RemoteAddr = peer },
		); rec.Code == http.StatusForbidden {
			t.Errorf("loopback peer %q was refused", peer)
		}
	}
}

func TestSecurity_A01_BenchUIRejectsProxyHeaders(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{})

	for _, h := range []string{"Forwarded", "X-Forwarded-For", "X-Forwarded-Host", "X-Forwarded-Proto", "X-Forwarded-Port", "X-Real-Ip", "Via"} {
		rec := serve(t, s, http.MethodGet, "/", func(r *http.Request) {
			withToken(s)(r)
			r.Header.Set(h, "203.0.113.9")
		})
		if rec.Code != http.StatusForbidden {
			t.Errorf("a request with %s answered %d, want 403", h, rec.Code)
		}
	}
}

func TestSecurity_A01_BenchUIRejectsForeignHost(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{})

	for _, host := range []string{"evil.example", "evil.example:80", "0.0.0.0:3000", "localhost.evil.example"} {
		rec := serve(t, s, http.MethodGet, "/", func(r *http.Request) {
			withToken(s)(r)
			r.Host = host
		})
		if rec.Code != http.StatusForbidden {
			t.Errorf("Host %q answered %d, want 403 (DNS rebinding)", host, rec.Code)
		}
	}

	for _, host := range []string{"localhost:5000", "127.0.0.1", "[::1]:5000"} {
		rec := serve(t, s, http.MethodGet, "/", func(r *http.Request) {
			withToken(s)(r)
			r.Host = host
		})
		if rec.Code != http.StatusOK {
			t.Errorf("Host %q answered %d, want 200", host, rec.Code)
		}
	}
}

func TestSecurity_A07_BenchUIRequiresToken(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{})

	for name, edit := range map[string]func(*http.Request){
		"no token":     nil,
		"wrong token":  func(r *http.Request) { r.URL.RawQuery = "token=" + strings.Repeat("A", len(s.Token())) },
		"empty token":  func(r *http.Request) { r.URL.RawQuery = "token=" },
		"wrong cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieName, Value: "nope"}) },
		"short token":  func(r *http.Request) { r.URL.RawQuery = "token=" + s.Token()[:5] },
	} {
		for _, path := range []string{"/", "/events", "/summary.json", "/report.html", "/assets/live.js"} {
			if rec := serve(t, s, http.MethodGet, path, edit); rec.Code != http.StatusUnauthorized {
				t.Errorf("%s on %s answered %d, want 401", name, path, rec.Code)
			}
		}
	}
}

func TestSecurity_A07_BenchUITokenBecomesCookie(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{})

	rec := serve(t, s, http.MethodGet, "/?token="+s.Token()+"&x=1", nil)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}

	if loc := rec.Header().Get("Location"); strings.Contains(loc, "token") || !strings.HasPrefix(loc, "/") {
		t.Fatalf("Location = %q, want the URL without the token", loc)
	}

	var c *http.Cookie

	for _, ck := range rec.Result().Cookies() {
		if ck.Name == cookieName {
			c = ck
		}
	}

	if c == nil || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.Value == "" {
		t.Fatalf("cookie = %+v, want HttpOnly, SameSite=Strict, Path=/", c)
	}

	// The cookie, not the token, opens the page afterwards.
	if rec := serve(t, s, http.MethodGet, "/", func(r *http.Request) { r.AddCookie(c) }); rec.Code != http.StatusOK {
		t.Fatalf("the cookie was refused: %d", rec.Code)
	}
}

func TestSecurity_A01_BenchUIIsReadOnly(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{})

	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, path := range []string{"/", "/events", "/summary.json", "/control/stop", "/control/rate", "/control/quit", "/assets/live.js"} {
			rec := serve(t, s, method, path, withToken(s))
			if rec.Code < 400 {
				t.Errorf(
					"%s %s answered %d: the dashboard has no controls, so nothing but a GET may succeed",
					method,
					path,
					rec.Code,
				)
			}
		}
	}
}

func TestSecurity_A01_BenchUIRejectsCrossSiteRequests(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{})

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		rec := serve(t, s, method, "/", func(r *http.Request) {
			withToken(s)(r)
			r.Header.Set("Sec-Fetch-Site", "cross-site")
		})
		if rec.Code != http.StatusForbidden {
			t.Errorf("a cross-site %s answered %d, want 403", method, rec.Code)
		}
	}

	rec := serve(t, s, http.MethodPost, "/control/stop", func(r *http.Request) {
		withToken(s)(r)
		r.Header.Set("Origin", "https://evil.example")
	})
	if rec.Code != http.StatusForbidden {
		t.Errorf("a POST with a foreign Origin answered %d, want 403", rec.Code)
	}

	if rec := serve(t, s, http.MethodGet, "/", func(r *http.Request) {
		withToken(s)(r)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}); rec.Code != http.StatusOK {
		t.Errorf("a same-origin GET answered %d", rec.Code)
	}
}

func TestSecurity_A05_BenchUISendsStrictCSP(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{})

	const csp = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
		"form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'; connect-src 'self'"

	for _, path := range []string{"/", "/assets/live.css", "/summary.json"} {
		h := serve(t, s, http.MethodGet, path, withToken(s)).Header()

		if got := h.Get("Content-Security-Policy"); got != csp {
			t.Errorf("%s CSP = %q, want %q", path, got, csp)
		}

		if h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Referrer-Policy") != "no-referrer" ||
			h.Get("Cache-Control") != "no-store" {
			t.Errorf("%s headers = %v", path, h)
		}
	}

	// The refusals carry the headers too.
	if h := serve(t, s, http.MethodGet, "/", nil).Header(); h.Get("Content-Security-Policy") == "" {
		t.Error("a 401 has no CSP")
	}

	if strings.Contains(csp, "unsafe") {
		t.Fatal("the policy must not allow inline code")
	}
}

func TestSecurity_A02_BenchUITokenIsRandom(t *testing.T) {
	t.Parallel()

	seen := map[string]bool{}

	for range 20 {
		s, _ := newServer(t, Config{})

		tok := s.Token()
		if len(tok) != 43 || seen[tok] {
			t.Fatalf("token %q: want 43 distinct URL-safe characters (32 random bytes)", tok)
		}

		seen[tok] = true
	}
}

func TestSecurity_A07_BenchUICookieIsNotTheToken(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{})

	rec := serve(t, s, http.MethodGet, "/?token="+s.Token(), nil)

	var c *http.Cookie

	for _, ck := range rec.Result().Cookies() {
		if ck.Name == cookieName {
			c = ck
		}
	}

	if c == nil {
		t.Fatal("no cookie")
	}

	// The cookie goes to every service on localhost, the app under test included: it must not be the secret of the URL.
	if c.Value == s.Token() || strings.Contains(c.Value, s.Token()) || len(c.Value) < 32 {
		t.Fatalf("cookie value %q is the start token or too short", c.Value)
	}

	if c.MaxAge <= 0 || c.MaxAge > 3600 {
		t.Fatalf("Max-Age = %d, want a short lifetime of at most an hour", c.MaxAge)
	}

	// The token itself is not a session.
	if rec := serve(
		t,
		s,
		http.MethodGet,
		"/",
		func(r *http.Request) { r.AddCookie(&http.Cookie{Name: cookieName, Value: s.Token()}) },
	); rec.Code != http.StatusUnauthorized {
		t.Fatalf("the start token worked as a cookie: %d", rec.Code)
	}

	// The cookie the exchange gave opens the page, and every answer renews it.
	rec = serve(t, s, http.MethodGet, "/", func(r *http.Request) { r.AddCookie(c) })
	if rec.Code != http.StatusOK || len(rec.Result().Cookies()) == 0 {
		t.Fatalf("the session cookie = %d with %d renewed cookies", rec.Code, len(rec.Result().Cookies()))
	}
}

func TestSecurity_A07_BenchUITokenWorksOnce(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{})

	if rec := serve(t, s, http.MethodGet, "/?token="+s.Token(), nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("first use = %d, want 303", rec.Code)
	}

	// The URL stays in the browser history and on the process list: once traded it is dead.
	if rec := serve(t, s, http.MethodGet, "/?token="+s.Token(), nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("second use = %d, want 401", rec.Code)
	}
}

func TestSessionsAreBoundedAndExpire(t *testing.T) {
	t.Parallel()

	s, clk := newServer(t, Config{})

	first := s.newSession()
	for range 3 * maxSessions {
		s.newSession()
	}

	if s.validSession(first) {
		t.Error("the oldest session survived more than maxSessions newer ones")
	}

	fresh := s.newSession()
	if !s.validSession(fresh) {
		t.Fatal("a new session was refused")
	}

	clk.set(epoch.Add(sessionLifetime + time.Second))

	if s.validSession(fresh) {
		t.Error("a session outlived its lifetime")
	}
}
