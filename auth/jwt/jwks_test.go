package jwt_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/auth/jwt"
)

type jwksServer struct {
	*httptest.Server

	mu    sync.Mutex
	set   jwt.JWKS
	calls int
}

func newJWKSServer(t *testing.T) *jwksServer {
	t.Helper()

	s := &jwksServer{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.mu.Lock()
		defer s.mu.Unlock()

		s.calls++
		_ = json.NewEncoder(w).Encode(s.set)
	}))
	t.Cleanup(s.Close)

	return s
}

func (s *jwksServer) publish(t *testing.T, keys ...jwt.Key) {
	t.Helper()

	set, err := jwt.PublicJWKS(keys...)
	if err != nil {
		t.Fatal(err)
	}

	s.mu.Lock()
	s.set = set
	s.mu.Unlock()
}

func TestPublicJWKSRoundTrip(t *testing.T) {
	t.Parallel()

	ks := keys(t)

	set, err := jwt.PublicJWKS(ks...)
	if err != nil {
		t.Fatal(err)
	}

	if len(set.Keys) != 3 {
		t.Fatalf("published %d keys, want 3 (HMAC stays private): %+v", len(set.Keys), set.Keys)
	}

	for i, j := range set.Keys {
		k, err := j.Key()
		if err != nil {
			t.Fatalf("JWK %s: %v", j.Kid, err)
		}

		tok, err := jwt.Sign(ks[i+1], claims())
		if err != nil {
			t.Fatal(err)
		}

		if _, err := verifier(k).Verify(t.Context(), tok); err != nil {
			t.Errorf("%s token with the published key: %v", j.Kid, err)
		}
	}

	if _, err := (jwt.JWK{Kty: "oct", Kid: "x"}).Key(); err == nil {
		t.Error("symmetric JWK accepted")
	}
}

func TestRemoteJWKSRotation(t *testing.T) {
	t.Parallel()

	ks := keys(t)
	srv := newJWKSServer(t)
	srv.publish(t, ks[1])

	clock := now
	remote := &jwt.RemoteJWKS{URL: srv.URL, Now: func() time.Time { return clock }}
	v := jwt.NewVerifier(
		remote,
		jwt.VerifyOptions{Issuer: "https://vukem.test", Audience: "api", Now: func() time.Time { return now }},
	)

	sign := func(k jwt.Key) string {
		tok, err := jwt.Sign(k, claims())
		if err != nil {
			t.Fatal(err)
		}

		return tok
	}

	if _, err := v.Verify(t.Context(), sign(ks[1])); err != nil {
		t.Fatalf("first key: %v", err)
	}

	// The provider rotates to a new key; the first token with its kid
	// triggers a refetch once the minimum interval has passed.
	srv.publish(t, ks[1], ks[2])

	if _, err := v.Verify(t.Context(), sign(ks[2])); err == nil {
		t.Error("unknown kid accepted before MinRefresh elapsed")
	}

	clock = clock.Add(2 * time.Minute)

	if _, err := v.Verify(t.Context(), sign(ks[2])); err != nil {
		t.Fatalf("rotated key: %v", err)
	}

	calls := srv.calls
	for range 5 {
		_, _ = v.Verify(t.Context(), sign(ks[3])) // never published
	}

	if srv.calls != calls {
		t.Errorf("unknown kids refetched the JWKS %d times within MinRefresh", srv.calls-calls)
	}
}

func TestSecurity_A07_RemoteJWKSRequiresAudienceAndIssuer(t *testing.T) {
	t.Parallel()

	ks := keys(t)
	srv := newJWKSServer(t)
	srv.publish(t, ks[1])

	// A token the shared identity provider issued for another app.
	other := claims()
	other.Audience = []string{"someone-else"}

	tok, err := jwt.Sign(ks[1], other)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		opts jwt.VerifyOptions
		ok   bool
	}{
		{"no audience", jwt.VerifyOptions{Issuer: "https://vukem.test"}, false},
		{"no issuer", jwt.VerifyOptions{Audience: "someone-else"}, false},
		{"neither", jwt.VerifyOptions{}, false},
		{"wrong audience", jwt.VerifyOptions{Issuer: "https://vukem.test", Audience: "api"}, false},
		{"bound", jwt.VerifyOptions{Issuer: "https://vukem.test", Audience: "someone-else"}, true},
		{"explicit opt-out", jwt.VerifyOptions{InsecureSkipAudience: true, InsecureSkipIssuer: true}, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tt.opts.Now = clock
			misconfigured := tt.opts.Audience == "" && !tt.opts.InsecureSkipAudience ||
				tt.opts.Issuer == "" && !tt.opts.InsecureSkipIssuer

			// A custom KeySet wrapping the remote one is just as shared.
			for name, ks := range map[string]jwt.KeySet{
				"remote":  &jwt.RemoteJWKS{URL: srv.URL},
				"wrapped": loggingKeys{&jwt.RemoteJWKS{URL: srv.URL}},
			} {
				v := jwt.NewVerifier(ks, tt.opts)

				if (v.Err() != nil) != misconfigured {
					t.Fatalf("%s: Err() = %v, want misconfigured %v", name, v.Err(), misconfigured)
				}

				_, err := v.Verify(t.Context(), tok)
				if (err == nil) != tt.ok || err != nil && !errors.Is(err, jwt.ErrInvalid) {
					t.Fatalf("%s: Verify() = %v, want ok %v", name, err, tt.ok)
				}

				// The configuration detail stays out of Verify's error.
				if misconfigured && strings.Contains(err.Error(), "VerifyOptions") {
					t.Errorf("%s: Verify() = %q, leaks the configuration", name, err)
				}

				if misconfigured {
					assertGuardPanics(t, v)
				}
			}
		})
	}
}

// loggingKeys is a custom KeySet that forwards to a remote one.
type loggingKeys struct{ *jwt.RemoteJWKS }

func (l loggingKeys) Key(ctx context.Context, kid string) (jwt.Key, error) { return l.RemoteJWKS.Key(ctx, kid) }

func assertGuardPanics(t *testing.T, v *jwt.Verifier) {
	t.Helper()

	defer func() {
		if recover() == nil {
			t.Error("NewGuard with a misconfigured verifier did not fail at boot")
		}
	}()

	jwt.NewGuard[user]("api", v, users{})
}
