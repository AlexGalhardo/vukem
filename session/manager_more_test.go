package session

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/web"
)

// faultyStore wraps MemoryStore; set errors make the matching call fail.
type faultyStore struct {
	*MemoryStore

	mu                       sync.Mutex
	loadErr, saveErr, delErr error
	raw                      map[string][]byte // returned by Load as-is
}

func (f *faultyStore) Load(ctx context.Context, id string) ([]byte, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.loadErr != nil {
		return nil, false, f.loadErr
	}

	if b, ok := f.raw[id]; ok {
		return b, true, nil
	}

	return f.MemoryStore.Load(ctx, id)
}

func (f *faultyStore) Save(ctx context.Context, id string, data []byte, expiry time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.saveErr != nil {
		return f.saveErr
	}

	return f.MemoryStore.Save(ctx, id, data, expiry)
}

func (f *faultyStore) Delete(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.delErr != nil {
		return f.delErr
	}

	return f.MemoryStore.Delete(ctx, id)
}

func (f *faultyStore) set(fn func(f *faultyStore)) {
	f.mu.Lock()
	defer f.mu.Unlock()

	fn(f)
}

func newFaultyManager(t *testing.T) (*Manager, *faultyStore, *bytes.Buffer) {
	t.Helper()

	var logs bytes.Buffer

	st := &faultyStore{MemoryStore: NewMemoryStore(), raw: map[string][]byte{}}
	m, clk := newManager(t, Config{Store: st, Log: slog.New(slog.NewTextHandler(&logs, nil))})
	st.now = clk.Now // expiries come from the manager clock

	return m, st, &logs
}

const validID = "ABCDEFGHIJKLMNOPQRSTUVWXYZ"

func TestStoreFailuresAreLoggedAndFailClosed(t *testing.T) {
	t.Parallel()

	t.Run("load", func(t *testing.T) {
		t.Parallel()

		m, st, logs := newFaultyManager(t)
		c := newClient(t, appHandler(m, logs))
		c.do("GET", "/put?v=ana", nil)

		st.set(func(f *faultyStore) { f.loadErr = errors.New("db down") })

		if got := c.do("GET", "/get", nil).Body.String(); got != "" {
			t.Errorf("GET /get with the store down = %q, want an empty session", got)
		}

		if !strings.Contains(logs.String(), "session load failed") {
			t.Errorf("logs = %q, want session load failed", logs.String())
		}
	})

	t.Run("save", func(t *testing.T) {
		t.Parallel()

		m, st, logs := newFaultyManager(t)
		st.set(func(f *faultyStore) { f.saveErr = errors.New("disk full") })

		rec := newClient(t, appHandler(m, logs)).do("GET", "/put?v=ana", nil)
		if cks := rec.Result().Cookies(); len(cks) != 0 {
			t.Errorf("cookies after a failed save = %v, want none (no id for unsaved data)", cks)
		}

		if !strings.Contains(logs.String(), "session save failed") {
			t.Errorf("logs = %q, want session save failed", logs.String())
		}
	})

	t.Run("delete", func(t *testing.T) {
		t.Parallel()

		m, st, logs := newFaultyManager(t)
		c := newClient(t, appHandler(m, logs))
		c.do("GET", "/put?v=ana", nil)

		st.set(func(f *faultyStore) { f.delErr = errors.New("db down") })

		// Regenerate must still issue a new id when the old record cannot
		// be deleted.
		before := c.do("GET", "/id", nil).Body.String()
		after := c.do("GET", "/login", nil).Body.String()

		if before == after || !idRe.MatchString(after) {
			t.Errorf("login id = %q (before %q), want a new valid id", after, before)
		}

		if !strings.Contains(logs.String(), "session delete failed") {
			t.Errorf("logs = %q, want session delete failed", logs.String())
		}
	})
}

func TestInvalidStoredSessionsStartFresh(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		cookie    string
		raw       string
		wantEvent string
	}{
		{name: "malformed id", cookie: "../../etc/passwd", wantEvent: "malformed session id"},
		{name: "lowercase id", cookie: strings.ToLower(validID), wantEvent: "malformed session id"},
		{name: "undecodable data", cookie: validID, raw: "{not json", wantEvent: "undecodable data"},
		{name: "null data", cookie: validID, raw: "null"},
		{name: "fractional timestamps", cookie: validID, raw: `{"_created":1.5,"_last":1.5,"user":"ana"}`},
		{name: "string timestamps", cookie: validID, raw: `{"_created":"x","_last":"x","user":"ana"}`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			m, st, logs := newFaultyManager(t)
			if tt.raw != "" {
				st.set(func(f *faultyStore) { f.raw[validID] = []byte(tt.raw) })
			}

			c := newClient(t, appHandler(m, logs))
			c.cookies[m.CookieName()] = &http.Cookie{Name: m.CookieName(), Value: tt.cookie}

			if got := c.do("GET", "/get", nil).Body.String(); got != "" {
				t.Errorf("user = %q, want an empty session", got)
			}

			if tt.wantEvent != "" && !strings.Contains(logs.String(), tt.wantEvent) {
				t.Errorf("logs = %q, want security event %q", logs.String(), tt.wantEvent)
			}

			// The next write gets a fresh id, never the presented one.
			if id := c.do("GET", "/login", nil).Body.String(); id == tt.cookie || !idRe.MatchString(id) {
				t.Errorf("id after login = %q, want a fresh id", id)
			}
		})
	}
}

