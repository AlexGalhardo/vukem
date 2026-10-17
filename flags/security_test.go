package flags_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/flags"
)

// downCache is a cache store whose every call fails.
type downCache struct{}

func (downCache) Get(context.Context, string) ([]byte, bool, error)        { return nil, false, errDown }
func (downCache) Set(context.Context, string, []byte, time.Duration) error { return errDown }
func (downCache) Delete(context.Context, ...string) error                  { return errDown }
func (downCache) Flush(context.Context) error                              { return errDown }
func (downCache) DeleteIf(context.Context, string, []byte) (bool, error)   { return false, errDown }

func (downCache) Add(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, errDown
}

func (downCache) Increment(context.Context, string, int64, time.Duration) (int64, error) {
	return 0, errDown
}

// scopeFailStore serves the global scope and fails every other one.
type scopeFailStore struct{ *memStore }

func (s *scopeFailStore) Load(ctx context.Context, scope string) (map[string]string, error) {
	if scope != flags.Global {
		return nil, errDown
	}

	return s.memStore.Load(ctx, scope)
}

func TestSecurity_A01_LockedFlagsKeepCodeDefault(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	store := newMemStore()
	store.put(flags.Global, "admin_two_factor", "false") // written before the flag was locked
	store.put("user:1", "admin_two_factor", "false")

	f := newFlags(t, flags.Config{Store: store})

	if !f.Enabled(ctx, adminMFA) || !f.Enabled(flags.WithScope(ctx, "user:1"), adminMFA) {
		t.Error("a stored value switched off a locked flag")
	}

	// The registered definition wins: an unlocked copy cannot unlock it.
	if !f.Enabled(ctx, flags.Bool("admin_two_factor", true)) {
		t.Error("an unlocked copy of a locked flag read the stored value")
	}

	if _, err := f.Set(ctx, flags.Global, "admin_two_factor", "false"); !errors.Is(err, flags.ErrLocked) {
		t.Errorf("Set(locked) = %v, want ErrLocked", err)
	}

	if _, err := flags.Put(ctx, f, "user:1", adminMFA, false); !errors.Is(err, flags.ErrLocked) {
		t.Errorf("Put(locked) = %v, want ErrLocked", err)
	}

	if _, err := f.Reset(ctx, flags.Global, "admin_two_factor"); !errors.Is(err, flags.ErrLocked) {
		t.Errorf("Reset(locked) = %v, want ErrLocked", err)
	}
}

func TestSecurity_A02_LockedFlagEnvOverrideIsBootError(t *testing.T) {
	t.Parallel()

	_, err := flags.New(flags.Config{
		Flags: all(),
		Env:   config.MapSource(map[string]string{"FLAG_ADMIN_TWO_FACTOR": "false"}),
	})
	if err == nil || !strings.Contains(err.Error(), "locked") {
		t.Errorf("New() with FLAG_ADMIN_TWO_FACTOR=false = %v, want a boot error", err)
	}
}

func TestSecurity_A10_FlagReadsFailClosed(t *testing.T) {
	t.Parallel()

	env := config.MapSource(map[string]string{"FLAG_UPLOAD_LIMIT_MB": "20"})

	t.Run("store down", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		store := newMemStore()
		store.put(flags.Global, "new_checkout", "true")
		store.put(flags.Global, "upload_limit_mb", "500")
		store.breakDown()

		f := newFlags(t, flags.Config{Store: store, Env: env})

		if f.Enabled(ctx, checkout) || flags.Get(ctx, f, limit) != 20 {
			t.Errorf("reads with the store down = %v %d, want the defaults false 20",
				f.Enabled(ctx, checkout), flags.Get(ctx, f, limit))
		}

		if diff := cmp.Diff(map[string]any{"new_checkout": false}, f.Expose(ctx, checkout)); diff != "" {
			t.Errorf("Expose() with the store down (-want +got):\n%s", diff)
		}
	})

	t.Run("scope load fails", func(t *testing.T) {
		t.Parallel()

		store := &scopeFailStore{memStore: newMemStore()}
		store.put(flags.Global, "new_checkout", "true")

		f := newFlags(t, flags.Config{Store: store})
		if f.Enabled(flags.WithScope(t.Context(), "user:1"), checkout) {
			t.Error("a failed scope load kept the global value instead of the default")
		}
	})

	t.Run("cache down", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		store.put(flags.Global, "new_checkout", "true")

		f := newFlags(t, flags.Config{Store: store, Cache: cache.New(downCache{}, "")})
		if f.Enabled(t.Context(), checkout) {
			t.Error("a cache failure did not fall back to the default")
		}
	})

	t.Run("require denies", func(t *testing.T) {
		t.Parallel()

		store := newMemStore()
		store.put(flags.Global, "new_checkout", "true")
		store.breakDown()

		f := newFlags(t, flags.Config{Store: store})
		h := f.Require(
			checkout,
		)(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }),
		)

		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", nil))

		if rec.Code != http.StatusNotFound {
			t.Errorf("Require() with the store down = %d, want 404", rec.Code)
		}
	})
}

func TestSecurity_A01_FlagsExposeOnlyAllowlist(t *testing.T) {
	t.Parallel()

	f := newFlags(t, flags.Config{})

	got := f.Expose(t.Context(), checkout, limit)
	if diff := cmp.Diff(map[string]any{"new_checkout": false, "upload_limit_mb": 10}, got); diff != "" {
		t.Errorf("Expose() (-want +got):\n%s", diff)
	}

	if got := f.Expose(t.Context()); len(got) != 0 {
		t.Errorf("Expose() without an allowlist = %v, want nothing", got)
	}
}
