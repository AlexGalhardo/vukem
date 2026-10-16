package session

import (
	"bytes"
	"context"
	"database/sql"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	_ "modernc.org/sqlite" // SQLite driver for SQLStore tests

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

var testKey = bytes.Repeat([]byte{7}, 32)

// client replays cookies between requests like a browser.
type client struct {
	t       *testing.T
	h       http.Handler
	cookies map[string]*http.Cookie
}

func newClient(t *testing.T, h http.Handler) *client {
	t.Helper()

	return &client{t: t, h: h, cookies: map[string]*http.Cookie{}}
}

func (c *client) do(method, target string, body url.Values) *httptest.ResponseRecorder {
	c.t.Helper()

	var r *http.Request
	if body != nil {
		r = httptest.NewRequestWithContext(c.t.Context(), method, target, strings.NewReader(body.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	} else {
		r = httptest.NewRequestWithContext(c.t.Context(), method, target, http.NoBody)
	}

	for _, ck := range c.cookies {
		r.AddCookie(ck)
	}

	rec := httptest.NewRecorder()
	c.h.ServeHTTP(rec, r)

	for _, ck := range rec.Result().Cookies() {
		if ck.MaxAge < 0 {
			delete(c.cookies, ck.Name)
		} else {
			c.cookies[ck.Name] = ck
		}
	}

	return rec
}

func appHandler(m *Manager, logs *bytes.Buffer) http.Handler {
	s := &web.Services{Log: slog.New(slog.NewTextHandler(logs, nil))}

	mux := http.NewServeMux()
	handle := func(pattern string, h web.HandlerFunc) {
		mux.Handle(pattern, m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			web.NewCtx(w, r, s).Serve(h)
		})))
	}

	handle("GET /put", func(c *web.Ctx) error {
		c.Session().Put("user", c.Query("v"))
		return c.String(200, "ok")
	})
	handle("GET /get", func(c *web.Ctx) error {
		v, _ := c.Session().Get("user").(string)
		return c.String(200, v)
	})
	handle("GET /flash", func(c *web.Ctx) error {
		_ = c.Flash("status", "Saved!")
		return c.String(200, "flashed")
	})
	handle("GET /read-flash", func(c *web.Ctx) error {
		v, _ := c.FlashValue("status").(string)
		return c.String(200, v)
	})
	handle("GET /static", func(c *web.Ctx) error { return c.String(200, "no session use") })
	handle("GET /login", func(c *web.Ctx) error {
		c.Session().Regenerate()
		c.Session().Put("user", "admin")
		return c.String(200, c.Session().ID())
	})
	handle("GET /id", func(c *web.Ctx) error { return c.String(200, c.Session().ID()) })
	handle("GET /logout", func(c *web.Ctx) error {
		c.Session().Invalidate()
		return c.String(200, "bye")
	})
	handle("POST /posts", func(c *web.Ctx) error {
		if _, err := c.Input(); err != nil {
			return err
		}
		return errs.NewValidation().Add("title", "The title field is required.")
	})
	handle("GET /form", func(c *web.Ctx) error {
		return c.String(200, c.Old("title")+"|"+c.Old("password")+"|"+strings.Join(c.Errors()["title"], ","))
	})

	// The router puts services in the context before any middleware runs.
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mux.ServeHTTP(w, r.WithContext(web.WithServices(r.Context(), s)))
	})
}

func newManager(t *testing.T, cfg Config) (*Manager, *clock) {
	t.Helper()

	clk := &clock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
	cfg.Now = clk.Now
	if cfg.Store == nil && cfg.CookieKey == nil {
		cfg.CookieKey = testKey
	}

	m, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	return m, clk
}

func storesUnderTest(t *testing.T) map[string]Store {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	sq, err := NewSQLStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err := sq.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	return map[string]Store{"cookie": nil, "memory": NewMemoryStore(), "sql": sq}
}

