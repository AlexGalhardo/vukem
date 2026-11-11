package social

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/web"
)

// The first discovery fetch must not hold a lock that every login of the
// provider needs: a second login reaches the server while the first waits.
func TestOIDCDiscoveryDoesNotSerializeLogins(t *testing.T) {
	t.Parallel()

	var (
		hits    atomic.Int32
		first   = make(chan struct{})
		second  = make(chan struct{})
		release = make(chan struct{})
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch hits.Add(1) {
		case 1:
			close(first)
			<-release
		case 2:
			close(second)
		}

		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": "http://" + r.Host, "authorization_endpoint": "http://" + r.Host + "/authorize",
			"token_endpoint": "http://" + r.Host + "/token", "jwks_uri": "http://" + r.Host + "/jwks",
		})
	}))
	t.Cleanup(srv.Close)

	p := OIDC("idp", srv.URL, cfg())
	done := make(chan error, 2)
	login := func() {
		_, err := p.AuthURL(t.Context(), srv.Client(), "state", "challenge", "nonce")
		done <- err
	}

	go login()

	<-first

	go login()

	select {
	case <-second:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("the second login did not reach the server while the first discovery was pending")
	}

	close(release)

	for range 2 {
		if err := <-done; err != nil {
			t.Errorf("AuthURL() = %v", err)
		}
	}
}

// noNetwork is a client that fails the test when it is used.
func noNetwork(t *testing.T) *http.Client {
	t.Helper()

	return &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("unexpected request to %s", r.URL)
		return nil, http.ErrNotSupported
	})}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// discoveryServer serves a discovery document built by doc from its own URL.
func discoveryServer(t *testing.T, doc func(base string) map[string]any) *httptest.Server {
	t.Helper()

	var srv *httptest.Server

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}

		_ = json.NewEncoder(w).Encode(doc(srv.URL))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func goodDoc(base string) map[string]any {
	return map[string]any{
		"issuer": base, "authorization_endpoint": base + "/authorize",
		"token_endpoint": base + "/token", "jwks_uri": base + "/jwks",
	}
}

func TestSecurity_A02_OIDCIssuerRequiresHTTPS(t *testing.T) {
	t.Parallel()

	// A plain http issuer on a public host is refused before any request.
	_, err := OIDC("idp", "http://idp.example.com", cfg()).AuthURL(t.Context(), noNetwork(t), "s", "c", "n")
	if err == nil || !strings.Contains(err.Error(), "must use https") {
		t.Fatalf("AuthURL() error = %v, want an https error", err)
	}

	// InsecureHTTP lifts it: the request is made.
	var hit atomic.Bool

	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		hit.Store(true)
		return nil, http.ErrNotSupported
	})}

	_, _ = OIDC("idp", "http://idp.example.com", cfg()).InsecureHTTP().AuthURL(t.Context(), client, "s", "c", "n")

	if !hit.Load() {
		t.Error("InsecureHTTP did not allow the request")
	}

	// Loopback needs no opt-out (tests, local identity providers).
	srv := discoveryServer(t, goodDoc)
	if _, err := OIDC("idp", srv.URL, cfg()).AuthURL(t.Context(), srv.Client(), "s", "c", "n"); err != nil {
		t.Errorf("loopback http issuer = %v, want accepted", err)
	}
}

func TestSecurity_A07_OIDCDiscoveryIssuerMustMatch(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		issuer  func(base string) string
		wantErr string
	}{
		{"other issuer", func(string) string { return "https://evil.example" }, "does not match"},
		{"same issuer with a trailing slash", func(b string) string { return b + "/" }, ""},
		{"tenant placeholder on the same host", func(b string) string { return b + "/{tenantid}/v2.0" }, ""},
		{
			"tenant placeholder on another host",
			func(string) string { return "https://evil.example/{tenantid}/v2.0" },
			"does not match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			srv := discoveryServer(t, func(base string) map[string]any {
				d := goodDoc(base)
				d["issuer"] = tt.issuer(base)

				return d
			})

			_, err := OIDC("idp", srv.URL, cfg()).AuthURL(t.Context(), srv.Client(), "s", "c", "n")
			if tt.wantErr == "" && err != nil {
				t.Fatalf("AuthURL() = %v, want accepted", err)
			}

			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("AuthURL() error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestSecurity_A02_OIDCEndpointsRequireHTTPS(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"authorization_endpoint", "token_endpoint", "jwks_uri", "userinfo_endpoint"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()

			srv := discoveryServer(t, func(base string) map[string]any {
				d := goodDoc(base)
				d[field] = "http://other.example/x"

				return d
			})

			_, err := OIDC("idp", srv.URL, cfg()).AuthURL(t.Context(), srv.Client(), "s", "c", "n")
			if err == nil || !strings.Contains(err.Error(), "must use https") {
				t.Fatalf("AuthURL() error = %v, want an https error for %s", err, field)
			}
		})
	}
}

