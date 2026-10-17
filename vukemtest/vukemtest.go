// Package vukemtest is Vukem's testing toolkit: boot an app for a test with
// an isolated in-memory database, send in-process HTTP requests with a
// fluent client, and assert on responses, sessions and the database.
//
//	func TestCreatePost(t *testing.T) {
//		app := vukemtest.New(t, bootstrap.New)
//
//		app.Client(t).
//			PostForm("/posts", url.Values{"title": {"Hello"}}).
//			AssertRedirect("/posts").
//			AssertSessionHas("success")
//
//		vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{"title": "Hello"})
//	}
//
// [New] returns an [App] wrapping the booted app, its handler and fakes
// (mail through [App.Mails], social logins through [FakeSocial]); a
// [Client] sends requests and returns a [Response] with Assert helpers;
// [FakeClock] controls time in tests.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/testing.md.
package vukemtest

import (
	"context"
	"crypto/rand"
	"io"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/social"
)

// App is an application booted for one test.
type App struct {
	*vukem.App

	t       testing.TB
	handler http.Handler
}

// Options tune New.
type Options struct {
	// Env adds or overrides configuration values.
	Env map[string]string
	// Clock replaces the app clock (see FakeClock).
	Clock func() time.Time
	// SkipMigrations does not run registered migrations.
	SkipMigrations bool
	// AppOptions are extra vukem options passed to build.
	AppOptions []vukem.Option
}

var nonWord = regexp.MustCompile(`\W+`)

// New builds the app with build (usually bootstrap.New) in the test
// environment: APP_ENV=test, a random APP_KEY, a private in-memory SQLite
// database, no HTTP listener. Registered migrations run, and the app shuts
// down in t.Cleanup.
func New(tb testing.TB, build vukem.BuildFunc, opts ...Options) *App {
	tb.Helper()

	var o Options
	if len(opts) > 0 {
		o = opts[0]
	}

	env := map[string]string{
		"APP_ENV": "test",
		"APP_KEY": config.GenerateKey(),
		"DATABASE_URL": "file:" + nonWord.ReplaceAllString(
			tb.Name(),
			"_",
		) + "_" + rand.Text()[:8] + "?mode=memory&cache=shared",
		"SESSION_DRIVER": "cookie",
		"MAIL_MAILER":    "array",
	}
	maps.Copy(env, o.Env)

	appOpts := []vukem.Option{
		vukem.WithSource(config.MapSource(env)),
		vukem.WithKind(vukem.KindTest),
		vukem.WithoutHTTP(),
		vukem.WithLogOutput(io.Discard),
		vukem.WithStdout(io.Discard),
	}

	if o.Clock != nil {
		appOpts = append(appOpts, vukem.WithClock(o.Clock))
	}

	app, err := build(append(appOpts, o.AppOptions...)...)
	if err != nil {
		tb.Fatalf("vukemtest: build app: %v", err)
	}

	tb.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	if err := app.Boot(tb.Context()); err != nil {
		tb.Fatalf("vukemtest: boot: %v", err)
	}

	if !o.SkipMigrations {
		m, err := app.Migrator()
		if err != nil {
			tb.Fatalf("vukemtest: open database: %v", err)
		}

		if _, err := m.Up(tb.Context()); err != nil {
			tb.Fatalf("vukemtest: migrate: %v", err)
		}
	}

	h, err := app.Handler()
	if err != nil {
		tb.Fatalf("vukemtest: routes: %v", err)
	}

	return &App{App: app, t: tb, handler: h}
}

// Mails returns the in-memory mail transport (MAIL_MAILER=array, the
// default in tests) to assert on sent messages.
func (a *App) Mails() *mail.Array {
	a.t.Helper()

	m, err := a.Mailer()
	if err != nil {
		a.t.Fatalf("vukemtest: mailer: %v", err)
	}

	arr, ok := m.Transport().(*mail.Array)
	if !ok {
		a.t.Fatalf("vukemtest: mail transport is %T, not *mail.Array", m.Transport())
	}

	return arr
}

// FakeSocial returns Options.Env entries that make apps scaffolded with
// install:auth --social sign in u through a fake provider (test apps only).
func FakeSocial(u social.User) map[string]string {
	verified := "false"
	if u.EmailVerified {
		verified = "true"
	}

	return map[string]string{
		"SOCIAL_FAKE_ID": u.ID, "SOCIAL_FAKE_EMAIL": u.Email, "SOCIAL_FAKE_NAME": u.Name,
		"SOCIAL_FAKE_EMAIL_VERIFIED": verified,
	}
}

// Handler returns the compiled HTTP handler.
func (a *App) Handler() http.Handler { return a.handler }

// RefreshDatabase drops every table and re-runs the migrations.
func (a *App) RefreshDatabase(tb testing.TB) {
	tb.Helper()

	m, err := a.Migrator()
	if err != nil {
		tb.Fatalf("vukemtest: %v", err)
	}

	if _, err := m.Fresh(tb.Context()); err != nil {
		tb.Fatalf("vukemtest: refresh database: %v", err)
	}
}

// FakeClock is a controllable clock; pass clock.Now as Options.Clock.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFakeClock returns a clock fixed at start.
func NewFakeClock(start time.Time) *FakeClock { return &FakeClock{now: start} }

// Now returns the clock's time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.now
}

// Advance moves the clock forward.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = c.now.Add(d)
}

// Set moves the clock to t.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.now = t
}

// AssertDatabaseHas fails unless a row of table matches every column value.
func AssertDatabaseHas(tb testing.TB, app *App, table string, where map[string]any) {
	tb.Helper()

	if n := countRows(tb, app, table, where); n == 0 {
		tb.Fatalf("vukemtest: no row in %s matches %v", table, where)
	}
}

// AssertDatabaseMissing fails if a row of table matches every column value.
func AssertDatabaseMissing(tb testing.TB, app *App, table string, where map[string]any) {
	tb.Helper()

	if n := countRows(tb, app, table, where); n != 0 {
		tb.Fatalf("vukemtest: %d row(s) in %s match %v, want none", n, table, where)
	}
}

// AssertDatabaseCount fails unless table has exactly n rows.
func AssertDatabaseCount(tb testing.TB, app *App, table string, n int64) {
	tb.Helper()

	if got := countRows(tb, app, table, nil); got != n {
		tb.Fatalf("vukemtest: %s has %d rows, want %d", table, got, n)
	}
}

var identRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func countRows(tb testing.TB, app *App, table string, where map[string]any) int64 {
	tb.Helper()

	db, err := app.DB()
	if err != nil {
		tb.Fatalf("vukemtest: %v", err)
	}

	if !identRe.MatchString(table) {
		tb.Fatalf("vukemtest: invalid table %q", table)
	}

	d := db.Dialect()

	var (
		conds []string
		args  []any
	)

	for _, col := range slices.Sorted(maps.Keys(where)) {
		if !identRe.MatchString(col) {
			tb.Fatalf("vukemtest: invalid column %q", col)
		}

		if where[col] == nil {
			conds = append(conds, d.Quote(col)+" IS NULL")
			continue
		}

		args = append(args, where[col])
		conds = append(conds, d.Quote(col)+" = "+d.Placeholder(len(args)))
	}

	q := "SELECT COUNT(*) FROM " + d.Quote(table)
	if len(conds) > 0 {
		q += " WHERE " + strings.Join(conds, " AND ")
	}

	var count int64
	if err := db.QueryRowContext(tb.Context(), q, args...).Scan(&count); err != nil {
		tb.Fatalf("vukemtest: count %s: %v", table, err)
	}

	return count
}
