package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/web"
)

// The throttle must read the email the way the login handler does, or a
// request the handler accepts could dodge the per-account bucket.
func TestSecurity_A07_LoginThrottleParserDifferential(t *testing.T) {
	t.Parallel()

	perIP, perAccount := auth.ThrottleKeys("email")

	post := func(target, contentType, body, ip string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		r.Header.Set("Content-Type", contentType)
		r.RemoteAddr = ip + ":1234"

		return r
	}

	const account = "login-account:ana@example.com"

	for _, tt := range []struct {
		name string
		req  *http.Request
	}{
		{"json with charset", post("/login", "application/json; charset=utf-8", `{"email":"Ana@Example.com"}`, "192.0.2.1")},
		{"json media type casing", post("/login", "Application/JSON", `{"email":"ana@example.com"}`, "192.0.2.1")},
		{"+json media type", post("/login", "application/vnd.api+json", `{"email":"ana@example.com"}`, "192.0.2.1")},
		{"query string email", post("/login?email=Ana@Example.com", "application/x-www-form-urlencoded", "", "192.0.2.1")},
		{"json body wins over query", post("/login?email=bob@example.com", "application/json", `{"email":"ana@example.com"}`, "192.0.2.1")},
	} {
		if got := perAccount(tt.req); got != account {
			t.Errorf("%s: perAccount = %q, want %q", tt.name, got, account)
		}
	}

	for _, tt := range []struct {
		name string
		req  func(ip string) *http.Request
	}{
		{"unparsable body", func(ip string) *http.Request { return post("/login", "application/json", `{"email":`, ip) }},
		{"non-string email", func(ip string) *http.Request { return post("/login", "application/json", `{"email":["a"]}`, ip) }},
		{"oversized body", func(ip string) *http.Request {
			return post("/login", "application/json", `{"email":"ana@example.com","pad":"`+strings.Repeat("x", web.DefaultMaxBody)+`"}`, ip)
		}},
		{"no email", func(ip string) *http.Request { return post("/login", "application/x-www-form-urlencoded", "x=1", ip) }},
	} {
		a, b := perAccount(tt.req("192.0.2.1")), perAccount(tt.req("198.51.100.7"))
		if a == "" || b == "" {
			t.Errorf("%s: an unreadable email skipped the limit", tt.name)
		}

		if a == b {
			t.Errorf("%s: unreadable emails from two IPs share bucket %q", tt.name, a)
		}

		if k := perIP(tt.req("192.0.2.1")); k == "" || k == perIP(tt.req("198.51.100.7")) {
			t.Errorf("%s: perIP = %q, want a per-IP bucket", tt.name, k)
		}
	}
}
