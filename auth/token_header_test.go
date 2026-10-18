package auth_test

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/web"
)

// comparableType instantiates only for a comparable T.
func comparableType[T comparable]() {}

// This instantiation stops compiling if TokenGuard gains a slice, map or
// function field: the apidiff gate counts losing comparability as a breaking
// change ("old is comparable, new is not").
var _ = comparableType[auth.TokenGuard[User]]

// guardRequest runs g.User on a request carrying headers and returns the
// user's email (empty when the request was refused) and the response headers
// the guard set.
func guardRequest(t *testing.T, g *auth.TokenGuard[User], headers map[string]string) (string, http.Header) {
	t.Helper()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	for name, value := range headers {
		r.Header.Add(name, value)
	}

	rec := httptest.NewRecorder()

	u, err := g.User(web.NewCtx(rec, r, nil))
	if err != nil {
		return "", rec.Header()
	}

	return u.Email, rec.Header()
}

func TestTokenGuardExtraHeaders(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ana := ta.user(t, "ana@example.com", "pw", true)
	bob := ta.user(t, "bob@example.com", "pw", true)
	ctx := t.Context()

	anaKey, _, err := ta.tokens.Create(ctx, ana.AuthID(), "ana-cli", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	bobKey, _, err := ta.tokens.Create(ctx, bob.AuthID(), "bob-cli", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	users := auth.NewORMProvider[User](ta.mustDB(t))
	g := auth.NewTokenGuard("api", users, ta.tokens).WithTokenHeaders("X-API-Key", "x-token")

	tests := []struct {
		name    string
		headers map[string]string
		want    string
	}{
		{"bearer alone", map[string]string{"Authorization": "Bearer " + anaKey}, "ana@example.com"},
		{"extra header alone", map[string]string{"X-API-Key": anaKey}, "ana@example.com"},
		{"extra header padded", map[string]string{"X-API-Key": "  " + anaKey + " "}, "ana@example.com"},
		{"second extra header", map[string]string{"X-Token": anaKey}, "ana@example.com"},
		{"name registered in another case", map[string]string{"x-token": anaKey}, "ana@example.com"},
		{
			"authorization wins over an extra header",
			map[string]string{"Authorization": "Bearer " + anaKey, "X-API-Key": bobKey},
			"ana@example.com",
		},
		{
			"first registered header wins between extras",
			map[string]string{"X-API-Key": anaKey, "X-Token": bobKey},
			"ana@example.com",
		},
		{
			"a non-bearer authorization falls through",
			map[string]string{"Authorization": "Basic dXNlcjpwdw==", "X-API-Key": anaKey},
			"ana@example.com",
		},
		{"no credential", nil, ""},
		{"blank extra header", map[string]string{"X-API-Key": "   "}, ""},
		{"unknown key", map[string]string{"X-API-Key": "vk_nope.nope"}, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got, _ := guardRequest(t, g, tt.headers); got != tt.want {
				t.Errorf("User() = %q, want %q", got, tt.want)
			}
		})
	}

	// Two lines of the same header are a guess, not a credential.
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r.Header.Add("X-API-Key", anaKey)
	r.Header.Add("X-API-Key", bobKey)

	if _, err := g.User(web.NewCtx(httptest.NewRecorder(), r, nil)); err == nil {
		t.Error("User() with two X-API-Key lines = nil error, want the request refused")
	}
}

// TestSecurity_A07_TokenHeadersClosedByDefault asserts a guard accepts a
// token only from Authorization until the app names another header, so no
// request becomes authenticated by an upgrade.
func TestSecurity_A07_TokenHeadersClosedByDefault(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ana := ta.user(t, "ana@example.com", "pw", true)

	key, _, err := ta.tokens.Create(t.Context(), ana.AuthID(), "cli", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	bare := auth.NewTokenGuard("api", auth.NewORMProvider[User](ta.mustDB(t)), ta.tokens)

	for _, name := range []string{"X-API-Key", "X-Token", "Api-Key", "X-Auth-Token"} {
		got, h := guardRequest(t, bare, map[string]string{name: key})
		if got != "" {
			t.Errorf("User() with %s and no WithTokenHeaders = %q, want the request refused", name, got)
		}

		if vary := h.Values("Vary"); len(vary) != 0 {
			t.Errorf("Vary = %q with no extra header accepted, want none", vary)
		}
	}

	// The same key still works where the guard does read it.
	if got, _ := guardRequest(t, bare, map[string]string{"Authorization": "Bearer " + key}); got != "ana@example.com" {
		t.Errorf("User() with a bearer token = %q, want ana@example.com", got)
	}

	// Headers that are already credentials, or not header names at all, are
	// refused at wiring time instead of silently ignored.
	for _, name := range []string{"Authorization", "authorization", "Cookie", "", "   ", "X API Key", "X-API-Key:"} {
		t.Run("refuses "+name, func(t *testing.T) {
			t.Parallel()

			defer func() {
				if recover() == nil {
					t.Errorf("WithTokenHeaders(%q) did not panic", name)
				}
			}()

			auth.NewTokenGuard("api", auth.NewORMProvider[User](ta.mustDB(t)), ta.tokens).WithTokenHeaders(name)
		})
	}
}

// TestSecurity_A07_TokenHeadersAlwaysVary asserts every accepted header is
// named in Vary on every response the guard sees, so a shared cache cannot
// serve one key's response to a request holding another.
func TestSecurity_A07_TokenHeadersAlwaysVary(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ana := ta.user(t, "ana@example.com", "pw", true)

	key, _, err := ta.tokens.Create(t.Context(), ana.AuthID(), "cli", nil, 0)
	if err != nil {
		t.Fatal(err)
	}

	g := auth.NewTokenGuard("api", auth.NewORMProvider[User](ta.mustDB(t)), ta.tokens).
		WithTokenHeaders("X-API-Key", "X-Token")

	cases := map[string]map[string]string{
		"accepted key":     {"X-API-Key": key},
		"rejected key":     {"X-API-Key": "vk_nope.nope"},
		"bearer token":     {"Authorization": "Bearer " + key},
		"no credential":    nil,
		"unrelated header": {"Accept": "application/json"},
	}

	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			_, h := guardRequest(t, g, headers)

			vary := h.Values("Vary")
			for _, want := range []string{"X-API-Key", "X-Token"} {
				if !slices.Contains(vary, want) {
					t.Errorf("Vary = %q, want it to name %s", vary, want)
				}
			}

			// Authorization is kept out: RFC 9111 §3.5 already stops shared
			// caches from storing those responses, and listing it would
			// split every cache entry.
			if slices.Contains(vary, "Authorization") {
				t.Errorf("Vary = %q, want no Authorization entry", vary)
			}
		})
	}

	// A second call in the same request does not repeat the entries, and an
	// entry another middleware wrote is left alone.
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil)
	r.Header.Set("X-API-Key", key)
	rec := httptest.NewRecorder()
	rec.Header().Add("Vary", "Accept-Encoding, X-Token")
	c := web.NewCtx(rec, r, nil)

	for range 3 {
		if _, err := g.User(c); err != nil {
			t.Fatalf("User() = %v", err)
		}
	}

	if got := rec.Header().Values("Vary"); !slices.Equal(got, []string{"Accept-Encoding, X-Token", "X-API-Key"}) {
		t.Errorf("Vary = %q, want the existing line kept and X-API-Key added once", got)
	}
}

