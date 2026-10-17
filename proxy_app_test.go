package vukem

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// proxyApp serves the client IP at /ip under the default middleware, with
// env added to the test configuration.
func proxyApp(t *testing.T, env map[string]string) *App {
	t.Helper()

	app, err := New(WithSource(testSource(env)), WithLogger(discardLogger()),
		WithRoutes(func(r *router.Router, app *App) error {
			r.Get("/ip", func(c *web.Ctx) error { return c.String(http.StatusOK, c.IP()) })
			return DefaultMiddleware(app, r)
		}))
	if err != nil {
		t.Fatal(err)
	}

	return app
}

func proxyReq(t *testing.T, app *App, peer string, header map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	h, err := app.Handler()
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/ip", http.NoBody)
	req.RemoteAddr = peer
	for k, v := range header {
		req.Header.Set(k, v)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestSecurity_A01_TrustedProxiesFromEnv(t *testing.T) {
	t.Parallel()

	spoof := map[string]string{"X-Forwarded-For": "203.0.113.9"}

	tests := []struct {
		name    string
		proxies string
		peer    string
		want    string
	}{
		// Default: nobody is trusted, so a spoofed header is ignored.
		{name: "unset trusts nobody", peer: "10.1.2.3:4000", want: "10.1.2.3"},
		{name: "cidr from trusted peer", proxies: "10.0.0.0/8", peer: "10.1.2.3:4000", want: "203.0.113.9"},
		{name: "untrusted peer is ignored", proxies: "10.0.0.0/8", peer: "198.51.100.7:4000", want: "198.51.100.7"},
		{name: "bare ip and spaces", proxies: " 192.0.2.1 , 10.0.0.0/8", peer: "192.0.2.1:4000", want: "203.0.113.9"},
		{name: "bare ip is exact", proxies: "192.0.2.1", peer: "192.0.2.2:4000", want: "192.0.2.2"},
		{name: "ipv6 cidr", proxies: "fd00::/8", peer: "[fd00::1]:4000", want: "203.0.113.9"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			app := proxyApp(t, map[string]string{"TRUSTED_PROXIES": tt.proxies})

			rec := proxyReq(t, app, tt.peer, spoof)
			if got := rec.Body.String(); got != tt.want {
				t.Fatalf("IP() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSecurity_A02_TrustedProxiesMisconfigurationRefusesBoot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "bad cidr", env: map[string]string{"TRUSTED_PROXIES": "10.0.0.0/33"}, want: "TRUSTED_PROXIES"},
		{
			name: "hostname",
			env:  map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8,proxy.internal"},
			want: "proxy.internal",
		},
		// X-Forwarded-Proto from any peer would let clients claim HTTPS.
		{
			name: "proto without proxies",
			env:  map[string]string{"TRUST_FORWARDED_PROTO": "true"},
			want: "TRUST_FORWARDED_PROTO requires TRUSTED_PROXIES",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(WithSource(testSource(tt.env)), WithLogger(discardLogger()))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSecurity_A02_TrustForwardedProtoOnlyFromTrustedProxies(t *testing.T) {
	t.Parallel()

	https := map[string]string{"X-Forwarded-Proto": "https"}

	tests := []struct {
		name     string
		env      map[string]string
		peer     string
		wantHSTS bool
	}{
		{name: "off by default", env: map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8"}, peer: "10.0.0.1:1"},
		{
			name:     "trusted proxy",
			env:      map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8", "TRUST_FORWARDED_PROTO": "true"},
			peer:     "10.0.0.1:1",
			wantHSTS: true,
		},
		{
			name: "spoofed by a client",
			env:  map[string]string{"TRUSTED_PROXIES": "10.0.0.0/8", "TRUST_FORWARDED_PROTO": "true"},
			peer: "198.51.100.7:1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := proxyReq(t, proxyApp(t, tt.env), tt.peer, https)
			if got := rec.Header().Get("Strict-Transport-Security") != ""; got != tt.wantHSTS {
				t.Fatalf("HSTS sent = %v, want %v", got, tt.wantHSTS)
			}
		})
	}
}

func TestAddrJoinsHostAndPort(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host string
		want string
	}{
		{host: "localhost", want: "localhost:8080"},
		{host: "0.0.0.0", want: "0.0.0.0:8080"},
		// IPv6 hosts need brackets, or "::" becomes the invalid ":::8080".
		{host: "::", want: "[::]:8080"},
		{host: "[::]", want: "[::]:8080"},
		{host: "fd12::1", want: "[fd12::1]:8080"},
	}

	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()

			app := newTestApp(t, WithSource(testSource(map[string]string{"HOST": tt.host, "PORT": "8080"})))
			if got := app.Addr(); got != tt.want {
				t.Fatalf("Addr() = %q, want %q", got, tt.want)
			}

			if _, _, err := net.SplitHostPort(app.Addr()); err != nil {
				t.Fatalf("Addr() %q is not host:port: %v", app.Addr(), err)
			}
		})
	}
}
