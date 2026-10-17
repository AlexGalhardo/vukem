package social

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// downStore is a cache store whose every call fails.
type downStore struct{ cache.Store }

func (downStore) Add(context.Context, string, []byte, time.Duration) (bool, error) {
	return false, errors.New("store down")
}

// replay sends the callback captured in q/ck to s again.
func replay(t *testing.T, s *Social, q url.Values, ck *http.Cookie) error {
	t.Helper()

	cb := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/cb?"+q.Encode(), http.NoBody)
	cb.AddCookie(ck)

	_, err := s.User(web.NewCtx(httptest.NewRecorder(), cb, nil), "github")

	return err
}

func TestSecurity_A07_SocialReplayAcrossInstances(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.profile = map[string]any{"id": 1.0, "login": "ana"}

	// Two replicas (or one restarted process) sharing the app cache store.
	shared := cache.NewMemory(100)
	a := New(testKey, GitHub(cfg(), WithEndpoints(oauthEndpoints(f))))
	a.States = shared
	b := New(testKey, GitHub(cfg(), WithEndpoints(oauthEndpoints(f))))
	b.States = shared

	var (
		q  url.Values
		ck http.Cookie
	)

	if _, err := flow(t, a, f, "github", func(qq url.Values, c *http.Cookie) { q, ck = qq, *c }); err != nil {
		t.Fatal(err)
	}

	if err := replay(t, b, q, &ck); !errors.Is(err, ErrState) {
		t.Fatalf("replay on another instance = %v, want ErrState", err)
	}
}

func TestSecurity_A04_SocialStateStoreFailsClosed(t *testing.T) {
	t.Parallel()

	f := newFakeServer(t)
	f.profile = map[string]any{"id": 1.0, "login": "ana"}
	s := New(testKey, GitHub(cfg(), WithEndpoints(oauthEndpoints(f))))
	s.States = downStore{}

	_, err := flow(t, s, f, "github", nil)
	if err == nil || errs.Status(err) != http.StatusServiceUnavailable {
		t.Fatalf("callback with store down = %v (status %d), want 503", err, errs.Status(err))
	}
}
