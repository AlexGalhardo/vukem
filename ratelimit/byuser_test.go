package ratelimit_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/AlexGalhardo/vukem/internal/principal"
	"github.com/AlexGalhardo/vukem/ratelimit"
)

// A103: neither ByIP nor ByToken keys an authenticated action taken from a
// browser session. ByIP punishes everybody behind one NAT while an
// attacker rotates addresses, and ByToken finds no bearer token on a
// session request, so it falls back to the IP. The limit that matters for
// "10 new API keys an hour per account" is the account.
func TestByUser(t *testing.T) {
	t.Parallel()

	key := ratelimit.ByUser("web")

	req := func(ctx context.Context) *http.Request {
		r := httptest.NewRequestWithContext(ctx, http.MethodPost, "/account/keys", http.NoBody)
		r.RemoteAddr = "203.0.113.7:1234"

		return r
	}

	// Authenticated: the key is the account, whatever the address.
	ana := principal.With(t.Context(), "web", "u-1")
	if got := key(req(ana)); got != "user:web:u-1" {
		t.Errorf("ByUser() authenticated = %q, want user:web:u-1", got)
	}

	// Two addresses, one account: one bucket.
	other := httptest.NewRequestWithContext(ana, http.MethodPost, "/account/keys", http.NoBody)
	other.RemoteAddr = "198.51.100.9:5678"

	if got := key(other); got != "user:web:u-1" {
		t.Errorf("ByUser() from another address = %q, want the same bucket", got)
	}

	// A different guard does not answer for this one.
	api := principal.With(t.Context(), "api", "u-1")
	if got, want := key(req(api)), ratelimit.ByIP(req(api)); got != want {
		t.Errorf("ByUser(web) with an api principal = %q, want the IP key %q", got, want)
	}

	// Anonymous falls back to the IP, never to one shared bucket: an empty
	// key would make Middleware pass the request through unlimited, and a
	// constant one would let any caller exhaust everybody's budget.
	anon := key(req(t.Context()))
	if anon == "" || anon == "user:web:" {
		t.Fatalf("ByUser() anonymous = %q, want an IP key", anon)
	}

	if want := ratelimit.ByIP(req(t.Context())); anon != want {
		t.Errorf("ByUser() anonymous = %q, want %q", anon, want)
	}

	if second := key(httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/x", http.NoBody)); second == anon {
		t.Error("two anonymous callers from different addresses share one bucket")
	}
}
