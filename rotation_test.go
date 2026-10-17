package vukem

import (
	"bytes"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/crypt"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/session"
	"github.com/AlexGalhardo/vukem/signed"
	"github.com/AlexGalhardo/vukem/web"
)

// rotationApps are the three stages of an APP_KEY rotation: before (the
// old key), rotated (new key, old key in APP_PREVIOUS_KEYS) and dropped
// (new key only, the old key removed).
type rotationApps struct{ before, rotated, dropped *App }

func newRotationApps(t *testing.T, extra map[string]string, opts ...func() Option) rotationApps {
	t.Helper()

	oldKey, newKey := config.GenerateKey(), config.GenerateKey()

	build := func(env map[string]string) *App {
		m := maps.Clone(extra)
		if m == nil {
			m = map[string]string{}
		}

		maps.Copy(m, env)

		o := []Option{WithSource(testSource(m))}
		for _, opt := range opts {
			o = append(o, opt())
		}

		app := newTestApp(t, o...)
		t.Cleanup(func() { _ = app.Shutdown(t.Context()) }) // Windows: close the DB before TempDir removal

		return app
	}

	return rotationApps{
		before:  build(map[string]string{"APP_KEY": oldKey}),
		rotated: build(map[string]string{"APP_KEY": newKey, "APP_PREVIOUS_KEYS": config.GenerateKey() + "," + oldKey}),
		dropped: build(map[string]string{"APP_KEY": newKey}),
	}
}

func TestDerivePreviousKeys(t *testing.T) {
	t.Parallel()

	apps := newRotationApps(t, nil)

	old, err := apps.before.DeriveKey("signed.urls", 32)
	if err != nil {
		t.Fatal(err)
	}

	prev := apps.rotated.DerivePreviousKeys("signed.urls", 32)
	if len(prev) != 2 || !bytes.Equal(prev[1], old) {
		t.Fatalf("DerivePreviousKeys() = %d keys, want 2 with the old derived key last", len(prev))
	}

	if got := apps.dropped.DerivePreviousKeys("signed.urls", 32); got != nil {
		t.Fatalf("DerivePreviousKeys() without APP_PREVIOUS_KEYS = %x, want nil", got)
	}
}

