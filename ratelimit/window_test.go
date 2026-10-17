package ratelimit

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/database"
)

func TestWindow(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		// The synctest clock starts on a minute boundary.
		w := NewWindow(cache.NewMemory(0), "api", 3, time.Minute)
		ctx := t.Context()

		for i, want := range []Result{
			{Allowed: true, Limit: 3, Remaining: 2, ResetAfter: time.Minute},
			{Allowed: true, Limit: 3, Remaining: 1, ResetAfter: time.Minute},
			{Allowed: true, Limit: 3, Remaining: 0, ResetAfter: time.Minute},
			{Limit: 3, ResetAfter: time.Minute, RetryAfter: time.Minute},
		} {
			got, err := w.Allow(ctx, "a")
			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("request %d (-want +got):\n%s", i+1, diff)
			}
		}

		if res, _ := w.Allow(ctx, "b"); !res.Allowed {
			t.Fatal("key b limited by key a")
		}

		time.Sleep(15 * time.Second)

		if res, _ := w.Allow(ctx, "a"); res.Allowed || res.RetryAfter != 45*time.Second {
			t.Fatalf("Allow(a) mid-window = %+v, want denied with RetryAfter 45s", res)
		}

		time.Sleep(45 * time.Second)

		if res, _ := w.Allow(ctx, "a"); !res.Allowed || res.Remaining != 2 {
			t.Fatalf("Allow(a) in the next window = %+v, want a fresh budget", res)
		}
	})
}

func TestWindowNamesSeparateBudgets(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		store := cache.NewMemory(0)
		login := NewWindow(store, "login", 1, time.Minute)
		mails := NewWindow(store, "mails", 1, time.Minute)

		for _, l := range []*Window{login, mails} {
			if res, _ := l.Allow(t.Context(), "ip:192.0.2.1"); !res.Allowed {
				t.Fatalf("%s: first request denied", l.name)
			}
		}

		if res, _ := login.Allow(t.Context(), "ip:192.0.2.1"); res.Allowed {
			t.Fatal("login: second request allowed")
		}
	})
}

type recorder struct{ keys []string }

func (r *recorder) Increment(_ context.Context, key string, _ int64, _ time.Duration) (int64, error) {
	r.keys = append(r.keys, key)
	return 1, nil
}

func TestWindowBoundsKeyLength(t *testing.T) {
	t.Parallel()

	rec := &recorder{}
	w := NewWindow(rec, "login", 5, time.Minute)

	for _, key := range []string{"ip:192.0.2.1", "login:" + strings.Repeat("a", 400) + "@example.com"} {
		if _, err := w.Allow(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}

	// MySQL keys are VARBINARY(255); keys are hashed to a fixed length.
	if len(rec.keys[0]) != len(rec.keys[1]) || len(rec.keys[1]) > 128 {
		t.Errorf("stored keys %q, want one fixed length ≤ 128", rec.keys)
	}
}

// Keys come from requests (emails, tokens): no key may land in another
// key's bucket, whatever bytes or separators it carries.
func TestSecurity_A07_RateLimitKeysDoNotCollide(t *testing.T) {
	t.Parallel()

	long := "login:" + strings.Repeat("a", 400) + "@example.com"
	sum := sha256.Sum256([]byte(long))

	rec := &recorder{}
	w := NewWindow(rec, "login", 5, time.Minute)
	keys := []string{
		long,
		"sha256:" + hex.EncodeToString(sum[:16]), // the old encoding of long
		"a:b", "a", "a:b:0", "x\x00y", "x\ny",
	}

	for _, key := range keys {
		if _, err := w.Allow(t.Context(), key); err != nil {
			t.Fatal(err)
		}
	}

	seen := map[string]string{}
	for i, stored := range rec.keys {
		if prev, ok := seen[stored]; ok {
			t.Fatalf("keys %q and %q share the bucket %q", prev, keys[i], stored)
		}

		seen[stored] = keys[i]
	}
}

func TestWindowRejectsBadNames(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"", "a:b", strings.Repeat("n", 65), "bad\nname"} {
		w := NewWindow(&recorder{}, name, 5, time.Minute)
		if _, err := w.Allow(t.Context(), "ip:192.0.2.1"); err == nil {
			t.Errorf("NewWindow(%q).Allow() succeeded, want an error (deny)", name)
		}
	}

	if _, err := NewWindow(&recorder{}, "two-factor.challenge_1", 5, time.Minute).Allow(t.Context(), "k"); err != nil {
		t.Errorf("valid name: %v", err)
	}
}

// Several app servers share one database: the budget must hold under
// concurrent requests on separate connections.
func TestWindowDatabaseConcurrent(t *testing.T) {
	t.Parallel()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	store, err := cache.NewDatabase(db, "")
	if err != nil {
		t.Fatal(err)
	}

	if err := store.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	w := NewWindow(store, "api", 10, time.Hour)
	w.now = func() time.Time { return time.Date(2026, 1, 1, 12, 30, 0, 0, time.UTC) }

	var (
		wg      sync.WaitGroup
		allowed atomic.Int32
		failed  atomic.Int32
	)

	for range 25 {
		wg.Go(func() {
			res, err := w.Allow(t.Context(), "ip:192.0.2.1")
			switch {
			case err != nil:
				failed.Add(1)
			case res.Allowed:
				allowed.Add(1)
			}
		})
	}

	wg.Wait()

	if allowed.Load() != 10 || failed.Load() != 0 {
		t.Fatalf("25 concurrent requests, limit 10: %d allowed, %d errors", allowed.Load(), failed.Load())
	}
}

type brokenCounter struct{}

func (brokenCounter) Increment(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errors.New("database is down")
}

func TestSecurity_A06_RateLimitWindowStoreFailsClosed(t *testing.T) {
	t.Parallel()

	w := NewWindow(brokenCounter{}, "login", 5, time.Minute)
	if res, err := w.Allow(t.Context(), "ip:192.0.2.1"); err == nil || res.Allowed {
		t.Fatalf("Allow() with a failing store = %+v, %v, want an error and no allowance", res, err)
	}

	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/login", http.NoBody)
	r.RemoteAddr = "192.0.2.1:1234"

	rec := serve(Middleware(w, nil)(ok), r)
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("Middleware(failing window) = %d, want 503", rec.Code)
	}

	if body := rec.Body.String(); strings.Contains(body, "database is down") {
		t.Fatalf("response leaks the store error: %s", body)
	}
}
