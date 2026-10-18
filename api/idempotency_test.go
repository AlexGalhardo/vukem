package api_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/web"
)

func idemHandler(
	store cache.Store,
	calls *atomic.Int32,
	o api.IdempotencyOptions,
	entered ...chan struct{},
) http.Handler {
	return api.Idempotency(store, o)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := calls.Add(1)

		switch r.URL.Path {
		case "/fail":
			w.WriteHeader(http.StatusInternalServerError)
		case "/slow":
			close(entered[0])
			<-r.Context().Done()
		default:
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "secret"})
			w.Header().Set("Location", "/orders/1")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"order":` + strconv.Itoa(int(n)) + `}`))
		}
	}))
}

func send(t *testing.T, h http.Handler, method, path, key, authorization, body string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), method, path, strings.NewReader(body))

	if key != "" {
		r.Header.Set(api.IdempotencyHeader, key)
	}

	r.Header.Set("Authorization", authorization)
	h.ServeHTTP(rec, r)

	return rec
}

func TestIdempotencyReplaysTheStoredResponse(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	h := idemHandler(cache.NewMemory(100), &calls, api.IdempotencyOptions{})

	first := send(t, h, "POST", "/orders", "k1", "Bearer a", `{"qty":1}`)
	again := send(t, h, "POST", "/orders", "k1", "Bearer a", `{"qty":1}`)

	if calls.Load() != 1 || first.Code != 201 || again.Code != 201 || again.Body.String() != `{"order":1}` ||
		again.Header().Get("Location") != "/orders/1" || again.Header().Get("Idempotent-Replayed") != "true" {
		t.Fatalf("calls=%d first=%d again=%d %q %v", calls.Load(), first.Code, again.Code, again.Body, again.Header())
	}

	if again.Header().Get("Set-Cookie") != "" {
		t.Error("Set-Cookie replayed")
	}

	if rec := send(t, h, "POST", "/orders", "k1", "Bearer a", `{"qty":2}`); rec.Code != 422 {
		t.Errorf("reused key with another body = %d, want 422", rec.Code)
	}

	send(t, h, "POST", "/orders", "", "Bearer a", `{}`)
	send(t, h, "GET", "/orders", "k1", "Bearer a", ``)

	if calls.Load() != 3 {
		t.Errorf("no key / GET should pass through, calls = %d", calls.Load())
	}

	if rec := send(t, h, "POST", "/orders", "bad key", "Bearer a", `{}`); rec.Code != 400 {
		t.Errorf("invalid key = %d", rec.Code)
	}
}

func TestSecurity_A01_IdempotencyScopedToCaller(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	h := idemHandler(cache.NewMemory(100), &calls, api.IdempotencyOptions{})

	send(t, h, "POST", "/orders", "same", "Bearer alice", `{}`)

	rec := send(t, h, "POST", "/orders", "same", "Bearer bob", `{}`)
	if calls.Load() != 2 || rec.Header().Get("Idempotent-Replayed") != "" {
		t.Fatalf("bob got alice's response (calls=%d)", calls.Load())
	}
}

func TestSecurity_A01_IdempotencyRefusesAnonymousScope(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	h := idemHandler(cache.NewMemory(100), &calls, api.IdempotencyOptions{})

	// Two cookie-session users without Authorization would share the "" scope.
	if rec := send(t, h, "POST", "/orders", "k", "", `{}`); rec.Code != 400 || calls.Load() != 0 {
		t.Fatalf("empty scope = %d calls=%d, want 400 and no call", rec.Code, calls.Load())
	}

	if rec := send(t, h, "POST", "/orders", "", "", `{}`); rec.Code != 201 {
		t.Errorf("no key and empty scope = %d, want pass-through 201", rec.Code)
	}

	shared := idemHandler(cache.NewMemory(100), &calls, api.IdempotencyOptions{
		Scope: func(*http.Request) string { return "public" },
	})
	if rec := send(t, shared, "POST", "/orders", "k", "", `{}`); rec.Code != 201 {
		t.Errorf("explicit scope = %d, want 201", rec.Code)
	}
}

func TestIdempotencyInFlightErrorsAndRequired(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	store := cache.NewMemory(100)
	entered := make(chan struct{})
	h := idemHandler(store, &calls, api.IdempotencyOptions{Required: true, LockTTL: time.Minute}, entered)

	// Server errors are not stored: the retry runs again.
	send(t, h, "POST", "/fail", "f", "Bearer t", ``)

	if rec := send(t, h, "POST", "/fail", "f", "Bearer t", ``); rec.Code != 500 || calls.Load() != 2 {
		t.Fatalf("retry after 500 = %d calls=%d", rec.Code, calls.Load())
	}

	// A duplicate while the first is still running gets 409.
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		defer close(done)

		r := httptest.NewRequestWithContext(ctx, "POST", "/slow", http.NoBody)
		r.Header.Set(api.IdempotencyHeader, "s")
		r.Header.Set("Authorization", "Bearer t")
		h.ServeHTTP(httptest.NewRecorder(), r)
	}()

	<-entered

	rec := send(t, h, "POST", "/slow", "s", "Bearer t", ``)

	cancel()
	<-done

	if rec.Code != 409 {
		t.Fatalf("in-flight duplicate = %d, want 409", rec.Code)
	}

	if rec := send(t, h, "PATCH", "/orders", "", "", `{}`); rec.Code != 400 {
		t.Fatalf("missing required key = %d", rec.Code)
	}
}

type failingStore struct{ cache.Store }

func (failingStore) Add(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, errors.New("down")
}

func TestSecurity_A04_IdempotencyFailsClosed(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	h := idemHandler(failingStore{}, &calls, api.IdempotencyOptions{})

	if rec := send(t, h, "POST", "/orders", "k", "Bearer t", `{}`); rec.Code != 503 || calls.Load() != 0 {
		t.Fatalf("store down = %d calls=%d, want 503 and no call", rec.Code, calls.Load())
	}
}

type principal string

func (p principal) AuthID() string       { return string(p) }
func (p principal) AuthPassword() string { return "" }

// as authenticates the request as user id with the "api" guard, as
// auth.Required would before Idempotency.
func as(id string, next http.Handler) http.Handler { return asGuard("api", id, next) }

func asGuard(guard, id string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := web.NewCtx(w, r, nil)
		auth.SetUser(c, guard, principal(id))
		next.ServeHTTP(w, c.Request())
	})
}

func TestSecurity_A01_IdempotencyScopedToPrincipal(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	h := idemHandler(cache.NewMemory(100), &calls, api.IdempotencyOptions{})

	steps := []struct {
		name      string
		handler   http.Handler
		auth      string
		wantCalls int32
	}{
		{"user 7, first token", as("7", h), "Bearer t1", 1},
		{"user 7, rotated token replays", as("7", h), "Bearer t2", 1},
		{"user 8 with user 7's old token", as("8", h), "Bearer t1", 2},
		// No principal (e.g. the token was revoked): the header scope is
		// not the principal scope the response was stored under.
		{"unauthenticated, same header", h, "Bearer t1", 3},
	}

	for _, st := range steps {
		send(t, st.handler, "POST", "/orders", "k", st.auth, `{}`)

		if got := calls.Load(); got != st.wantCalls {
			t.Fatalf("%s: handler calls = %d, want %d", st.name, got, st.wantCalls)
		}
	}
}

func TestSecurity_A01_IdempotencyScopeHasNoSeparatorCollisions(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	h := idemHandler(cache.NewMemory(100), &calls, api.IdempotencyOptions{})

	send(t, asGuard("a:b", "c", h), "POST", "/orders", "k", "", `{}`)
	send(t, asGuard("a", "b:c", h), "POST", "/orders", "k", "", `{}`)

	if calls.Load() != 2 {
		t.Fatalf(
			"guard %q user %q and guard %q user %q share a scope (calls = %d)",
			"a:b",
			"c",
			"a",
			"b:c",
			calls.Load(),
		)
	}
}

func TestSecurity_A01_IdempotencyTokenPrefixDoesNotForkScope(t *testing.T) {
	t.Parallel()

	const (
		id     = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
		secret = "234567ABCDEFGHIJKLMNOPQRST"
	)

	var calls atomic.Int32

	h := idemHandler(cache.NewMemory(100), &calls, api.IdempotencyOptions{})

	for _, header := range []string{
		"Bearer vk_" + id + "." + secret,
		"Bearer " + id + "." + secret,
		"Bearer old_" + id + "." + secret,
	} {
		send(t, h, "POST", "/orders", "k", header, `{}`)
	}

	if calls.Load() != 1 {
		t.Fatalf("one token under different prefixes ran %d times, want 1", calls.Load())
	}

	// The secret still separates callers.
	send(t, h, "POST", "/orders", "k", "Bearer vk_"+id+"."+strings.Repeat("A", 26), `{}`)

	if calls.Load() != 2 {
		t.Fatalf("same token id with another secret replayed (calls = %d)", calls.Load())
	}
}

func TestSecurity_A01_IdempotencyReplayKeepsPerRequestHeaders(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("X-Debug-Token", "first")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	})

	// The outer middleware stands for request id and rate limit headers set
	// per request before Idempotency runs.
	var n atomic.Int32

	perRequest := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := strconv.Itoa(int(n.Add(1)))
			w.Header().Set("X-Request-ID", "req-"+id)
			w.Header().Set("RateLimit-Remaining", id)
			next.ServeHTTP(w, r)
		})
	}

	h := perRequest(api.Idempotency(cache.NewMemory(100), api.IdempotencyOptions{})(inner))

	send(t, h, "POST", "/orders", "k1", "Bearer a", `{}`)
	again := send(t, h, "POST", "/orders", "k1", "Bearer a", `{}`)

	got := map[string]string{}
	for _, k := range []string{
		"X-Request-ID", "RateLimit-Remaining", "Content-Type", "ETag", "X-Debug-Token", "Idempotent-Replayed",
	} {
		got[k] = again.Header().Get(k)
	}

	want := map[string]string{
		"X-Request-ID": "req-2", "RateLimit-Remaining": "2", "Content-Type": "application/json",
		"ETag": `"v1"`, "X-Debug-Token": "", "Idempotent-Replayed": "true",
	}
	if diff := cmp.Diff(want, got); diff != "" || calls.Load() != 1 {
		t.Fatalf("replayed headers (-want +got):\n%s (calls=%d)", diff, calls.Load())
	}

	custom := perRequest(api.Idempotency(cache.NewMemory(100),
		api.IdempotencyOptions{ReplayHeaders: []string{"x-debug-token"}})(inner))

	send(t, custom, "POST", "/orders", "k1", "Bearer a", `{}`)

	if again := send(
		t,
		custom,
		"POST",
		"/orders",
		"k1",
		"Bearer a",
		`{}`,
	); again.Header().
		Get("X-Debug-Token") !=
		"first" ||
		again.Header().Get("ETag") != "" {
		t.Fatalf("ReplayHeaders = %v, want only X-Debug-Token replayed", again.Header())
	}
}

func TestSecurity_A01_IdempotencyNeverReplaysCredentialHeaders(t *testing.T) {
	t.Parallel()

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for _, h := range []string{"Set-Cookie", "Authorization", "Proxy-Authenticate", "WWW-Authenticate", "ETag"} {
			w.Header().Set(h, "first")
		}

		w.WriteHeader(http.StatusCreated)
	})

	h := api.Idempotency(cache.NewMemory(100), api.IdempotencyOptions{ReplayHeaders: []string{
		"set-cookie", "authorization", "proxy-authenticate", "www-authenticate", "etag",
	}})(inner)

	send(t, h, "POST", "/orders", "k1", "Bearer a", `{}`)
	again := send(t, h, "POST", "/orders", "k1", "Bearer a", `{}`)

	for _, name := range []string{"Set-Cookie", "Authorization", "Proxy-Authenticate", "WWW-Authenticate"} {
		if v := again.Header().Get(name); v != "" {
			t.Errorf("replay sent %s = %q, want it never replayed", name, v)
		}
	}

	if again.Header().Get("ETag") != "first" {
		t.Errorf("replay ETag = %q, want the listed header replayed", again.Header().Get("ETag"))
	}
}