func TestMalformedCookieSessionIsRejected(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"!!!not-base64!!!", "AAAA"} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer

			m, _ := newManager(t, Config{})
			c := newClient(t, appHandler(m, &logs))
			c.cookies[m.CookieName()] = &http.Cookie{Name: m.CookieName(), Value: value}

			if got := c.do("GET", "/get", nil).Body.String(); got != "" {
				t.Errorf("user = %q, want an empty session", got)
			}

			if !strings.Contains(logs.String(), "cookie authentication failed") {
				t.Errorf("logs = %q, want a security event", logs.String())
			}
		})
	}
}

func TestCookiesStartsASession(t *testing.T) {
	t.Parallel()

	for name, mem := range map[string]*MemoryStore{"cookie": nil, "memory": NewMemoryStore()} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var store Store
			if mem != nil {
				store = mem
			}

			m, clk := newManager(t, Config{Store: store})
			if mem != nil {
				mem.now = clk.Now
			}

			cks := m.Cookies(t.Context(), map[string]any{"user": "ana"})
			if len(cks) != 1 || cks[0].Name != m.CookieName() || !cks[0].HttpOnly {
				t.Fatalf("Cookies() = %+v, want one HttpOnly session cookie", cks)
			}

			if got := m.Peek(t.Context(), cks); got["user"] != "ana" {
				t.Errorf("Peek(Cookies()) = %v, want user ana", got)
			}
		})
	}
}

func TestUnencodableDataIsNotSaved(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer

	m, _ := newManager(t, Config{Log: slog.New(slog.NewTextHandler(&logs, nil))})
	h := m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		web.SessionFrom(r.Context()).Put("bad", make(chan int))
		w.WriteHeader(http.StatusNoContent)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

	if cks := rec.Result().Cookies(); len(cks) != 0 {
		t.Errorf("cookies = %v, want none", cks)
	}

	if !strings.Contains(logs.String(), "session encode failed") {
		t.Errorf("logs = %q, want session encode failed", logs.String())
	}
}

func TestInvalidateThenPutKeepsNewData(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, Config{})
	h := m.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		s := web.SessionFrom(r.Context())
		s.Invalidate()
		s.Put("notice", "signed out")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

	cks := rec.Result().Cookies()
	if len(cks) != 1 || cks[0].MaxAge <= 0 {
		t.Fatalf("cookies = %+v, want a live session cookie, not a deletion", cks)
	}

	if got := m.Peek(t.Context(), cks); got["notice"] != "signed out" {
		t.Errorf("Peek() = %v, want the value put after Invalidate", got)
	}
}

func TestIntegersSurviveTheRoundTrip(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, Config{})
	cks := m.Cookies(t.Context(), map[string]any{"user_id": int64(9007199254740993)})

	var (
		got int64
		ok  bool
	)

	h := m.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		s, _ := web.SessionFrom(r.Context()).(*Session)
		got, ok = s.GetInt64("user_id")
	}))

	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	for _, c := range cks {
		r.AddCookie(c)
	}

	h.ServeHTTP(httptest.NewRecorder(), r)

	// 2^53+1 is not representable as float64: json.Number keeps it exact.
	if !ok || got != 9007199254740993 {
		t.Errorf("GetInt64(user_id) = %d, %v, want 9007199254740993, true", got, ok)
	}
}

func TestFlashTwiceKeepsOneEntry(t *testing.T) {
	t.Parallel()

	s := newSession("id", nil)
	s.Flash("status", "a")
	s.Flash("status", "b")

	if got := stringList(s.data[keyFlashNew]); len(got) != 1 {
		t.Errorf("flash keys = %q, want one entry", got)
	}

	s.ageFlash()

	if s.Get("status") != "b" {
		t.Errorf("Get(status) = %v, want the last flash", s.Get("status"))
	}
}

func TestValidationHookWithoutSession(t *testing.T) {
	t.Parallel()

	r := httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/", http.NoBody)
	c := web.NewCtx(httptest.NewRecorder(), r, &web.Services{})

	if validationHook(c, errs.NewValidation().Add("title", "required")) {
		t.Error("validationHook() without a session = true, want false (render the error instead)")
	}
}

func TestCommitWriterUnwraps(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, Config{})
	rec := httptest.NewRecorder()

	var inner http.ResponseWriter

	h := m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if u, ok := w.(interface{ Unwrap() http.ResponseWriter }); ok {
			inner = u.Unwrap()
		}
	}))
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody))

	if inner != rec {
		t.Errorf("Unwrap() = %T, want the original writer", inner)
	}
}

func TestMemoryStoreSaveEvictsExpired(t *testing.T) {
	t.Parallel()

	clk := &clock{t: time.Unix(1000, 0)}
	s := NewMemoryStore()
	s.now = clk.Now

	_ = s.Save(t.Context(), "old", []byte("x"), clk.Now().Add(time.Second))
	clk.Advance(time.Minute)
	_ = s.Save(t.Context(), "new", []byte("y"), clk.Now().Add(time.Hour))

	if s.Len() != 1 {
		t.Errorf("Len() = %d, want 1 (expired entry evicted on Save)", s.Len())
	}
}

func TestSQLStoreErrorsOnClosedDB(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(testutil.TempDir(t), "c.db"))
	if err != nil {
		t.Fatal(err)
	}

	s, err := NewSQLStore(db)
	if err != nil {
		t.Fatal(err)
	}

	_ = db.Close()
	ctx := t.Context()

	for name, call := range map[string]func() error{
		"save":   func() error { return s.Save(ctx, "a", []byte("{}"), time.Now()) },
		"delete": func() error { return s.Delete(ctx, "a") },
		"prune":  func() error { _, err := s.Prune(ctx); return err },
	} {
		if err := call(); err == nil || !strings.Contains(err.Error(), "session: "+name) {
			t.Errorf("%s on closed DB = %v, want session: %s error", name, err, name)
		}
	}
}