// TestTokenGuardVaryOnServedResponses asserts the Vary entries reach a real
// response on both the authenticated and the refused path.
func TestTokenGuardVaryOnServedResponses(t *testing.T) {
	t.Parallel()

	ta := newApp(t, hash.Fake())
	ana := ta.user(t, "ana@example.com", "pw", true)

	key, _, err := ta.tokens.Create(t.Context(), ana.AuthID(), "cli", []string{"*"}, 0)
	if err != nil {
		t.Fatal(err)
	}

	ta.Client(t).AcceptJSON().WithHeader("X-API-Key", key).GetJSON("/keyapi/me").
		AssertOK().AssertSee("ana@example.com").AssertHeaderContains("Vary", "X-API-Key")

	ta.Client(t).AcceptJSON().GetJSON("/keyapi/me").
		AssertUnauthorized().AssertHeaderContains("Vary", "X-API-Key")

	// The bearer-only guard on the same app adds nothing to Vary.
	resp := ta.Client(t).AcceptJSON().WithToken(key).GetJSON("/api/me").AssertOK()
	if vary := strings.Join(resp.Recorder().Header().Values("Vary"), ","); strings.Contains(vary, "X-API-Key") {
		t.Errorf("Vary = %q on the bearer-only guard, want no X-API-Key", vary)
	}
}
