package auth_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/web"
)

func TestSecurity_A07_LoginThrottleMatchesHandlerJSON(t *testing.T) {
	t.Parallel()

	_, perAccount := auth.ThrottleKeys("email")

	req := func(target, body string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "192.0.2.1:1234"

		return r
	}

	t.Run("empty body falls through to the query string", func(t *testing.T) {
		t.Parallel()

		if got := perAccount(req("/login?email=Ana@Example.com", "")); got != "login-account:ana@example.com" {
			t.Errorf("perAccount = %q", got)
		}
	})

	t.Run("trailing data is refused like the handler does", func(t *testing.T) {
		t.Parallel()

		got := perAccount(req("/login", `{"email":"ana@example.com"} {"email":"bob@example.com"}`))
		if !strings.HasPrefix(got, "login-unparsed:") {
			t.Errorf("perAccount = %q, want the per-IP fallback", got)
		}
	})

	t.Run("the route's body limit applies", func(t *testing.T) {
		t.Parallel()

		r := req("/login", `{"email":"ana@example.com","pad":"`+strings.Repeat("x", 100)+`"}`)
		r = web.LimitBody(httptest.NewRecorder(), r, 64)

		if got := perAccount(r); !strings.HasPrefix(got, "login-unparsed:") {
			t.Errorf("perAccount = %q, want the per-IP fallback", got)
		}
	})

	t.Run("body is intact for the handler", func(t *testing.T) {
		t.Parallel()

		const body = `{"email":"ana@example.com"}`

		r := req("/login", body)
		perAccount(r)

		if got, _ := io.ReadAll(r.Body); string(got) != body {
			t.Errorf("body after keying = %q", got)
		}
	})
}
