package jwt_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/jwt"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

// lookupUsers answers every lookup with err.
type lookupUsers struct{ err error }

func (p lookupUsers) ByID(context.Context, string) (user, error) { return user{id: "42"}, p.err }

func (lookupUsers) ByCredentials(context.Context, map[string]string) (user, error) {
	return user{}, auth.ErrUnauthenticated
}

// keySetFunc adapts a function to jwt.KeySet.
type keySetFunc func(ctx context.Context, kid string) (jwt.Key, error)

func (f keySetFunc) Key(ctx context.Context, kid string) (jwt.Key, error) { return f(ctx, kid) }

// failingTransport fails every request, like an unreachable identity provider.
type failingTransport struct{}

func (failingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("dial tcp: connection refused")
}

func guardHandler(t *testing.T, guard auth.Checker) http.Handler {
	t.Helper()

	r := router.New(nil)
	r.Get("/me", func(c *web.Ctx) error {
		return c.String(http.StatusOK, "ok")
	}).Use(auth.Required(guard))

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	return h
}

func callGuard(t *testing.T, h http.Handler, authz string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/me", http.NoBody)
	req.Header.Set("Accept", "text/html")

	if authz != "" {
		req.Header.Set("Authorization", authz)
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func TestSecurity_A04_JWTGuardOutageIs503(t *testing.T) {
	t.Parallel()

	k := keys(t)[0]

	tok, err := jwt.Sign(k, claims())
	if err != nil {
		t.Fatal(err)
	}

	opts := jwt.VerifyOptions{Issuer: "https://vukem.test", Audience: "api", Now: clock}
	unreachable := &jwt.RemoteJWKS{URL: "https://idp.invalid/jwks", Client: &http.Client{Transport: failingTransport{}}}

	tests := []struct {
		name     string
		verifier *jwt.Verifier
		users    auth.UserProvider[user]
		want     int
	}{
		{"valid", verifier(k), lookupUsers{}, http.StatusOK},
		{"user not found", verifier(k), lookupUsers{auth.ErrUserNotFound}, http.StatusUnauthorized},
		{
			"wrapped sql.ErrNoRows",
			verifier(k),
			lookupUsers{fmt.Errorf("load: %w", sql.ErrNoRows)},
			http.StatusUnauthorized,
		},
		{"provider says unauthenticated", verifier(k), lookupUsers{auth.ErrUnauthenticated}, http.StatusUnauthorized},
		{"user store down", verifier(k), lookupUsers{errors.New("connection refused")}, http.StatusServiceUnavailable},
		{"JWKS unreachable", jwt.NewVerifier(unreachable, opts), lookupUsers{}, http.StatusServiceUnavailable},
		{
			"custom key set outage",
			jwt.NewVerifier(keySetFunc(func(context.Context, string) (jwt.Key, error) {
				return jwt.Key{}, fmt.Errorf("vault: %w", jwt.ErrKeysUnavailable)
			}), opts),
			lookupUsers{},
			http.StatusServiceUnavailable,
		},
		{
			"unknown key id",
			jwt.NewVerifier(keySetFunc(func(context.Context, string) (jwt.Key, error) {
				return jwt.Key{}, errors.New("unknown key id")
			}), opts),
			lookupUsers{},
			http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			rec := callGuard(t, guardHandler(t, jwt.NewGuard("api", tt.verifier, tt.users)), "Bearer "+tok)
			if rec.Code != tt.want {
				t.Fatalf("GET /me = %d, want %d (%s)", rec.Code, tt.want, rec.Body)
			}

			if tt.want == http.StatusServiceUnavailable {
				if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
					t.Errorf("503 Content-Type = %q, want application/problem+json", ct)
				}

				if body := rec.Body.String(); strings.Contains(body, "refused") ||
					strings.Contains(body, "idp.invalid") {
					t.Errorf("503 body leaks the cause: %s", body)
				}
			}
		})
	}
}

func TestSecurity_A04_JWTGuardOutageStopsRequired(t *testing.T) {
	t.Parallel()

	k := keys(t)[0]

	tok, err := jwt.Sign(k, claims())
	if err != nil {
		t.Fatal(err)
	}

	down := jwt.NewGuard("api", verifier(k), lookupUsers{errors.New("connection refused")})
	fallback := jwt.NewGuard("other", verifier(k), lookupUsers{})

	r := router.New(nil)
	r.Get("/me", func(c *web.Ctx) error {
		return c.String(http.StatusOK, "ok")
	}).Use(auth.Required(down, fallback))

	h, err := r.Handler()
	if err != nil {
		t.Fatal(err)
	}

	// An outage in the first guard must not fall through to the next one.
	if rec := callGuard(t, h, "Bearer "+tok); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("Required(down, fallback) = %d, want 503", rec.Code)
	}
}

func TestGuardBearerSchemeCaseInsensitive(t *testing.T) {
	t.Parallel()

	k := keys(t)[0]

	tok, err := jwt.Sign(k, claims())
	if err != nil {
		t.Fatal(err)
	}

	h := guardHandler(t, jwt.NewGuard("api", verifier(k), lookupUsers{}))

	tests := []struct {
		authz string
		want  int
	}{
		{"Bearer " + tok, http.StatusOK},
		{"bearer " + tok, http.StatusOK},
		{"BEARER " + tok, http.StatusOK},
		{"Bearer   " + tok + " ", http.StatusOK},
		{"Bearer" + tok, http.StatusUnauthorized},
		{"Bearer ", http.StatusUnauthorized},
		{"Basic " + tok, http.StatusUnauthorized},
	}

	for _, tt := range tests {
		if rec := callGuard(t, h, tt.authz); rec.Code != tt.want {
			t.Errorf("Authorization %.12q... = %d, want %d", tt.authz, rec.Code, tt.want)
		}
	}
}