func TestSessionLifecycleAllStores(t *testing.T) {
	t.Parallel()

	for name, store := range storesUnderTest(t) {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			m, clk := newManager(t, Config{Store: store})
			if sq, ok := store.(*SQLStore); ok {
				sq.now = clk.Now
			}
			if ms, ok := store.(*MemoryStore); ok {
				ms.now = clk.Now
			}

			c := newClient(t, appHandler(m, &bytes.Buffer{}))

			c.do("GET", "/put?v=alice", nil)
			if got := c.do("GET", "/get", nil).Body.String(); got != "alice" {
				t.Fatalf("get = %q", got)
			}

			ck := c.cookies[m.CookieName()]
			if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" {
				t.Fatalf("cookie flags = %+v", ck)
			}

			// Flash survives exactly one request.
			c.do("GET", "/flash", nil)
			if got := c.do("GET", "/read-flash", nil).Body.String(); got != "Saved!" {
				t.Fatalf("flash first read = %q", got)
			}
			if got := c.do("GET", "/read-flash", nil).Body.String(); got != "" {
				t.Fatalf("flash second read = %q", got)
			}

			// Idle timeout.
			clk.Advance(3 * time.Hour)
			if got := c.do("GET", "/get", nil).Body.String(); got != "" {
				t.Fatalf("after idle timeout = %q", got)
			}

			// Logout clears.
			c.do("GET", "/put?v=bob", nil)
			c.do("GET", "/logout", nil)
			if got := c.do("GET", "/get", nil).Body.String(); got != "" {
				t.Fatalf("after logout = %q", got)
			}
		})
	}
}

func TestSecurity_A07_SessionRegeneratedOnLogin(t *testing.T) {
	t.Parallel()

	store := NewMemoryStore()
	m, clk := newManager(t, Config{Store: store})
	store.now = clk.Now
	c := newClient(t, appHandler(m, &bytes.Buffer{}))

	c.do("GET", "/put?v=guest", nil)
	before := c.cookies[m.CookieName()].Value

	after := c.do("GET", "/login", nil).Body.String()
	if after == before || after == "" || c.cookies[m.CookieName()].Value != after {
		t.Fatalf("id not regenerated: before=%s after=%s", before, after)
	}

	if _, found, _ := store.Load(t.Context(), before); found {
		t.Fatal("old session id still valid after regeneration")
	}
	if got := c.do("GET", "/get", nil).Body.String(); got != "admin" {
		t.Fatalf("data lost on regenerate: %q", got)
	}

	// A fixated (attacker-chosen) id is not honoured.
	fix := newClient(t, appHandler(m, &bytes.Buffer{}))
	fix.cookies[m.CookieName()] = &http.Cookie{Name: m.CookieName(), Value: "AAAAAAAAAAAAAAAAAAAAAAAAAA"}
	fix.do("GET", "/put?v=x", nil)
	if fix.cookies[m.CookieName()].Value == "AAAAAAAAAAAAAAAAAAAAAAAAAA" {
		t.Fatal("unknown session id was adopted")
	}
}

func TestSecurity_A08_TamperedCookieRejected(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	m, _ := newManager(t, Config{})
	c := newClient(t, appHandler(m, &logs))

	c.do("GET", "/put?v=alice", nil)
	ck := c.cookies[m.CookieName()]

	raw := []byte(ck.Value)
	raw[len(raw)/2] ^= 1
	ck.Value = string(raw)

	if got := c.do("GET", "/get", nil).Body.String(); got != "" {
		t.Fatalf("tampered cookie accepted: %q", got)
	}
	if !strings.Contains(logs.String(), "event=session.invalid") {
		t.Fatalf("no security event logged: %s", logs.String())
	}

	// A cookie sealed for another name is rejected (name is authenticated).
	other, _ := newSealer(testKey)
	ck.Value = other.seal("other_cookie", []byte(`{"user":"mallory"}`))
	if got := c.do("GET", "/get", nil).Body.String(); got != "" {
		t.Fatalf("moved cookie accepted: %q", got)
	}
}

