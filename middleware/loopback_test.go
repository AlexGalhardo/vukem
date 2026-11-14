package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AlexGalhardo/vukem/middleware"
)

func TestSecurity_A01_LoopbackOnlyRefusesRebindingHostsAndOrigins(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remote     string
		host       string
		headers    map[string]string
		port       int
		wantStatus int
	}{
		{"localhost", "127.0.0.1:5000", "localhost:8080", nil, 8080, http.StatusNoContent},
		{"ipv4", "127.0.0.1:5000", "127.0.0.1:8080", nil, 8080, http.StatusNoContent},
		{"ipv6", "[::1]:5000", "[::1]:8080", nil, 8080, http.StatusNoContent},
		{"port unchecked", "127.0.0.1:5000", "localhost:9999", nil, 0, http.StatusNoContent},
		{
			"same origin",
			"127.0.0.1:5000",
			"localhost:8080",
			map[string]string{"Origin": "http://localhost:8080"},
			8080,
			http.StatusNoContent,
		},
		{"rebinding name", "127.0.0.1:5000", "evil.example:8080", nil, 8080, http.StatusForbidden},
		{"subdomain of localhost", "127.0.0.1:5000", "app.localhost:8080", nil, 8080, http.StatusForbidden},
		{"other port", "127.0.0.1:5000", "localhost:9999", nil, 8080, http.StatusForbidden},
		{"remote peer", "203.0.113.9:5000", "localhost:8080", nil, 8080, http.StatusForbidden},
		{
			"proxied",
			"127.0.0.1:5000",
			"localhost:8080",
			map[string]string{"X-Forwarded-For": "203.0.113.9"},
			8080,
			http.StatusForbidden,
		},
		{
			"foreign origin",
			"127.0.0.1:5000",
			"localhost:8080",
			map[string]string{"Origin": "http://evil.example"},
			8080,
			http.StatusForbidden,
		},
		{
			"origin other port",
			"127.0.0.1:5000",
			"localhost:8080",
			map[string]string{"Origin": "http://localhost:9999"},
			8080,
			http.StatusForbidden,
		},
		{
			"https origin",
			"127.0.0.1:5000",
			"localhost:8080",
			map[string]string{"Origin": "https://localhost:8080"},
			8080,
			http.StatusForbidden,
		},
		{
			"null origin",
			"127.0.0.1:5000",
			"localhost:8080",
			map[string]string{"Origin": "null"},
			8080,
			http.StatusForbidden,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			h := middleware.LoopbackOnly(tt.port)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}))

			r := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			r.RemoteAddr, r.Host = tt.remote, tt.host

			for k, v := range tt.headers {
				r.Header.Set(k, v)
			}

			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)

			if w.Code != tt.wantStatus {
				t.Errorf(
					"LoopbackOnly(%d) for %s from %s = %d, want %d",
					tt.port,
					tt.host,
					tt.remote,
					w.Code,
					tt.wantStatus,
				)
			}
		})
	}
}
