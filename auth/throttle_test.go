package auth_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth"
)

func TestSecurity_A07_LoginThrottle(t *testing.T) {
	t.Parallel()

	perIP, perAccount := auth.ThrottleKeys("email")

	form := func(email, ip string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{"email": {email}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.RemoteAddr = ip + ":1234"

		return r
	}

	jsonReq := func(email string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"email":"`+email+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = "192.0.2.1:1234"

		return r
	}

	t.Run("email is normalized", func(t *testing.T) {
		t.Parallel()

		if a, b := perIP(form(" Ana@Example.com ", "192.0.2.1")), perIP(form("ana@example.com", "192.0.2.1")); a != b {
			t.Errorf("perIP keys differ: %q vs %q", a, b)
		}

		if a, b := perAccount(
			form(" Ana@Example.com ", "192.0.2.1"),
		), perAccount(
			form("ana@example.com", "192.0.2.1"),
		); a != b {
			t.Errorf("perAccount keys differ: %q vs %q", a, b)
		}
	})

	t.Run("per-IP key splits by IP, per-account key does not", func(t *testing.T) {
		t.Parallel()

		if perIP(form("ana@example.com", "192.0.2.1")) == perIP(form("ana@example.com", "198.51.100.7")) {
			t.Error("perIP shares a bucket across IPs")
		}

		if perAccount(form("ana@example.com", "192.0.2.1")) != perAccount(form("ana@example.com", "198.51.100.7")) {
			t.Error("perAccount splits one account across IPs")
		}
	})

	t.Run("different emails never share a bucket", func(t *testing.T) {
		t.Parallel()

		if perAccount(form("ana@example.com", "192.0.2.1")) == perAccount(form("bob@example.com", "192.0.2.1")) {
			t.Error("two accounts share a bucket")
		}
	})

	t.Run("non-POST is not limited", func(t *testing.T) {
		t.Parallel()

		r := httptest.NewRequest(http.MethodGet, "/login", http.NoBody)
		if perIP(r) != "" || perAccount(r) != "" {
			t.Error("GET was counted")
		}
	})

	t.Run("JSON body is keyed and still readable", func(t *testing.T) {
		t.Parallel()

		r := jsonReq("Ana@Example.com")
		if got, want := perAccount(r), perAccount(form("ana@example.com", "192.0.2.1")); got != want {
			t.Errorf("perAccount(json) = %q, want %q", got, want)
		}

		body, err := io.ReadAll(r.Body)
		if err != nil || !strings.Contains(string(body), "Ana@Example.com") {
			t.Errorf("body after keying = %q, %v", body, err)
		}
	})
}