func TestAbsoluteLifetimeAndTouch(t *testing.T) {
	t.Parallel()

	m, clk := newManager(t, Config{Lifetime: 2 * time.Hour, IdleTimeout: time.Hour})
	c := newClient(t, appHandler(m, &bytes.Buffer{}))

	c.do("GET", "/put?v=alice", nil)

	// Activity keeps the idle timer alive but not past the absolute lifetime.
	for range 4 {
		clk.Advance(40 * time.Minute)
		c.do("GET", "/get", nil)
	}

	if got := c.do("GET", "/get", nil).Body.String(); got != "" {
		t.Fatalf("session outlived absolute lifetime: %q", got)
	}
}

func TestUntouchedSessionWritesNothing(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, Config{})
	rec := newClient(t, appHandler(m, &bytes.Buffer{})).do("GET", "/static", nil)

	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("cookie written for a request that never used the session")
	}
}

func TestValidationHookFlashesErrorsAndOldInput(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, Config{})
	c := newClient(t, appHandler(m, &bytes.Buffer{}))

	r := c.do("POST", "/posts", url.Values{"title": {"draft"}, "password": {"secret"}, "_token": {"t"}})
	if r.Code != http.StatusSeeOther {
		t.Fatalf("status = %d", r.Code)
	}

	got := c.do("GET", "/form", nil).Body.String()
	if got != "draft||The title field is required." {
		t.Fatalf("form = %q (password must not be flashed)", got)
	}

	if got := c.do("GET", "/form", nil).Body.String(); got != "||" {
		t.Fatalf("errors persisted beyond one request: %q", got)
	}
}

func TestSecurity_A02_ConfigChecks(t *testing.T) {
	t.Parallel()

	if _, err := New(Config{Production: true, CookieKey: testKey}); err == nil {
		t.Error("insecure cookie accepted in production")
	}
	if _, err := New(Config{SameSite: http.SameSiteNoneMode, CookieKey: testKey}); err == nil {
		t.Error("SameSite=None without Secure accepted")
	}
	if _, err := New(Config{}); err == nil {
		t.Error("cookie sessions without key accepted")
	}

	m, err := New(Config{Secure: true, Production: true, CookieKey: testKey})
	if err != nil || m.CookieName() != "__Host-vukem_session" {
		t.Fatalf("secure name = %q %v", m.CookieName(), err)
	}

	m, _ = New(Config{Secure: true, Domain: "example.com", CookieKey: testKey})
	if m.CookieName() != "vukem_session" {
		t.Fatal("__Host- prefix used with Domain")
	}
}

func TestCookieTooLarge(t *testing.T) {
	t.Parallel()

	var logs bytes.Buffer
	m, _ := newManager(t, Config{Log: slog.New(slog.NewTextHandler(&logs, nil))})

	h := m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		web.SessionFrom(r.Context()).Put("big", strings.Repeat("x", 5000))
		_, _ = w.Write([]byte("ok"))
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), "GET", "/", http.NoBody))

	if len(rec.Result().Cookies()) != 0 || !strings.Contains(logs.String(), "4 KB") {
		t.Fatalf("oversized cookie handling: cookies=%v logs=%s", rec.Result().Cookies(), logs.String())
	}
}

func TestSessionAPI(t *testing.T) {
	t.Parallel()

	s := newSession("id1", nil)
	s.Put("n", int64(5))
	s.Put("s", "x")

	if n, ok := s.GetInt64("n"); !ok || n != 5 || s.GetString("s") != "x" || !s.Has("s") {
		t.Fatal("getters")
	}
	if s.Pull("s") != "x" || s.Has("s") {
		t.Fatal("Pull")
	}
	s.Forget("n")
	if s.Has("n") {
		t.Fatal("Forget")
	}

	s.Flash("f", 1)
	s.ageFlash()
	s.Reflash()
	s.ageFlash()
	if s.Get("f") != 1 {
		t.Fatal("Reflash lost value")
	}
	s.ageFlash()
	if s.Get("f") != nil {
		t.Fatal("flash not aged")
	}

	s.Put("a", 1)
	s.Put("_internal", 2)
	if all := s.All(); all["a"] != 1 || all["_internal"] != nil {
		t.Fatalf("All() = %v", all)
	}

	for _, v := range []any{int(3), float64(3)} {
		s.Put("x", v)
		if n, ok := s.GetInt64("x"); !ok || n != 3 {
			t.Fatalf("GetInt64(%T)", v)
		}
	}
	s.Put("x", "nope")
	if _, ok := s.GetInt64("x"); ok {
		t.Fatal("GetInt64(string)")
	}
}

