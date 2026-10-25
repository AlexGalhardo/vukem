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

func TestSecurity_A07_LoginThrottleBoundsOversizedEmail(t *testing.T) {
	t.Parallel()

	perIP, perAccount := auth.ThrottleKeys("email")

	req := func(email, ip string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(`{"email":"`+email+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = ip + ":1234"

		return r
	}

	tests := []struct {
		name  string
		email string
		raw   bool // the key embeds the email as is
	}{
		{name: "254 bytes is embedded", email: strings.Repeat("a", 242) + "@example.com", raw: true},
		{name: "255 bytes is digested", email: strings.Repeat("a", 243) + "@example.com"},
		{name: "1 MiB is digested", email: strings.Repeat("a", 1<<20)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			keys := map[string]string{
				"perIP":      perIP(req(tt.email, "192.0.2.1")),
				"perAccount": perAccount(req(tt.email, "192.0.2.1")),
			}
			for name, key := range keys {
				if got := strings.Contains(key, tt.email); got != tt.raw {
					t.Errorf("%s key embeds email = %v, want %v (key length %d)", name, got, tt.raw, len(key))
				}

				if !tt.raw && len(key) > 128 {
					t.Errorf("%s key length = %d, want <= 128", name, len(key))
				}
			}

			// The account dimension survives the digest: one account from
			// two networks shares perAccount, never perIP.
			if a, b := perAccount(req(tt.email, "192.0.2.1")), perAccount(req(tt.email, "198.51.100.7")); a != b {
				t.Errorf("perAccount splits one account across IPs: %.40q vs %.40q", a, b)
			}

			if a, b := perIP(req(tt.email, "192.0.2.1")), perIP(req(tt.email, "198.51.100.7")); a == b {
				t.Errorf("perIP shares a bucket across IPs: %.40q", a)
			}

			if a, b := perAccount(req(tt.email, "192.0.2.1")), perAccount(req(tt.email+"b", "192.0.2.1")); a == b {
				t.Errorf("two accounts share perAccount bucket %.40q", a)
			}
		})
	}

	t.Run("a short email cannot forge a digested key", func(t *testing.T) {
		t.Parallel()

		long := strings.Repeat("a", 300)
		digested := perAccount(req(long, "192.0.2.1"))
		forged := "sha256:" + digested[strings.LastIndexByte(digested, ':')+1:]

		if got := perAccount(req(forged, "192.0.2.1")); got == digested {
			t.Errorf("perAccount(%q) = %q, the bucket of a 300-byte email", forged, got)
		}
	})
}
