package jwt_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

func (l loggingKeys) Key(ctx context.Context, kid string) (jwt.Key, error) {
	return l.RemoteJWKS.Key(ctx, kid)
}

func assertGuardPanics(t *testing.T, v *jwt.Verifier) {
	t.Helper()

	defer func() {
		if recover() == nil {
			t.Error("NewGuard with a misconfigured verifier did not fail at boot")
		}
	}()

	jwt.NewGuard[user]("api", v, users{})
}

// gatedJWKS serves set (or status) and, while release is non-nil, holds
// every response until the test sends on release.
type gatedJWKS struct {
	*httptest.Server

	calls   atomic.Int32
	started chan struct{}
	release chan struct{}
	status  int
	set     jwt.JWKS
}

func newGatedJWKS(t *testing.T, gated bool, status int, keys ...jwt.Key) *gatedJWKS {
	t.Helper()

	set, err := jwt.PublicJWKS(keys...)
	if err != nil {
		t.Fatal(err)
	}

	s := &gatedJWKS{started: make(chan struct{}, 16), status: status, set: set}
	if gated {
		s.release = make(chan struct{})
	}

	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		s.calls.Add(1)
		s.started <- struct{}{}

		if s.release != nil {
			<-s.release
		}

		if s.status != 0 {
			w.WriteHeader(s.status)
			return
		}

		_ = json.NewEncoder(w).Encode(s.set)
	}))
	t.Cleanup(s.Close)

	if gated {
		// Runs before s.Close (cleanups are LIFO), so held handlers can finish.
		t.Cleanup(func() { close(s.release) })
	}

	return s
}

type keyResult struct {
	key jwt.Key
	err error
}

func keyAsync(ctx context.Context, r *jwt.RemoteJWKS, kid string) <-chan keyResult {
	ch := make(chan keyResult, 1)

	go func() {
		k, err := r.Key(ctx, kid)
		ch <- keyResult{k, err}
	}()

	return ch
}

func await(t *testing.T, ch <-chan keyResult, what string) keyResult {
	t.Helper()

	select {
	case res := <-ch:
		return res
	case <-time.After(5 * time.Second):
		t.Fatalf("%s: still blocked after 5s", what)
		return keyResult{}
	}
}

func TestRemoteJWKSCachedKeyDoesNotWaitForRefresh(t *testing.T) {
	t.Parallel()

	ks := keys(t)
	srv := newGatedJWKS(t, true, 0, ks[1])

	clock := now
	var mu sync.Mutex
	remote := &jwt.RemoteJWKS{URL: srv.URL, Now: func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return clock
	}}

	first := keyAsync(t.Context(), remote, ks[1].ID)
	<-srv.started
	srv.release <- struct{}{}

	if res := await(t, first, "initial load"); res.err != nil {
		t.Fatalf("Key(%q) = %v, want the key", ks[1].ID, res.err)
	}

	mu.Lock()
	clock = clock.Add(2 * time.Hour) // past MaxAge
	mu.Unlock()

	// The stale lookup starts a refresh the server holds; neither it nor
	// the next lookup may wait for it while the key is cached.
	trigger := keyAsync(t.Context(), remote, ks[1].ID)
	<-srv.started

	if res := await(t, trigger, "stale lookup during refresh"); res.err != nil || res.key.ID != ks[1].ID {
		t.Errorf("Key(%q) during refresh = %v, %v, want the cached key", ks[1].ID, res.key.ID, res.err)
	}

	if res := await(t, keyAsync(t.Context(), remote, ks[1].ID), "second lookup"); res.err != nil {
		t.Errorf("Key(%q) during refresh = %v, want the cached key", ks[1].ID, res.err)
	}
}

func TestSecurity_A04_RemoteJWKSFailedRefreshHonoursMinRefresh(t *testing.T) {
	t.Parallel()

	srv := newGatedJWKS(t, false, http.StatusInternalServerError)

	clock := now
	remote := &jwt.RemoteJWKS{URL: srv.URL, Now: func() time.Time { return clock }}

	for range 2 {
		if _, err := remote.Key(t.Context(), "random"); !errors.Is(err, jwt.ErrKeysUnavailable) {
			t.Errorf("Key(random) with the provider down = %v, want ErrKeysUnavailable", err)
		}
	}

	if got := srv.calls.Load(); got != 1 {
		t.Errorf("provider calls within MinRefresh = %d, want 1", got)
	}

	clock = clock.Add(time.Minute)

	if _, err := remote.Key(t.Context(), "random"); !errors.Is(err, jwt.ErrKeysUnavailable) {
		t.Errorf("Key(random) after MinRefresh = %v, want ErrKeysUnavailable", err)
	}

	if got := srv.calls.Load(); got != 2 {
		t.Errorf("provider calls after MinRefresh = %d, want 2", got)
	}
}

func TestRemoteJWKSCallerCancelDoesNotAbortRefresh(t *testing.T) {
	t.Parallel()

	ks := keys(t)
	srv := newGatedJWKS(t, true, 0, ks[1])
	remote := &jwt.RemoteJWKS{URL: srv.URL, Now: func() time.Time { return now }}

	ctx, cancel := context.WithCancel(t.Context())
	first := keyAsync(ctx, remote, ks[1].ID)
	<-srv.started
	cancel()
	await(t, first, "cancelled caller")

	second := keyAsync(t.Context(), remote, ks[1].ID)
	srv.release <- struct{}{}

	if res := await(t, second, "waiting caller"); res.err != nil || res.key.ID != ks[1].ID {
		t.Errorf("Key(%q) after the first caller left = %v, %v, want the key", ks[1].ID, res.key.ID, res.err)
	}

	if got := srv.calls.Load(); got != 1 {
		t.Errorf("provider calls = %d, want 1 (the refresh survives the caller)", got)
	}
}