func TestSQLStoreTableValidationAndPrune(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:"+filepath.Join(t.TempDir(), "p.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	if _, err := NewSQLStore(db, WithTable("x; DROP TABLE users")); err == nil {
		t.Fatal("unsafe table name accepted")
	}

	s, _ := NewSQLStore(db, WithTable("web_sessions"))
	if err := s.CreateTable(t.Context()); err != nil {
		t.Fatal(err)
	}

	now := time.Now()
	_ = s.Save(t.Context(), "a", []byte(`{}`), now.Add(-time.Minute))
	_ = s.Save(t.Context(), "b", []byte(`{}`), now.Add(time.Hour))
	_ = s.Save(t.Context(), "b", []byte(`{"v":1}`), now.Add(time.Hour)) // upsert

	if n, err := s.Prune(t.Context()); err != nil || n != 1 {
		t.Fatalf("Prune = %d %v", n, err)
	}

	data, found, err := s.Load(t.Context(), "b")
	if err != nil || !found || string(data) != `{"v":1}` {
		t.Fatalf("Load = %s %v %v", data, found, err)
	}

	if err := s.Delete(t.Context(), "b"); err != nil {
		t.Fatal(err)
	}

	pg, _ := NewSQLStore(db, WithDollarPlaceholders())
	if !strings.Contains(pg.qSave, "$3") {
		t.Fatalf("dollar placeholders: %s", pg.qSave)
	}

	my, _ := NewSQLStore(db, WithMySQL())
	if !strings.Contains(my.qSave, "ON DUPLICATE KEY UPDATE data = VALUES(data)") ||
		!strings.Contains(
			my.qCreate,
			"id VARBINARY(255) NOT NULL PRIMARY KEY",
		) || !strings.Contains(my.qLoad, "id = ?") {
		t.Fatalf("mysql queries: %s / %s", my.qSave, my.qCreate)
	}

	_ = db.Close()
	if _, _, err := s.Load(context.Background(), "x"); err == nil {
		t.Fatal("Load on closed db = nil error")
	}
}

func TestMemoryStoreExpiry(t *testing.T) {
	t.Parallel()

	clk := &clock{t: time.Unix(1000, 0)}
	s := NewMemoryStore()
	s.now = clk.Now

	_ = s.Save(t.Context(), "a", []byte("x"), clk.Now().Add(time.Second))
	clk.Advance(2 * time.Second)

	if _, found, _ := s.Load(t.Context(), "a"); found {
		t.Fatal("expired entry loaded")
	}

	_ = s.Save(t.Context(), "b", []byte("y"), clk.Now().Add(time.Hour))
	if s.Len() != 1 {
		t.Fatalf("Len = %d", s.Len())
	}
	_ = s.Delete(t.Context(), "b")
	if s.Len() != 0 {
		t.Fatal("Delete")
	}
}

func TestPeekAndStreaming(t *testing.T) {
	t.Parallel()

	m, _ := newManager(t, Config{})
	h := m.Middleware()(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		web.SessionFrom(r.Context()).Put("user", "ana")

		if err := http.NewResponseController(w).Flush(); err != nil {
			t.Error(err)
		}

		_, _ = w.Write([]byte("streamed"))
	}))

	rec := httptest.NewRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
	h.ServeHTTP(rec, r.WithContext(web.WithServices(r.Context(), &web.Services{})))

	if !rec.Flushed || rec.Body.String() != "streamed" {
		t.Fatalf("flushed=%v body=%q", rec.Flushed, rec.Body.String())
	}

	if got := m.Peek(t.Context(), rec.Result().Cookies()); got["user"] != "ana" {
		t.Fatalf("Peek = %v", got)
	}

	if got := m.Peek(t.Context(), nil); len(got) != 0 {
		t.Fatalf("Peek without cookies = %v", got)
	}
}