func TestSecurity_A07_SocialStateCookieUsesHostPrefix(t *testing.T) {
	t.Parallel()

	redirect := func(t *testing.T, s *Social) *http.Cookie {
		t.Helper()

		rec := httptest.NewRecorder()
		req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/acme/redirect", http.NoBody)

		if err := s.Redirect(web.NewCtx(rec, req, nil), "acme"); err != nil {
			t.Fatal(err)
		}

		return rec.Result().Cookies()[0]
	}

	f := newFakeServer(t)

	secure := redirect(t, New(testKey, OIDC("acme", f.URL, cfg())))
	if secure.Name != "__Host-social_acme" || !secure.Secure || secure.Path != "/" || secure.Domain != "" {
		t.Errorf("default state cookie = %+v, want __Host-social_acme with Secure, Path=/ and no Domain", secure)
	}

	insecure := New(testKey, OIDC("acme", f.URL, cfg()))
	insecure.InsecureCookies = true

	if c := redirect(t, insecure); c.Name != "social_acme" {
		t.Errorf("InsecureCookies state cookie name = %q, want social_acme", c.Name)
	}

	// The callback reads the cookie under the same name.
	if _, err := flow(t, New(testKey, OIDC("acme", f.URL, cfg())), f, "acme", nil); err != nil {
		t.Errorf("callback with the prefixed cookie = %v", err)
	}
}

// The https check looks at the first URL only unless redirects are checked
// too: a provider endpoint must not be able to bounce the app, with the code
// and client secret, to plain http or to another host.
func TestSecurity_A02_SocialClientRefusesRedirectsToHTTP(t *testing.T) {
	t.Parallel()

	const evil = "http://evil.example/steal"

	var mux http.ServeMux

	srv := httptest.NewServer(&mux)
	t.Cleanup(srv.Close)

	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(goodDoc(srv.URL))
	})
	mux.HandleFunc("/moved/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil, http.StatusFound)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, evil, http.StatusTemporaryRedirect) // keeps POST, body and secret
	})
	mux.HandleFunc(
		"/userinfo",
		func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, evil, http.StatusFound) },
	)
	mux.HandleFunc(
		"/jwks",
		func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, evil, http.StatusFound) },
	)

	client := New(testKey).HTTP
	p := OIDC("idp", srv.URL, cfg())

	d, err := p.discover(t.Context(), client)
	if err != nil {
		t.Fatal(err)
	}

	steps := map[string]func() error{
		"discovery": func() error {
			_, err := OIDC("moved", srv.URL+"/moved", cfg()).discover(t.Context(), client)
			return err
		},
		"token":    func() error { _, err := p.Exchange(t.Context(), client, "code", "verifier"); return err },
		"userinfo": func() error { return getJSON(t.Context(), client, srv.URL+"/userinfo", "tok", &map[string]any{}) },
		"jwks":     func() error { _, err := p.key(t.Context(), client, d, "kid"); return err },
	}

	for name, step := range steps {
		if err := step(); err == nil || !strings.Contains(err.Error(), "must use https") {
			t.Errorf("%s: error = %v, want the redirect to http refused", name, err)
		}
	}
}

func TestSecurity_A02_SocialRedirectPolicy(t *testing.T) {
	t.Parallel()

	req := func(raw string) *http.Request {
		r, err := http.NewRequestWithContext(t.Context(), http.MethodGet, raw, http.NoBody)
		if err != nil {
			t.Fatal(err)
		}

		return r
	}

	tests := []struct {
		name, from, to string
		hops           int
		wantErr        bool
	}{
		{"https to https", "https://idp.example", "https://other.example/x", 1, false},
		{"https to http", "https://idp.example", "http://other.example/x", 1, true},
		{"https to http loopback", "https://idp.example", "http://127.0.0.1/x", 1, true},
		{"loopback to loopback", "http://127.0.0.1:1", "http://127.0.0.1:2/x", 1, false},
		{"loopback to http elsewhere", "http://127.0.0.1:1", "http://other.example/x", 1, true},
		{"too many hops", "https://idp.example", "https://idp.example/y", 5, true},
	}

	for _, tt := range tests {
		via := make([]*http.Request, tt.hops)
		for i := range via {
			via[i] = req(tt.from)
		}

		if err := redirectGuard(req(tt.to), via); (err != nil) != tt.wantErr {
			t.Errorf("%s: redirectGuard() = %v, wantErr %v", tt.name, err, tt.wantErr)
		}
	}
}

// The single-use delete of the state cookie must carry the same __Host- name,
// or the browser keeps the real one.
func TestSecurity_A07_SocialStateCookieDeleteUsesHostPrefix(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		insecure bool
		want     string
	}{"default": {false, "__Host-social_acme"}, "insecure": {true, "social_acme"}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := New(testKey, OIDC("acme", "https://idp.example.com", cfg()))
			s.InsecureCookies = tt.insecure

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/auth/acme/callback", http.NoBody)

			_, _ = s.User(web.NewCtx(rec, req, nil), "acme") // fails: no state cookie, still clears it

			cookies := rec.Result().Cookies()
			if len(cookies) != 1 || cookies[0].Name != tt.want || cookies[0].MaxAge >= 0 {
				t.Errorf("cookies = %v, want one deleting %s", cookies, tt.want)
			}
		})
	}
}