// TestSecurity_A04_KeyRotationEndToEnd checks every feature keyed by
// APP_KEY across a rotation: values written under the old key still open
// while it is in APP_PREVIOUS_KEYS, new values use the new key, and once
// the old key is removed its values fail closed.
func TestSecurity_A04_KeyRotationEndToEnd(t *testing.T) {
	t.Parallel()

	t.Run("cookie sessions, CSRF token and flash", func(t *testing.T) {
		t.Parallel()

		apps := newRotationApps(t, nil)
		ctx := t.Context()

		before := mustSessions(t, apps.before)
		rec := httptest.NewRecorder()
		before.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			s := web.SessionFrom(r.Context())
			s.Put(web.TokenKey, "csrf-token")
			s.Put(web.SessionUserKey, "42")
			s.Flash("status", "saved")
		})).ServeHTTP(rec, httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody))
		oldCookies := rec.Result().Cookies()

		rotated := mustSessions(t, apps.rotated)
		data := rotated.Peek(ctx, oldCookies)

		for k, want := range map[string]string{web.TokenKey: "csrf-token", web.SessionUserKey: "42", "status": "saved"} {
			if got, _ := data[k].(string); got != want {
				t.Errorf("rotated Peek(old cookie)[%q] = %q, want %q", k, got, want)
			}
		}

		// A read-only request re-seals the session with the new key.
		rec = httptest.NewRecorder()
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)

		for _, c := range oldCookies {
			req.AddCookie(c)
		}

		rotated.Middleware()(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			_ = web.SessionFrom(r.Context()).Get(web.SessionUserKey)
		})).ServeHTTP(rec, req)

		resealed := rec.Result().Cookies()
		if len(resealed) == 0 {
			t.Fatal("rotated session middleware did not re-seal a session opened with a previous key")
		}

		dropped := mustSessions(t, apps.dropped)
		if got, _ := dropped.Peek(ctx, resealed)[web.SessionUserKey].(string); got != "42" {
			t.Errorf("new-key-only Peek(re-sealed cookie)[user] = %q, want 42", got)
		}

		if got := dropped.Peek(ctx, oldCookies); len(got) != 0 {
			t.Errorf("new-key-only Peek(old cookie) = %v, want an empty session", got)
		}
	})

	t.Run("signed URLs", func(t *testing.T) {
		t.Parallel()

		apps := newRotationApps(t, nil)
		signer := func(app *App) *signed.Signer {
			key, err := app.DeriveKey("signed.urls", 32)
			if err != nil {
				t.Fatal(err)
			}

			return signed.New(key).WithPreviousKeys(app.DerivePreviousKeys("signed.urls", 32)...)
		}

		verify := func(s *signed.Signer, link string) error {
			return s.Verify(httptest.NewRequestWithContext(t.Context(), http.MethodGet, link, http.NoBody))
		}

		oldLink, err := signer(apps.before).Sign("https://app.test/email/verify/42", time.Hour)
		if err != nil {
			t.Fatal(err)
		}

		if err := verify(signer(apps.rotated), oldLink); err != nil {
			t.Errorf("rotated Verify(old link) = %v, want nil", err)
		}

		newLink, err := signer(apps.rotated).Sign("https://app.test/email/verify/42", time.Hour)
		if err != nil {
			t.Fatal(err)
		}

		if err := verify(signer(apps.dropped), newLink); err != nil {
			t.Errorf("new-key-only Verify(link signed after rotation) = %v, want nil", err)
		}

		if err := verify(signer(apps.dropped), oldLink); !errors.Is(err, signed.ErrInvalid) {
			t.Errorf("new-key-only Verify(old link) = %v, want signed.ErrInvalid", err)
		}
	})

	t.Run("encrypted cookies", func(t *testing.T) {
		t.Parallel()

		apps := newRotationApps(t, nil)
		set := func(app *App) []*http.Cookie {
			rec := httptest.NewRecorder()
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

			if err := app.Crypt.SetCookie(rec, r, http.Cookie{Name: "prefs", Value: "dark"}); err != nil {
				t.Fatal(err)
			}

			return rec.Result().Cookies()
		}

		get := func(app *App, cookies []*http.Cookie) (string, error) {
			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)
			for _, c := range cookies {
				r.AddCookie(c)
			}

			return app.Crypt.Cookie(r, "prefs")
		}

		old := set(apps.before)
		if got, err := get(apps.rotated, old); err != nil || got != "dark" {
			t.Errorf("rotated Cookie(old) = %q, %v, want dark", got, err)
		}

		if got, err := get(apps.dropped, set(apps.rotated)); err != nil || got != "dark" {
			t.Errorf("new-key-only Cookie(set after rotation) = %q, %v, want dark", got, err)
		}

		if _, err := get(apps.dropped, old); !errors.Is(err, crypt.ErrInvalid) {
			t.Errorf("new-key-only Cookie(old) error = %v, want crypt.ErrInvalid", err)
		}
	})

	t.Run("maintenance bypass cookie", func(t *testing.T) {
		t.Parallel()

		apps := newRotationApps(t, nil)
		file := filepath.Join(testutil.TempDir(t), "down")

		if err := middleware.WriteMaintenance(file, middleware.MaintenanceState{Secret: "let-me-in"}); err != nil {
			t.Fatal(err)
		}

		serve := func(app *App, path string, cookies []*http.Cookie) *httptest.ResponseRecorder {
			cfg := maintenanceConfig(app)
			cfg.File = file

			r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, path, http.NoBody)
			for _, c := range cookies {
				r.AddCookie(c)
			}

			rec := httptest.NewRecorder()
			middleware.Maintenance(cfg)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			})).ServeHTTP(rec, r)

			return rec
		}

		old := serve(apps.before, "/let-me-in", nil).Result().Cookies()
		if len(old) != 1 {
			t.Fatalf("bypass visit set %d cookies, want 1", len(old))
		}

		rec := serve(apps.rotated, "/", old)
		if rec.Code != http.StatusOK {
			t.Errorf("rotated bypass with old cookie = %d, want 200", rec.Code)
		}

		reissued := rec.Result().Cookies()
		if len(reissued) != 1 {
			t.Fatalf("rotated bypass with old cookie re-issued %d cookies, want 1", len(reissued))
		}

		if code := serve(apps.dropped, "/", reissued).Code; code != http.StatusOK {
			t.Errorf("new-key-only bypass with re-issued cookie = %d, want 200", code)
		}

		if code := serve(apps.dropped, "/", old).Code; code != http.StatusServiceUnavailable {
			t.Errorf("new-key-only bypass with old cookie = %d, want 503", code)
		}
	})

	t.Run("queued mail", func(t *testing.T) {
		t.Parallel()

		var fakes []*queue.Fake // before, rotated, dropped

		apps := newRotationApps(t, map[string]string{"MAIL_MAILER": "array"}, func() Option {
			f := queue.NewFake()
			fakes = append(fakes, f)

			return WithQueueDriver(f)
		})
		fakeOf := map[*App]*queue.Fake{apps.before: fakes[0], apps.rotated: fakes[1], apps.dropped: fakes[2]}

		payload := func(app *App) json.RawMessage {
			m, err := app.Mailer()
			if err != nil {
				t.Fatal(err)
			}

			if err := m.SendLater(t.Context(), resetMail{}); err != nil {
				t.Fatal(err)
			}

			msgs := fakeOf[app].Messages()

			return msgs[len(msgs)-1].Payload
		}

		// deliver runs a payload queued by another app on app's worker and
		// reports how many mails it sent.
		deliver := func(app *App, p json.RawMessage) int {
			q, err := app.Queue()
			if err != nil {
				t.Fatal(err)
			}

			if err := q.DispatchKind(t.Context(), MailJobKind, p, queue.Retries(0)); err != nil {
				t.Fatal(err)
			}

			_ = q.Work(t.Context(), queue.WorkOptions{StopWhenEmpty: true})

			m, _ := app.Mailer()

			return len(m.Transport().(*mail.Array).Sent())
		}

		old := payload(apps.before)
		if n := deliver(apps.rotated, old); n != 1 {
			t.Errorf("rotated worker sent %d mails for a job sealed with the old key, want 1", n)
		}

		if n := deliver(apps.dropped, payload(apps.rotated)); n != 1 {
			t.Errorf("new-key-only worker sent %d mails for a job sealed after rotation, want 1", n)
		}

		if n := deliver(apps.dropped, old); n != 1 {
			t.Errorf("new-key-only worker sent a job sealed with the removed key (%d mails total, want 1)", n)
		}

		if f := fakes[2].Failed(); len(f) != 1 {
			t.Errorf("new-key-only worker failed %d jobs, want 1 (the old-key job)", len(f))
		}
	})

	t.Run("encrypted columns", func(t *testing.T) {
		t.Parallel()

		url := "file:" + filepath.Join(testutil.TempDir(t), "rotation.db")
		apps := newRotationApps(t, map[string]string{"DATABASE_URL": url})
		ctx := t.Context()

		db := func(app *App) *database.DB {
			d, err := app.DB()
			if err != nil {
				t.Fatal(err)
			}

			return d
		}

		if _, err := db(apps.before).ExecContext(ctx, `CREATE TABLE vaults (id INTEGER PRIMARY KEY AUTOINCREMENT,
			created_at TIMESTAMP, updated_at TIMESTAMP, secret TEXT)`); err != nil {
			t.Fatal(err)
		}

		old := vault{Secret: orm.Encrypted[string]{V: "4111"}}
		if err := orm.Create(ctx, db(apps.before), &old); err != nil {
			t.Fatal(err)
		}

		got, err := orm.QueryOf[vault](db(apps.rotated)).Find(ctx, old.ID)
		if err != nil || got.Secret.V != "4111" {
			t.Fatalf("rotated Find(old row) = %+v, %v, want 4111", got, err)
		}

		fresh := vault{Secret: orm.Encrypted[string]{V: "5500"}}
		if err := orm.Create(ctx, db(apps.rotated), &fresh); err != nil {
			t.Fatal(err)
		}

		if got, err := orm.QueryOf[vault](db(apps.dropped)).Find(ctx, fresh.ID); err != nil || got.Secret.V != "5500" {
			t.Errorf("new-key-only Find(row written after rotation) = %+v, %v, want 5500", got, err)
		}

		if _, err := orm.QueryOf[vault](db(apps.dropped)).Find(ctx, old.ID); !errors.Is(err, crypt.ErrInvalid) {
			t.Errorf("new-key-only Find(old row) error = %v, want crypt.ErrInvalid", err)
		}
	})
}

type vault struct {
	orm.Model
	Secret orm.Encrypted[string] `json:"-"`
}

func mustSessions(t *testing.T, app *App) *session.Manager {
	t.Helper()

	m, err := app.Sessions()
	if err != nil {
		t.Fatal(err)
	}

	return m
}
