package vukem

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/session"
)

var appMigrations = []schema.Migration{{
	ID: "20260101000000_create_notes",
	Up: func(s *schema.Schema) error {
		return s.Create("notes", func(t *schema.Table) { t.ID(); t.String("body") })
	},
	Down: func(s *schema.Schema) error { return s.Drop("notes") },
}}

func TestMigrateCommands(t *testing.T) {
	t.Parallel()

	dbURL := "file:" + filepath.Join(testutil.TempDir(t), "app.db")

	run := func(env string, args ...string) (string, error) {
		var out bytes.Buffer

		getenv := func(k string) (string, bool) {
			v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": env, "DATABASE_URL": dbURL}[k]
			return v, ok
		}

		build := func(opts ...Option) (*App, error) {
			return New(append(opts, WithLogger(discardLogger()), WithStdout(&out), WithMigrations(appMigrations))...)
		}

		err := RunMain(t.Context(), append([]string{"app"}, args...), getenv, build)

		return out.String(), err
	}

	if out, err := run(
		"test",
		"migrate",
	); err != nil ||
		!strings.Contains(out, "Migrated: 20260101000000_create_notes") {
		t.Fatalf("migrate = %q %v", out, err)
	}
	if out, _ := run("test", "migrate"); !strings.Contains(out, "Nothing to migrate.") {
		t.Fatalf("second migrate = %q", out)
	}
	if out, err := run("test", "migrate:status"); err != nil || !strings.Contains(out, "Ran") {
		t.Fatalf("status = %q %v", out, err)
	}
	if out, err := run("test", "migrate:status", "-json"); err != nil || !strings.Contains(out, `"ran":true`) {
		t.Fatalf("status json = %q %v", out, err)
	}
	if out, err := run("test", "migrate:rollback", "-step", "1"); err != nil || !strings.Contains(out, "Rolled back:") {
		t.Fatalf("rollback = %q %v", out, err)
	}

	// Destructive commands need --force in production.
	if _, err := run("production", "migrate:fresh"); !errors.Is(err, errForceRequired) {
		t.Fatalf("fresh in production = %v", err)
	}
	if out, err := run("production", "migrate:fresh", "-force"); err != nil || !strings.Contains(out, "Migrated:") {
		t.Fatalf("fresh --force = %q %v", out, err)
	}
	if out, err := run("test", "migrate:reset"); err != nil || !strings.Contains(out, "Rolled back:") {
		t.Fatalf("reset = %q %v", out, err)
	}
}

func TestSecurity_A02_DestructiveCommandsNeedForce(t *testing.T) {
	t.Parallel()

	for _, cmd := range []string{"migrate:fresh", "migrate:reset", "db:wipe", "db:seed"} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()

			dbURL := "file:" + filepath.Join(testutil.TempDir(t), "app.db")
			getenv := func(k string) (string, bool) {
				v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": "production", "DATABASE_URL": dbURL}[k]
				return v, ok
			}

			build := func(opts ...Option) (*App, error) {
				return New(
					append(opts, WithLogger(discardLogger()), WithStdout(io.Discard), WithMigrations(appMigrations))...)
			}

			if err := RunMain(t.Context(), []string{"app", cmd}, getenv, build); !errors.Is(err, errForceRequired) {
				t.Fatalf("%s in production = %v, want errForceRequired", cmd, err)
			}
		})
	}
}

func TestDBHealthCheckAndBadURL(t *testing.T) {
	t.Parallel()

	app := newTestApp(
		t,
		WithSource(testSource(map[string]string{"DATABASE_URL": "file:" + filepath.Join(testutil.TempDir(t), "h.db")})),
	)

	db, err := app.DB()
	if err != nil || db == nil {
		t.Fatal(err)
	}

	if rec := doReq(
		t,
		app,
		"/up?checks=1",
	); rec.Code != http.StatusOK ||
		!strings.Contains(rec.Body.String(), `"db":"ok"`) {
		t.Fatalf("health = %d %s", rec.Code, rec.Body.String())
	}

	if err := app.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	bad := newTestApp(t, WithSource(testSource(map[string]string{"DATABASE_URL": "mysql://nope"})))
	if _, err := bad.DB(); err == nil {
		t.Fatal("unsupported URL accepted")
	}
	if _, err := bad.Migrator(); err == nil {
		t.Fatal("Migrator without DB")
	}
}

func TestAppDBUsesCrypt(t *testing.T) {
	t.Parallel()

	app := newTestApp(
		t,
		WithSource(testSource(map[string]string{"DATABASE_URL": "file:" + filepath.Join(testutil.TempDir(t), "c.db")})),
	)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	if db.Encrypter() != app.Crypt {
		t.Fatalf("DB().Encrypter() = %v, want app.Crypt", db.Encrypter())
	}
}

type countingObserver struct {
	mu  sync.Mutex
	sql []string
}

func (o *countingObserver) ObserveQuery(_ context.Context, q database.Query) {
	o.mu.Lock()
	defer o.mu.Unlock()

	o.sql = append(o.sql, q.SQL)
}

func TestAppDBQueryObserversKeepDevtools(t *testing.T) {
	t.Parallel()

	tracing := &countingObserver{}

	app, err := New(
		WithSource(testSource(map[string]string{
			"APP_ENV": "development", "DEVTOOLS": "true", "DATABASE_URL": ":memory:",
		})),
		WithLogger(discardLogger()),
		WithQueryObservers(tracing, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"SELECT 1"}, tracing.sql); diff != "" {
		t.Errorf("tracing observer (-want +got):\n%s", diff)
	}

	if app.Devtools == nil {
		t.Fatal("devtools not enabled")
	}

	if got := len(app.Devtools.Queries()); got != 1 {
		t.Errorf("len(Devtools.Queries()) = %d, want 1", got)
	}
}

type notesSeeder struct{ n int }

func (s notesSeeder) Seed(ctx context.Context, db *database.DB) error {
	for range s.n {
		if _, err := db.ExecContext(ctx, "INSERT INTO notes (body) VALUES ('seeded')"); err != nil {
			return err
		}
	}

	return nil
}

func TestSeedAndWipeCommands(t *testing.T) {
	t.Parallel()

	dbURL := "file:" + filepath.Join(testutil.TempDir(t), "app.db")
	failing := SeederFunc(func(context.Context, *database.DB) error { return errors.New("boom") })

	run := func(env string, seeders []Seeder, args ...string) (string, error) {
		var out bytes.Buffer

		getenv := func(k string) (string, bool) {
			v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": env, "DATABASE_URL": dbURL}[k]
			return v, ok
		}

		build := func(opts ...Option) (*App, error) {
			return New(append(opts, WithLogger(discardLogger()), WithStdout(&out), WithMigrations(appMigrations),
				WithSeeders(seeders...))...)
		}

		err := RunMain(t.Context(), append([]string{"app"}, args...), getenv, build)

		return out.String(), err
	}

	seeders := []Seeder{notesSeeder{n: 2}, notesSeeder{n: 1}}

	if _, err := run("test", seeders, "migrate"); err != nil {
		t.Fatal(err)
	}

	out, err := run("test", seeders, "db:seed")
	if err != nil || strings.Count(out, "Seeded: notesSeeder") != 2 {
		t.Fatalf("db:seed = %q %v", out, err)
	}

	db, err := database.Open(t.Context(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	var n int
	if err := db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM notes").Scan(&n); err != nil || n != 3 {
		t.Fatalf("notes = %d %v", n, err)
	}

	if out, _ := run("test", nil, "db:seed"); !strings.Contains(out, "No seeders registered") {
		t.Fatalf("no seeders = %q", out)
	}

	if _, err := run(
		"test",
		[]Seeder{failing},
		"db:seed",
	); err == nil ||
		!strings.Contains(err.Error(), "seed SeederFunc: boom") {
		t.Fatalf("failing seeder = %v", err)
	}

	// Production needs --force for both commands.
	for _, cmd := range []string{"db:seed", "db:wipe"} {
		if _, err := run("production", seeders, cmd); !errors.Is(err, errForceRequired) {
			t.Fatalf("%s in production = %v", cmd, err)
		}
	}

	if out, err := run(
		"production",
		seeders,
		"db:wipe",
		"--force",
	); err != nil ||
		!strings.Contains(out, "Dropped all tables.") {
		t.Fatalf("db:wipe = %q %v", out, err)
	}

	if has, err := schema.New(t.Context(), db).HasTable("notes"); err != nil || has {
		t.Fatalf("notes after wipe: has=%v err=%v", has, err)
	}
}

func TestAppSeedersReachTheApp(t *testing.T) {
	t.Parallel()

	var (
		out   bytes.Buffer
		order []string
	)

	dbURL := "file:" + filepath.Join(testutil.TempDir(t), "app.db")
	record := func(name string) Seeder {
		return SeederFunc(func(context.Context, *database.DB) error { order = append(order, name); return nil })
	}
	withApp := AppSeederFunc(func(ctx context.Context, app *App) error {
		db, err := app.DB()
		if err != nil {
			return err
		}

		var one int
		if err := db.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
			return err
		}

		order = append(order, "app:"+app.Source.Get("SEED_PASSWORD"))

		return nil
	})

	app, err := New(
		WithSource(config.MapSource(map[string]string{
			"APP_KEY": testKey, "APP_ENV": "test", "DATABASE_URL": dbURL, "SEED_PASSWORD": "s3cret",
		})),
		WithLogger(discardLogger()), WithStdout(&out),
		WithSeeders(record("db1")),
		WithAppSeeders(withApp),
		WithSeeders(record("db2")),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	if err := app.Seed(t.Context()); err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff([]string{"db1", "app:s3cret", "db2"}, order); diff != "" {
		t.Errorf("seed order (-want +got):\n%s", diff)
	}

	if got := strings.Count(out.String(), "Seeded: SeederFunc"); got != 2 {
		t.Errorf("db seeders printed %d times in %q", got, out.String())
	}

	if !strings.Contains(out.String(), "Seeded: AppSeederFunc") {
		t.Errorf("app seeder not printed in %q", out.String())
	}

	failing := AppSeederFunc(func(context.Context, *App) error { return errors.New("boom") })

	app2, err := New(WithSource(config.MapSource(map[string]string{"APP_KEY": testKey, "APP_ENV": "test"})),
		WithLogger(discardLogger()), WithStdout(&out), WithAppSeeders(failing))
	if err != nil {
		t.Fatal(err)
	}

	if err := app2.Seed(t.Context()); err == nil || !strings.Contains(err.Error(), "seed AppSeederFunc: boom") {
		t.Fatalf("failing app seeder = %v", err)
	}
}

func TestSeederNames(t *testing.T) {
	t.Parallel()

	tests := []struct {
		s    AppSeeder
		want string
	}{
		{dbSeeder{notesSeeder{}}, "notesSeeder"},
		{AppSeederFunc(func(context.Context, *App) error { return nil }), "AppSeederFunc"},
		{NamedAppSeeder("DemoShop", func(context.Context, *App) error { return nil }), "DemoShop"},
		{dbSeeder{namedSeeder{}}, "Users"},
	}

	for _, tt := range tests {
		if got := seederName(tt.s); got != tt.want {
			t.Errorf("seederName(%T) = %q, want %q", tt.s, got, tt.want)
		}
	}
}

type namedSeeder struct{}

func (namedSeeder) Seed(context.Context, *database.DB) error { return nil }
func (namedSeeder) Name() string                             { return "Users" }

func TestWithStores(t *testing.T) {
	t.Parallel()

	store := cache.NewMemory(10)
	app := newTestApp(t, WithCacheStore(store), WithSessionStore(session.NewMemoryStore()),
		WithSource(testSource(map[string]string{"CACHE_STORE": "unknown-is-ignored", "SESSION_DRIVER": "nope"})))

	c, err := app.Cache()
	if err != nil {
		t.Fatal(err)
	}

	if err := c.Put(t.Context(), "k", 1, 0); err != nil {
		t.Fatal(err)
	}

	if _, ok, _ := store.Get(t.Context(), "vukem:k"); !ok {
		t.Error("cache did not use the given store")
	}

	if _, err := app.Sessions(); err != nil {
		t.Errorf("sessions with a given store = %v", err)
	}
}

func TestLazyStoreFuncs(t *testing.T) {
	t.Parallel()

	var calls []string

	store := sharedStore{cache.NewMemory(10)}
	fake := queue.NewFake()

	app := newTestApp(t,
		WithSource(testSource(map[string]string{"REDIS_URL": "redis://from-config"})),
		WithCacheStoreFunc(func(app *App) (cache.Store, error) {
			calls = append(calls, "cache:"+app.Source.Get("REDIS_URL"))
			return store, nil
		}),
		WithSessionStoreFunc(func(app *App) (session.Store, error) {
			calls = append(calls, "session:"+app.Source.Get("REDIS_URL"))
			return session.NewMemoryStore(), nil
		}),
		WithQueueDriverFunc(func(app *App) (queue.Driver, error) {
			calls = append(calls, "queue:"+app.Source.Get("REDIS_URL"))
			return fake, nil
		}),
	)

	if len(calls) != 0 {
		t.Fatalf("store funcs ran during New: %v, want lazy", calls)
	}

	for range 2 {
		if _, err := app.Cache(); err != nil {
			t.Fatal(err)
		}

		if _, err := app.Sessions(); err != nil {
			t.Fatal(err)
		}

		if _, err := app.Queue(); err != nil {
			t.Fatal(err)
		}
	}

	want := []string{"cache:redis://from-config", "session:redis://from-config", "queue:redis://from-config"}
	if diff := cmp.Diff(want, calls); diff != "" {
		t.Errorf("store func calls (-want +got):\n%s", diff)
	}

	shared, err := app.cacheShared()
	if err != nil || !shared {
		t.Errorf("cacheShared() = %v, %v, want true (a non-memory store is shared)", shared, err)
	}
}

func TestLazyStoreFuncErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("redis down")
	app := newTestApp(t,
		WithCacheStoreFunc(func(*App) (cache.Store, error) { return nil, boom }),
		WithSessionStoreFunc(func(*App) (session.Store, error) { return nil, boom }),
		WithQueueDriverFunc(func(*App) (queue.Driver, error) { return nil, boom }),
	)

	if _, err := app.Cache(); !errors.Is(err, boom) {
		t.Errorf("Cache() error = %v, want %v", err, boom)
	}

	if _, err := app.Sessions(); !errors.Is(err, boom) {
		t.Errorf("Sessions() error = %v, want %v", err, boom)
	}

	if _, err := app.Queue(); !errors.Is(err, boom) {
		t.Errorf("Queue() error = %v, want %v", err, boom)
	}
}

func TestLazyStoreFuncsRejectNil(t *testing.T) {
	t.Parallel()

	app := newTestApp(t,
		WithCacheStoreFunc(func(*App) (cache.Store, error) { return nil, nil }),
		WithSessionStoreFunc(func(*App) (session.Store, error) { return nil, nil }),
		WithQueueDriverFunc(func(*App) (queue.Driver, error) { return nil, nil }),
	)

	if _, err := app.Cache(); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("Cache() error = %v, want a nil-store error", err)
	}

	if _, err := app.Sessions(); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("Sessions() error = %v, want a nil-store error", err)
	}

	if _, err := app.Queue(); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("Queue() error = %v, want a nil-driver error", err)
	}
}

// A store func returns ErrUseDefault (directly or wrapped) when its service
// is not configured: the app then builds what CACHE_STORE, SESSION_DRIVER
// and QUEUE_CONNECTION choose, as if no func had been passed.
func TestLazyStoreFuncsUseDefault(t *testing.T) {
	t.Parallel()

	for name, errDefault := range map[string]error{
		"direct":  ErrUseDefault,
		"wrapped": fmt.Errorf("REDIS_URL is not set: %w", ErrUseDefault),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			app := newTestApp(t,
				WithCacheStoreFunc(func(*App) (cache.Store, error) { return nil, errDefault }),
				WithSessionStoreFunc(func(*App) (session.Store, error) { return nil, errDefault }),
				WithQueueDriverFunc(func(*App) (queue.Driver, error) { return nil, errDefault }),
			)

			c, err := app.Cache()
			if err != nil {
				t.Fatalf("Cache() = %v, want the CACHE_STORE default", err)
			}

			if _, ok := c.Store().(*cache.Memory); !ok {
				t.Errorf("Cache().Store() = %T, want *cache.Memory (CACHE_STORE=memory)", c.Store())
			}

			if _, err := app.Sessions(); err != nil {
				t.Fatalf("Sessions() = %v, want the SESSION_DRIVER default", err)
			}

			if app.sessStore != nil {
				t.Errorf("session store = %T, want none (SESSION_DRIVER=cookie)", app.sessStore)
			}

			q, err := app.Queue()
			if err != nil {
				t.Fatalf("Queue() = %v, want the QUEUE_CONNECTION default", err)
			}

			if _, ok := q.Driver().(queue.Sync); !ok {
				t.Errorf("Queue().Driver() = %T, want queue.Sync (QUEUE_CONNECTION=sync)", q.Driver())
			}
		})
	}
}

// The default is the configured one, not a hard-coded memory store.
func TestLazyStoreFuncsUseConfiguredDefault(t *testing.T) {
	t.Parallel()

	useDefault := func(*App) (cache.Store, error) { return nil, ErrUseDefault }
	app := dbQueueApp(t, map[string]string{"CACHE_STORE": "database", "SESSION_DRIVER": "database"},
		WithCacheStoreFunc(useDefault),
		WithSessionStoreFunc(func(*App) (session.Store, error) { return nil, ErrUseDefault }),
		WithQueueDriverFunc(func(*App) (queue.Driver, error) { return nil, ErrUseDefault }),
	)

	// Boot creates the database queue tables even with a driver func,
	// so the first Queue() never runs DDL inside a transaction.
	if err := app.Boot(t.Context()); err != nil {
		t.Fatal(err)
	}

	if app.queueDB == nil {
		t.Error("Boot() did not prepare the database queue the store func falls back to")
	}

	c, err := app.Cache()
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := c.Store().(*cache.Database); !ok {
		t.Errorf("Cache().Store() = %T, want *cache.Database (CACHE_STORE=database)", c.Store())
	}

	if _, err := app.Sessions(); err != nil {
		t.Fatal(err)
	}

	if _, ok := app.sessPruner.(*session.SQLStore); !ok {
		t.Errorf("session store = %T, want *session.SQLStore (SESSION_DRIVER=database)", app.sessPruner)
	}

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := q.Driver().(*queue.Database); !ok {
		t.Errorf("Queue().Driver() = %T, want *queue.Database (QUEUE_CONNECTION=database)", q.Driver())
	}
}

// A store func that fails (Redis URL configured but malformed, a client
// that cannot be built) is an error, never a silent fallback to the
// configured default: only ErrUseDefault selects it. DefaultMiddleware
// builds the sessions at boot, so serve does not start.
func TestSecurity_A02_LazyStoreFuncErrorNeverFallsBack(t *testing.T) {
	t.Parallel()

	boom := errors.New("redis: parse url: invalid scheme")
	app := newTestApp(t,
		WithCacheStoreFunc(func(*App) (cache.Store, error) { return nil, boom }),
		WithSessionStoreFunc(func(*App) (session.Store, error) { return nil, boom }),
		WithQueueDriverFunc(func(*App) (queue.Driver, error) { return nil, boom }),
	)

	if c, err := app.Cache(); !errors.Is(err, boom) || c != nil {
		t.Errorf("Cache() = %v, %v, want no cache and %v", c != nil, err, boom)
	}

	if q, err := app.Queue(); !errors.Is(err, boom) || q != nil {
		t.Errorf("Queue() = %v, %v, want no queue and %v", q != nil, err, boom)
	}

	if m, err := app.Sessions(); !errors.Is(err, boom) || m != nil {
		t.Errorf("Sessions() = %v, %v, want no manager and %v", m != nil, err, boom)
	}
}

// Only ErrUseDefault itself, or a single-wrap chain of it, falls back: a
// joined or multi-%w error also carries a real failure and must surface.
func TestSecurity_A02_LazyStoreFuncJoinedErrorDoesNotFallBack(t *testing.T) {
	t.Parallel()

	redisErr := errors.New("redis: dial tcp: connection refused")

	for name, err := range map[string]error{
		"joined":       errors.Join(redisErr, ErrUseDefault),
		"multi %w":     fmt.Errorf("%w; %w", redisErr, ErrUseDefault),
		"wrapped join": fmt.Errorf("cache: %w", errors.Join(ErrUseDefault, redisErr)),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			app := newTestApp(t,
				WithCacheStoreFunc(func(*App) (cache.Store, error) { return nil, err }),
				WithSessionStoreFunc(func(*App) (session.Store, error) { return nil, err }),
				WithQueueDriverFunc(func(*App) (queue.Driver, error) { return nil, err }),
			)

			if _, got := app.Cache(); !errors.Is(got, redisErr) {
				t.Errorf("Cache() error = %v, want %v", got, redisErr)
			}

			if _, got := app.Sessions(); !errors.Is(got, redisErr) {
				t.Errorf("Sessions() error = %v, want %v", got, redisErr)
			}

			if _, got := app.Queue(); !errors.Is(got, redisErr) {
				t.Errorf("Queue() error = %v, want %v", got, redisErr)
			}
		})
	}
}

// The fallback session store is the cookie driver: the cookie carries the
// encrypted data, so another app with the same APP_KEY and no store reads
// it, and it keeps the cookie flags.
func TestLazySessionFuncFallsBackToCookieDriver(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithSessionStoreFunc(func(*App) (session.Store, error) { return nil, ErrUseDefault }))

	m, err := app.Sessions()
	if err != nil {
		t.Fatal(err)
	}

	cookies := m.Cookies(t.Context(), map[string]any{"user": "ana"})
	if len(cookies) == 0 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("Cookies() = %+v, want an HttpOnly SameSite=Lax session cookie", cookies)
	}

	other, err := newTestApp(t).Sessions()
	if err != nil {
		t.Fatal(err)
	}

	if got := other.Peek(t.Context(), cookies)["user"]; got != "ana" {
		t.Errorf("Peek() on another app = %v, want ana (data in the cookie, SESSION_DRIVER=cookie)", got)
	}
}

// A session store func error is a boot error: DefaultMiddleware builds the
// sessions while the routes are registered, so New fails.
func TestLazySessionFuncErrorFailsBoot(t *testing.T) {
	t.Parallel()

	boom := errors.New("redis: parse url: invalid scheme")

	_, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()),
		WithSessionStoreFunc(func(*App) (session.Store, error) { return nil, boom }),
		WithRoutes(func(r *router.Router, app *App) error { return DefaultMiddleware(app, r) }),
	)
	if !errors.Is(err, boom) {
		t.Errorf("New() error = %v, want %v", err, boom)
	}
}

// With QUEUE_CONNECTION=database next to a queue func, Boot resolves the
// func: a driver of its own (Redis) needs no database at boot; only a
// fallback prepares the database queue; a func error fails the boot. The
// func runs once either way.
func TestBootResolvesQueueDriverFunc(t *testing.T) {
	t.Parallel()

	unreachable := "file:" + filepath.Join(testutil.TempDir(t), "missing", "app.db")
	boom := errors.New("redis: parse url: invalid scheme")

	for name, tt := range map[string]struct {
		dbURL   string
		driver  queue.Driver
		err     error
		wantErr error
		wantDB  bool
	}{
		"own driver, database unreachable": {dbURL: unreachable, driver: queue.NewMemory()},
		"fallback":                         {err: ErrUseDefault, wantDB: true},
		"error":                            {err: boom, wantErr: boom},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var calls atomic.Int32

			env := map[string]string{}
			if tt.dbURL != "" {
				env["DATABASE_URL"] = tt.dbURL
			}

			app := dbQueueApp(t, env, WithQueueDriverFunc(func(*App) (queue.Driver, error) {
				calls.Add(1)
				return tt.driver, tt.err
			}))

			if err := app.Boot(t.Context()); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Boot() = %v, want %v", err, tt.wantErr)
			}

			if got := app.queueDB != nil; got != tt.wantDB {
				t.Errorf("Boot() prepared the database queue = %v, want %v", got, tt.wantDB)
			}

			if tt.wantErr != nil {
				return
			}

			q, err := app.Queue()
			if err != nil {
				t.Fatal(err)
			}

			if tt.driver != nil && q.Driver() != tt.driver {
				t.Errorf("Queue().Driver() = %T, want the func's driver", q.Driver())
			}

			if got := calls.Load(); got != 1 {
				t.Errorf("queue func ran %d times, want 1", got)
			}
		})
	}
}

// Concurrent first calls build the lazy store once and share it.
func TestLazyCacheFuncConcurrentFirstUse(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32

	app := newTestApp(t, WithCacheStoreFunc(func(*App) (cache.Store, error) {
		calls.Add(1)
		return nil, ErrUseDefault
	}))

	var (
		wg     sync.WaitGroup
		caches = make([]*cache.Cache, 16)
	)

	for i := range caches {
		wg.Go(func() {
			c, err := app.Cache()
			if err != nil {
				t.Error(err)
			}

			caches[i] = c
		})
	}

	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("cache func ran %d times, want 1", got)
	}

	for _, c := range caches[1:] {
		if c != caches[0] {
			t.Fatal("Cache() returned different caches to concurrent callers")
		}
	}
}

// A fallback in production usually means a missing or misspelled
// REDIS_URL, so it is logged at Warn there and at Info elsewhere.
func TestLazyStoreFallbackLogLevel(t *testing.T) {
	t.Parallel()

	for env, want := range map[string]slog.Level{"production": slog.LevelWarn, "test": slog.LevelInfo} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer

			app := newTestApp(t,
				WithSource(testSource(map[string]string{"APP_ENV": env})),
				WithLogger(slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))),
				WithCacheStoreFunc(func(*App) (cache.Store, error) { return nil, ErrUseDefault }),
			)

			if _, err := app.Cache(); err != nil {
				t.Fatal(err)
			}

			var rec struct {
				Level string `json:"level"`
				Msg   string `json:"msg"`
			}

			for line := range strings.Lines(buf.String()) {
				if err := json.Unmarshal(
					[]byte(line),
					&rec,
				); err == nil &&
					strings.Contains(rec.Msg, "WithCacheStoreFunc") {
					break
				}
			}

			if rec.Level != want.String() {
				t.Errorf("fallback log level = %q, want %q (log %s)", rec.Level, want, buf.String())
			}
		})
	}
}

// A store together with ErrUseDefault is ambiguous: an error, not a guess.
func TestLazyStoreFuncsRejectStoreWithUseDefault(t *testing.T) {
	t.Parallel()

	app := newTestApp(t,
		WithCacheStoreFunc(func(*App) (cache.Store, error) { return cache.NewMemory(1), ErrUseDefault }),
		WithSessionStoreFunc(func(*App) (session.Store, error) { return session.NewMemoryStore(), ErrUseDefault }),
		WithQueueDriverFunc(func(*App) (queue.Driver, error) { return queue.NewMemory(), ErrUseDefault }),
	)

	if _, err := app.Cache(); err == nil {
		t.Error("Cache() = nil error, want an error for a store with ErrUseDefault")
	}

	if _, err := app.Sessions(); err == nil {
		t.Error("Sessions() = nil error, want an error for a store with ErrUseDefault")
	}

	if _, err := app.Queue(); err == nil {
		t.Error("Queue() = nil error, want an error for a driver with ErrUseDefault")
	}
}

func TestStoreOptionsLastWins(t *testing.T) {
	t.Parallel()

	eager := cache.NewMemory(10)
	app := newTestApp(t,
		WithCacheStoreFunc(func(*App) (cache.Store, error) { return nil, errors.New("replaced") }),
		WithCacheStore(eager),
	)

	if _, err := app.Cache(); err != nil {
		t.Fatalf("Cache() = %v, want the later WithCacheStore to win", err)
	}
}

// flagSeeder records its runs and the flag values it saw.
type flagSeeder struct {
	name    string
	count   bool // defines --count as well as --fresh
	defines string
	log     *[]string
	fresh   bool
	n       int
}

func (s *flagSeeder) Name() string { return s.name }

func (s *flagSeeder) SeedFlags(fs *flag.FlagSet) {
	fs.BoolVar(&s.fresh, "fresh", false, "drop the demo rows first")

	if s.count {
		fs.IntVar(&s.n, "count", 10, "rows to create")
	}

	if s.defines != "" {
		fs.Bool(s.defines, false, "shadows a db:seed flag")
	}
}

func (s *flagSeeder) SeedApp(context.Context, *App) error {
	*s.log = append(*s.log, fmt.Sprintf("%s fresh=%v n=%d", s.name, s.fresh, s.n))
	return nil
}

func TestSeedCommandSelectsSeedersAndPassesFlags(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     string
		args    []string
		want    []string
		wantErr string // substring of the error or output; "" means success
	}{
		{
			name: "all in order",
			args: nil,
			want: []string{"DemoCatalog fresh=false n=10", "Users fresh=false n=0", "plain"},
		},
		{
			name: "positional name and flags", args: []string{"DemoCatalog", "--fresh", "--count", "3"},
			want: []string{"DemoCatalog fresh=true n=3"},
		},
		{
			name: "class flag",
			args: []string{"--class", "DemoCatalog", "--count=5"},
			want: []string{"DemoCatalog fresh=false n=5"},
		},
		{
			name: "class then seeder flag",
			args: []string{"--class", "DemoCatalog", "--fresh"},
			want: []string{"DemoCatalog fresh=true n=10"},
		},
		{
			name: "class among seeder flags",
			env:  "production",
			args: []string{"--force", "--count", "2", "--class", "DemoCatalog"},
			want: []string{"DemoCatalog fresh=false n=2"},
		},
		{
			name: "class twice",
			args: []string{"--class=Users", "-class", "DemoCatalog"},
			want: []string{"Users fresh=false n=0", "DemoCatalog fresh=false n=10"},
		},
		{
			name: "argument order wins",
			args: []string{"Users", "DemoCatalog"},
			want: []string{"Users fresh=false n=0", "DemoCatalog fresh=false n=10"},
		},
		{
			name: "case and Seeder suffix",
			args: []string{"users", "plain"},
			want: []string{"Users fresh=false n=0", "plain"},
		},
		{
			name: "duplicates run once",
			args: []string{"Users", "--class", "users"},
			want: []string{"Users fresh=false n=0"},
		},
		{
			name: "shared flag reaches every seeder",
			args: []string{"--fresh"},
			want: []string{"DemoCatalog fresh=true n=10", "Users fresh=true n=0", "plain"},
		},
		{name: "unknown seeder", args: []string{"Nope"}, wantErr: `unknown seeder "Nope"`},
		{
			name:    "unknown flag",
			args:    []string{"DemoCatalog", "--bogus"},
			wantErr: "flag provided but not defined: -bogus",
		},
		{
			name:    "flag of an unselected seeder",
			args:    []string{"Users", "--count", "3"},
			wantErr: "flag provided but not defined: -count",
		},
		{
			name:    "stray argument",
			args:    []string{"DemoCatalog", "--fresh", "extra"},
			wantErr: `unexpected argument "extra"`,
		},
		{
			name:    "production without force",
			env:     "production",
			args:    []string{"DemoCatalog", "--fresh"},
			wantErr: "without --force",
		},
		{
			name: "production force after the name",
			env:  "production",
			args: []string{"DemoCatalog", "--fresh", "--force"},
			want: []string{"DemoCatalog fresh=true n=10"},
		},
		{
			name: "production force first",
			env:  "production",
			args: []string{"--force", "Users"},
			want: []string{"Users fresh=false n=0"},
		},
		{name: "help", args: []string{"DemoCatalog", "-h"}, wantErr: "-count"},
		{
			name:    "force is never a seeder flag value",
			env:     "production",
			args:    []string{"DemoCatalog", "--count", "--force"},
			wantErr: "flag needs an argument: -count",
		},
		{name: "class without a name", args: []string{"--class"}, wantErr: "-class needs a seeder name"},
		{
			name:    "class without a name in production",
			env:     "production",
			args:    []string{"--force", "--class"},
			wantErr: "-class needs a seeder name",
		},
		{
			name:    "empty class",
			env:     "production",
			args:    []string{"--force", "--class="},
			wantErr: "-class needs a seeder name",
		},
		{
			name:    "class without a name after flags",
			args:    []string{"--fresh", "--class"},
			wantErr: "-class needs a seeder name",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var (
				log []string
				out bytes.Buffer
			)

			env := tt.env
			if env == "" {
				env = "test"
			}
			getenv := func(k string) (string, bool) {
				v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": env, "DATABASE_URL": ":memory:"}[k]
				return v, ok
			}

			plain := NamedAppSeeder("plainSeeder", func(context.Context, *App) error {
				log = append(log, "plain")
				return nil
			})

			build := func(opts ...Option) (*App, error) {
				return New(append(opts, WithLogger(discardLogger()), WithStdout(&out), WithAppSeeders(
					&flagSeeder{name: "DemoCatalog", count: true, log: &log},
					&flagSeeder{name: "Users", log: &log},
					plain,
				))...)
			}

			err := RunMain(t.Context(), append([]string{"app", "db:seed"}, tt.args...), getenv, build)

			if tt.wantErr != "" {
				if got := fmt.Sprint(
					err,
				) + out.String(); (err == nil && tt.name != "help") ||
					!strings.Contains(got, tt.wantErr) {
					t.Fatalf("db:seed %v = %v, output %q, want %q", tt.args, err, out.String(), tt.wantErr)
				}

				if len(log) != 0 {
					t.Fatalf("db:seed %v ran %v after failing", tt.args, log)
				}

				return
			}

			if err != nil {
				t.Fatalf("db:seed %v = %v, output %q", tt.args, err, out.String())
			}

			if diff := cmp.Diff(tt.want, log); diff != "" {
				t.Errorf("db:seed %v runs (-want +got):\n%s", tt.args, diff)
			}
		})
	}
}

func TestSecurity_A02_SeederFlagsCannotShadowForce(t *testing.T) {
	t.Parallel()

	for _, reserved := range []string{"force", "class", "help", "h"} {
		t.Run(reserved, func(t *testing.T) {
			t.Parallel()

			var log []string

			getenv := func(k string) (string, bool) {
				v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": "production", "DATABASE_URL": ":memory:"}[k]
				return v, ok
			}

			build := func(opts ...Option) (*App, error) {
				return New(append(opts, WithLogger(discardLogger()), WithStdout(io.Discard),
					WithAppSeeders(&flagSeeder{name: "Sneaky", defines: reserved, log: &log}))...)
			}

			err := RunMain(t.Context(), []string{"app", "db:seed", "--" + reserved}, getenv, build)
			if err == nil || len(log) != 0 {
				t.Fatalf(
					"db:seed with a seeder defining --%s = %v, ran %v; want an error and no run",
					reserved,
					err,
					log,
				)
			}
		})
	}
}

// uncomparableSeeder is a value seeder that == would panic on.
type uncomparableSeeder struct {
	tags []string
	log  *[]string
}

func (s uncomparableSeeder) Name() string { return "Tags" }

func (s uncomparableSeeder) SeedApp(context.Context, *App) error {
	*s.log = append(*s.log, "tags")
	return nil
}

type valueFlagSeeder struct{}

func (valueFlagSeeder) Name() string { return "ValueFlags" }

func (s valueFlagSeeder) SeedFlags(fs *flag.FlagSet) { fs.Bool("fresh", false, "") }

func (valueFlagSeeder) SeedApp(context.Context, *App) error { return nil }

func TestSeedCommandSeederKinds(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, seeder AppSeeder, args ...string) (string, error) {
		t.Helper()

		var out bytes.Buffer

		getenv := func(k string) (string, bool) {
			v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": "test", "DATABASE_URL": ":memory:"}[k]
			return v, ok
		}

		build := func(opts ...Option) (*App, error) {
			return New(append(opts, WithLogger(discardLogger()), WithStdout(&out), WithAppSeeders(seeder))...)
		}

		err := RunMain(t.Context(), append([]string{"app", "db:seed"}, args...), getenv, build)

		return out.String(), err
	}

	t.Run("uncomparable value seeder named twice", func(t *testing.T) {
		t.Parallel()

		var log []string

		if out, err := run(t, uncomparableSeeder{tags: []string{"a"}, log: &log}, "Tags", "tags"); err != nil {
			t.Fatalf("db:seed Tags tags = %v, output %q", err, out)
		}

		if diff := cmp.Diff([]string{"tags"}, log); diff != "" {
			t.Errorf("runs (-want +got):\n%s", diff)
		}
	})

	t.Run("value receiver with flags", func(t *testing.T) {
		t.Parallel()

		if _, err := run(t, valueFlagSeeder{}); err == nil || !strings.Contains(err.Error(), "pointer") {
			t.Fatalf("db:seed with a value FlagSeeder = %v, want a pointer receiver error", err)
		}
	})
}

func runDBRun(t *testing.T, app *App, stdin string, args ...string) error {
	t.Helper()

	cmd := dbRunCommand()
	fset := flag.NewFlagSet("db:run", flag.ContinueOnError)
	cmd.Flags(fset)

	if err := fset.Parse(args); err != nil {
		t.Fatal(err)
	}

	app.stdin = strings.NewReader(stdin)

	return cmd.Run(t.Context(), app, fset.Args())
}

func TestDBRunIsADryRunUnlessCommitted(t *testing.T) {
	t.Parallel()

	var out bytes.Buffer

	// Its own database file: the default one is ./storage/database.sqlite, shared by every test of the package.
	dbFile := "file:" + filepath.Join(t.TempDir(), "run.db")
	app := newTestApp(t, WithStdout(&out), WithSource(testSource(map[string]string{"DATABASE_URL": dbFile})))

	// Registered after TempDir, so it runs first: Windows cannot delete a database file that is still open.
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	table := "run_probe"

	count := func() int {
		t.Helper()

		var n int
		if err := db.QueryRowContext(t.Context(), `SELECT count(*) FROM `+table).Scan(&n); err != nil {
			t.Fatal(err)
		}

		return n
	}

	if _, err := db.ExecContext(t.Context(), `CREATE TABLE `+table+` (n INTEGER)`); err != nil {
		t.Fatal(err)
	}

	script := "INSERT INTO " + table + " VALUES (1);\nINSERT INTO " + table + " VALUES (2);\n"

	if err := runDBRun(t, app, script, "-file", "-"); err != nil || count() != 0 {
		t.Fatalf("dry run: err %v, rows %d, want nil and 0 (rolled back)", err, count())
	}

	if !strings.Contains(out.String(), "dry run, rolled back") {
		t.Fatalf("output = %q, want the dry run notice", out.String())
	}

	if err := runDBRun(t, app, script, "-file", "-", "-commit"); err != nil || count() != 2 {
		t.Fatalf("commit: err %v, rows %d, want nil and 2", err, count())
	}

	// A failing statement rolls the whole script back, committed or not.
	bad := "INSERT INTO " + table + " VALUES (3);\nINSERT INTO nope VALUES (1);\n"
	if err := runDBRun(t, app, bad, "-file", "-", "-commit"); err == nil || count() != 2 {
		t.Fatalf("failing script: err %v, rows %d, want an error and 2 (nothing from the script kept)", err, count())
	}
}

func runDBBackup(t *testing.T, app *App, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	app.stdout = &out
	cmd := dbBackupCommand()
	fset := flag.NewFlagSet("db:backup", flag.ContinueOnError)
	cmd.Flags(fset)

	if err := fset.Parse(args); err != nil {
		t.Fatal(err)
	}

	err := cmd.Run(t.Context(), app, fset.Args())

	return out.String(), err
}

func TestDBBackupSnapshotsAndRestores(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	live := filepath.Join(dir, "live.sqlite")
	app := newTestApp(t, WithSource(testSource(map[string]string{
		"DATABASE_URL": "file:" + filepath.ToSlash(live),
	})))
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(t.Context(), `CREATE TABLE notes (body TEXT)`); err != nil {
		t.Fatal(err)
	}

	if _, err := db.ExecContext(t.Context(), `INSERT INTO notes VALUES ('committed')`); err != nil {
		t.Fatal(err)
	}

	snap := filepath.Join(dir, "snap.sqlite")
	out, err := runDBBackup(t, app, "-to", snap)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "wrote") || strings.Contains(out, "committed") {
		t.Fatalf("stdout = %q, want the path and not the row", out)
	}

	assertSQLiteMode(t, snap)

	if got := sqliteNote(t, snap); got != "committed" {
		t.Fatalf("snapshot note = %q, want committed", got)
	}

	kept := []byte("keep")
	if err := os.WriteFile(snap+".exists", kept, 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := runDBBackup(t, app, "-to", snap+".exists"); err == nil {
		t.Fatal("backup overwrote an existing file")
	}

	if got, err := os.ReadFile(snap + ".exists"); err != nil || string(got) != "keep" {
		t.Fatalf("existing file = %q, %v", got, err)
	}

	link := filepath.Join(dir, "snap.link")
	if err := os.Symlink(snap, link); err != nil {
		t.Logf("symlink: %v", err)
	} else if _, err := runDBBackup(t, app, "-to", link); err == nil {
		t.Fatal("backup followed a symlink")
	}

	if _, err := runDBBackup(t, app, "-to", live); err == nil {
		t.Fatal("backup accepted the live database as -to")
	}

	sameDir := filepath.Join(dir, "restored.sqlite")
	if _, err := runDBBackup(t, app, "-restore", "-from", snap, "-to", sameDir); err == nil {
		t.Fatal("restore accepted the snapshot's directory")
	}

	other := t.TempDir()
	restored := filepath.Join(other, "restored.sqlite")
	out, err = runDBBackup(t, app, "-restore", "-from", snap, "-to", restored)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "restored") {
		t.Fatalf("stdout = %q, want restored", out)
	}

	assertSQLiteMode(t, restored)

	if got := sqliteNote(t, restored); got != "committed" {
		t.Fatalf("restored note = %q, want committed", got)
	}
}

func TestDBBackupRefusesNonSQLite(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithSource(testSource(map[string]string{
		"DATABASE_URL": "postgres://app@localhost/app",
	})))

	_, err := runDBBackup(t, app, "-to", filepath.Join(t.TempDir(), "x.sqlite"))
	if err == nil || !strings.Contains(err.Error(), "only SQLite") {
		t.Fatalf("postgres backup = %v, want only SQLite", err)
	}
}

func assertSQLiteMode(t *testing.T, path string) {
	t.Helper()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	perm := info.Mode().Perm()
	if runtime.GOOS == "windows" {
		if perm&0o200 == 0 {
			t.Fatalf("%s mode %#o, want the file writable", path, perm)
		}

		return
	}

	if perm != 0o600 {
		t.Fatalf("%s mode %#o, want 0600", path, perm)
	}
}

func sqliteNote(t *testing.T, path string) string {
	t.Helper()

	db, err := sql.Open("sqlite", "file:"+filepath.ToSlash(path)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	var body string
	if err := db.QueryRowContext(t.Context(), `SELECT body FROM notes`).Scan(&body); err != nil {
		t.Fatal(err)
	}

	return body
}

func TestDBRunValidatesItsInput(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	for name, tc := range map[string]struct {
		stdin string
		args  []string
	}{
		"no file":   {args: nil},
		"extra arg": {stdin: "SELECT 1", args: []string{"-file", "-", "SELECT 2"}},
		"empty":     {stdin: " \n", args: []string{"-file", "-"}},
		"missing":   {args: []string{"-file", filepath.Join(t.TempDir(), "nope.sql")}},
		"too large": {stdin: strings.Repeat("-", maxRunSQLBytes+1), args: []string{"-file", "-"}},
	} {
		if err := runDBRun(t, app, tc.stdin, tc.args...); err == nil {
			t.Errorf("%s: db:run accepted the input", name)
		}
	}
}

func TestDBRunRefusesTransactionControl(t *testing.T) {
	t.Parallel()

	app := newTestApp(
		t,
		WithSource(testSource(map[string]string{"DATABASE_URL": "file:" + filepath.Join(t.TempDir(), "tx.db")})),
	)
	t.Cleanup(func() { _ = app.Shutdown(context.Background()) })

	for _, script := range []string{
		"SELECT 1;\nCOMMIT;",
		"begin;\nSELECT 1;",
		"  Rollback;",
		"SELECT 1;\nSTART  TRANSACTION;",
	} {
		err := runDBRun(t, app, script, "-file", "-", "-commit")
		if err == nil || !strings.Contains(err.Error(), "owns the transaction") {
			t.Errorf("db:run %q = %v, want the transaction-control refusal", script, err)
		}
	}

	// Words that only start with a keyword are not transaction control.
	if txControl.MatchString("SELECT beginning FROM t;\nSELECT endpoint FROM t;") {
		t.Error("a column named like a keyword was taken for transaction control")
	}
}

func TestDBRunDDLPatternForMySQL(t *testing.T) {
	t.Parallel()

	for script, want := range map[string]bool{
		"ALTER TABLE t ADD c int;":           true,
		"UPDATE t SET a = 1;\ndrop table x;": true,
		"UPDATE t SET created_at = now();":   false,
		"INSERT INTO altered VALUES (1);":    false,
	} {
		if got := implicitCommit.MatchString(script); got != want {
			t.Errorf("implicitCommit(%q) = %v, want %v", script, got, want)
		}
	}
}

func TestMigrateExplainsQueueTableClash(t *testing.T) {
	t.Parallel()

	jobs := []schema.Migration{{
		ID: "20260101000000_create_jobs",
		Up: func(s *schema.Schema) error {
			return s.Create("jobs", func(t *schema.Table) { t.ID(); t.String("title") })
		},
		Down: func(s *schema.Schema) error { return s.Drop("jobs") },
	}}

	migrate := func(env map[string]string) (string, error) {
		var out bytes.Buffer

		env["APP_KEY"], env["APP_ENV"] = testKey, "test"
		env["DATABASE_URL"] = "file:" + filepath.Join(testutil.TempDir(t), "app.db")

		getenv := func(k string) (string, bool) { v, ok := env[k]; return v, ok }
		build := func(opts ...Option) (*App, error) {
			return New(append(opts, WithLogger(discardLogger()), WithStdout(&out), WithMigrations(jobs))...)
		}

		err := RunMain(t.Context(), []string{"app", "migrate"}, getenv, build)

		return out.String(), err
	}

	_, err := migrate(map[string]string{"QUEUE_CONNECTION": "database"})
	if err == nil || !strings.Contains(err.Error(), "QUEUE_TABLE") || !strings.Contains(err.Error(), "already exists") {
		t.Errorf("migrate with a jobs table and the database queue = %v, want an error naming QUEUE_TABLE", err)
	}

	if out, err := migrate(
		map[string]string{"QUEUE_CONNECTION": "database", "QUEUE_TABLE": "queue_jobs"},
	); err != nil ||
		!strings.Contains(out, "Migrated: 20260101000000_create_jobs") {
		t.Errorf("migrate with QUEUE_TABLE=queue_jobs = %q, %v, want it to succeed", out, err)
	}

	if _, err := migrate(map[string]string{}); err != nil {
		t.Errorf("migrate with the sync queue = %v, want nil", err)
	}
}

func anyBinary(name string) (string, error) { return name, nil }

func TestDBConsoleFor(t *testing.T) {
	t.Parallel()

	sqlitePath, err := filepath.Abs("storage/app.db")
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		url     string
		lookup  func(string) (string, error)
		want    dbConsole
		wantErr string
	}{
		{
			"sqlite file",
			"file:storage/app.db?cache=shared",
			anyBinary,
			dbConsole{Bin: "sqlite3", Args: []string{sqlitePath}},
			"",
		},
		{
			"sqlite scheme",
			"sqlite:storage/app.db",
			anyBinary,
			dbConsole{Bin: "sqlite3", Args: []string{sqlitePath}},
			"",
		},
		{
			"postgres full", "postgres://app:p%40ss@db.internal:5433/shop?sslmode=require", anyBinary,
			dbConsole{
				Bin:  "psql",
				Args: []string{"-h", "db.internal", "-p", "5433", "-U", "app", "-d", "shop"},
				Env:  []string{"PGPASSWORD=p@ss", "PGSSLMODE=require"},
			},
			"",
		},
		{
			"postgresql without port or password", "postgresql://app@localhost/shop", anyBinary,
			dbConsole{Bin: "psql", Args: []string{"-h", "localhost", "-U", "app", "-d", "shop"}},
			"",
		},
		{"in memory", ":memory:", anyBinary, dbConsole{}, "an in-memory database has no file to open"},
		{"in memory mode", "file:x?mode=memory", anyBinary, dbConsole{}, "an in-memory database has no file to open"},
		{"no scheme", "app:secret@db.internal/shop", anyBinary, dbConsole{}, "no URL scheme"},
		{
			"unknown scheme",
			"redis://u:secret@cache.internal/0",
			anyBinary,
			dbConsole{},
			`no console client for "redis" URLs`,
		},
		{
			"mysql is not wired",
			"mysql://app:secret@db.internal/app",
			anyBinary,
			dbConsole{},
			`no console client for "mysql" URLs`,
		},
		{
			"mariadb is not wired",
			"mariadb://app:secret@db.internal/app",
			anyBinary,
			dbConsole{},
			`no console client for "mariadb" URLs`,
		},
		{
			"database name that psql would parse as a connection string", "postgres://app@db.internal/x%20host%3Devil",
			anyBinary,
			dbConsole{},
			"database name",
		},
		{
			"missing binary", "postgres://app:secret@db.internal/shop",
			func(string) (string, error) { return "", errors.New("not found") },
			dbConsole{},
			"psql is not installed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got, err := consoleFor(tt.url, tt.lookup)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want %q", err, tt.wantErr)
				}

				return
			}

			if err != nil {
				t.Fatal(err)
			}

			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Fatalf("console mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSecurity_A02_DBConsoleKeepsPasswordOutOfArgv(t *testing.T) {
	t.Parallel()

	got, err := consoleFor("postgres://app:hunter2@db.internal:5432/shop", anyBinary)
	if err != nil {
		t.Fatal(err)
	}

	for _, a := range got.Args {
		if strings.Contains(a, "hunter2") {
			t.Fatalf("the password is in argv: %v", got.Args)
		}
	}

	if !slices.Contains(got.Env, "PGPASSWORD=hunter2") {
		t.Fatalf("env = %v, want the password in PGPASSWORD", got.Env)
	}

	for _, raw := range []string{
		"postgres://app:hunter2@db.internal:5432/shop",
		"mysql://app:hunter2@db.internal:3306/shop",
		"redis://app:hunter2@db.internal:6379/0",
	} {
		_, err := consoleFor(raw, func(string) (string, error) { return "", errors.New("not found") })
		if err == nil {
			t.Fatalf("consoleFor(%q) succeeded without the binary", raw)
		}

		for _, secret := range []string{"hunter2", "app", "db.internal"} {
			if strings.Contains(err.Error(), secret) {
				t.Errorf("the error for %q leaks %q: %v", raw, secret, err)
			}
		}
	}
}

func runDBConsole(t *testing.T, env string, dbURL string, args ...string) (string, error) {
	t.Helper()

	var out bytes.Buffer

	getenv := func(k string) (string, bool) {
		v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": env, "DATABASE_URL": dbURL}[k]
		return v, ok
	}

	build := func(opts ...Option) (*App, error) {
		return New(append(opts, WithLogger(discardLogger()), WithStdout(&out))...)
	}

	err := RunMain(t.Context(), append([]string{"app", "db"}, args...), getenv, build)

	return out.String(), err
}

func TestDBConsolePrint(t *testing.T) {
	t.Parallel()

	file := filepath.Join(testutil.TempDir(t), "console.db")

	out, err := runDBConsole(t, "test", "file:"+file, "-print")
	if err != nil {
		t.Fatal(err)
	}

	abs, err := filepath.Abs(file)
	if err != nil {
		t.Fatal(err)
	}

	if want := "sqlite3 " + abs + "\n"; out != want {
		t.Fatalf("-print = %q, want %q", out, want)
	}

	out, err = runDBConsole(t, "test", "postgres://app:hunter2@db.internal/shop", "-print")
	if err != nil || strings.Contains(out, "hunter2") || !strings.HasPrefix(out, "psql -h db.internal") {
		t.Fatalf("postgres -print = %q, %v", out, err)
	}
}

func TestSecurity_A05_DBConsoleNeedsForceInProduction(t *testing.T) {
	t.Parallel()

	file := "file:" + filepath.Join(testutil.TempDir(t), "console.db")

	if _, err := runDBConsole(t, "production", file, "-print"); !errors.Is(err, errForceRequired) {
		t.Fatalf("db in production = %v, want errForceRequired", err)
	}

	if _, err := runDBConsole(t, "production", file, "-print", "-force"); err != nil {
		t.Fatalf("db -force in production = %v", err)
	}
}

// ------------------------------------------------------------------ migrate:check, db:pull, migrate:diff

type appRun struct {
	dbURL      string
	env        string
	migrations []schema.Migration
	models     []orm.ModelInfo
	clock      func() time.Time
}

func (a appRun) run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer

	env := a.env
	if env == "" {
		env = "test"
	}

	getenv := func(k string) (string, bool) {
		v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": env, "DATABASE_URL": a.dbURL}[k]
		return v, ok
	}

	build := func(opts ...Option) (*App, error) {
		if a.clock != nil {
			opts = append(opts, WithClock(a.clock))
		}

		return New(append(opts, WithLogger(discardLogger()), WithStdout(&out), WithStderr(&errOut),
			WithMigrations(a.migrations), WithModels(a.models...))...)
	}

	err = RunMain(t.Context(), append([]string{"app"}, args...), getenv, build)

	return out.String(), errOut.String(), err
}

func newAppRun(t *testing.T, migs ...schema.Migration) appRun {
	t.Helper()

	return appRun{dbURL: "file:" + filepath.Join(testutil.TempDir(t), "app.db"), migrations: migs}
}

func createT(id, table string, cols func(t *schema.Table)) schema.Migration {
	return schema.Migration{ID: id, Up: func(s *schema.Schema) error { return s.Create(table, cols) }}
}

func addCol(id, col string) schema.Migration {
	return schema.Migration{ID: id, Up: func(s *schema.Schema) error {
		return s.Table("posts", func(t *schema.Table) { t.String(col).Nullable() })
	}}
}

func TestMigrateCheck(t *testing.T) {
	t.Parallel()

	base := createT("20260101000000_create_posts", "posts", func(t *schema.Table) { t.ID(); t.String("title") })

	a := newAppRun(t, base, addCol("20260102000000_add_slug", "slug"))
	if out, _, err := a.run(t, "migrate:check"); err != nil || !strings.Contains(out, "No conflicts.") {
		t.Fatalf("clean check = %q, %v", out, err)
	}

	// Two branches added the same column.
	a = newAppRun(
		t,
		base,
		addCol("20260102000000_add_slug", "slug"),
		addCol("20260103000000_add_slug_again", "slug"),
	)

	out, _, err := a.run(t, "migrate:check")
	if err == nil ||
		!strings.Contains(
			out,
			"conflict  20260103000000_add_slug_again  posts.slug  column already added by 20260102000000_add_slug",
		) {
		t.Fatalf("conflicting check = %q, %v", out, err)
	}

	jsonOut, _, err := a.run(t, "migrate:check", "-json")
	if err == nil || !strings.Contains(jsonOut, `"kind":"conflict"`) {
		t.Errorf("check -json = %q, %v", jsonOut, err)
	}

	// A pending migration older than the last one that ran.
	late := addCol("20260301000000_late", "late")
	a = newAppRun(t, base, late)

	if _, _, err := a.run(t, "migrate"); err != nil {
		t.Fatal(err)
	}

	a.migrations = []schema.Migration{base, addCol("20260201000000_other_branch", "other"), late}

	out, _, err = a.run(t, "migrate:check")
	if err == nil ||
		!strings.Contains(out, "order     20260201000000_other_branch  pending, but 20260301000000_late already ran") {
		t.Errorf("out of order check = %q, %v", out, err)
	}

	if out, _, _ := a.run(t, "migrate:status"); !strings.Contains(out, "Pending (out of order)") {
		t.Errorf("status = %q, want the out of order mark", out)
	}
}

func TestSecurity_A05_MigrateRefusesDestructiveInProduction(t *testing.T) {
	t.Parallel()

	a := newAppRun(t,
		createT("20260101000000_create_posts", "posts", func(t *schema.Table) { t.ID() }),
		schema.Migration{ID: "20260102000000_drop_posts", Up: func(s *schema.Schema) error { return s.Drop("posts") }},
	)
	a.env = "production"

	if _, _, err := a.run(t, "migrate"); !errors.Is(err, errDestructiveMigration) {
		t.Fatalf("migrate in production = %v, want the destructive refusal", err)
	}

	if out, _, err := a.run(t, "migrate", "-allow-destructive"); err != nil || !strings.Contains(out, "Migrated:") {
		t.Errorf("migrate --allow-destructive = %q, %v", out, err)
	}

	// Outside production nothing is refused.
	b := newAppRun(t, a.migrations...)
	if _, _, err := b.run(t, "migrate"); err != nil {
		t.Errorf("migrate in test = %v", err)
	}
}

type pullUser struct {
	ID    int64 `db:"id"`
	Email string
}

func (pullUser) Table() string { return "users" }

func pullDB(t *testing.T) (appRun, *database.DB) {
	t.Helper()

	a := newAppRun(t)

	db, err := database.Open(t.Context(), a.dbURL)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	s := schema.New(t.Context(), db)

	for _, err := range []error{
		s.Create("users", func(t *schema.Table) {
			t.ID()
			t.String("email", 120).Unique()
			t.Boolean("active").Default(true)
			t.Timestamps()
		}),
		s.Create("posts", func(t *schema.Table) {
			t.ID()
			t.ForeignID("user_id").Constrained("users").CascadeOnDelete()
			t.String("title")
			t.Text("body").Nullable()
			t.Index("title")
		}),
		s.Create("tags", func(t *schema.Table) {
			t.ID()
			t.String("label").Unique()
		}),
		s.Create("post_tag", func(t *schema.Table) {
			t.ForeignID("post_id").Constrained("posts").CascadeOnDelete()
			t.ForeignID("tag_id").Constrained("tags").CascadeOnDelete()
			t.Primary("post_id", "tag_id")
		}),
		// The framework owns this one; db:pull leaves it out.
		s.Create("jobs", func(t *schema.Table) { t.ID() }),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}

	return a, db
}

func TestDBPullBaselineRoundTrips(t *testing.T) {
	t.Parallel()

	a, db := pullDB(t)

	snap, err := schema.Inspect(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}

	snap.Tables = slices.DeleteFunc(
		snap.Tables,
		func(tb schema.TableInfo) bool { return slices.Contains(frameworkTables(), tb.Name) },
	)

	src, warnings, err := baselineGo("migrations", "20261201000000_baseline", snap)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("baselineGo = %v, warnings %v", err, warnings)
	}

	golden := filepath.Join("testdata", "dbpull", "baseline.golden")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(golden, src, 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(want), string(src)); diff != "" {
		t.Errorf("baseline mismatch (-want +got):\n%s", diff)
	}

	// Round trip: the same specs, applied through the builder on an empty
	// database, give back the schema they were read from.
	_ = a

	replay, err := replayBaseline(t, snap)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(snap, replay); diff != "" {
		t.Errorf("snapshot after the baseline (-original +replayed):\n%s", diff)
	}
}

// replayBaseline runs baselineMigration on an empty database and inspects it.
func replayBaseline(t *testing.T, snap schema.Snapshot) (schema.Snapshot, error) {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		return schema.Snapshot{}, err
	}

	t.Cleanup(func() { _ = db.Close() })

	if err := baselineMigration("replay", snap).Up(schema.New(t.Context(), db)); err != nil {
		return schema.Snapshot{}, err
	}

	return schema.Inspect(t.Context(), db)
}

func TestDBPullAndMigrateDiffNeverOverwriteAMigration(t *testing.T) {
	a, _ := pullDB(t)
	a.clock = func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }
	dir := filepath.Join(t.TempDir(), "m")
	path := filepath.Join(dir, "20260102030405_baseline.go")

	if _, _, err := a.run(t, "db:pull", "-o", dir); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte("package m // edited\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := a.run(t, "db:pull", "-o", dir); !errors.Is(err, os.ErrExist) {
		t.Errorf("second db:pull = %v, want it to refuse to overwrite", err)
	}

	if b, _ := os.ReadFile(path); !strings.Contains(string(b), "// edited") {
		t.Error("db:pull overwrote an existing migration")
	}

	d := diffSetup(t, nil)
	d.clock = a.clock

	diffPath := filepath.Join(dir, "20260102030405_sync_models.go")
	if _, _, err := d.run(t, "migrate:diff", "-o", dir, "-delete", "people.nickname"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := d.run(t, "migrate:diff", "-o", dir, "-delete", "people.nickname"); !errors.Is(err, os.ErrExist) {
		t.Errorf("second migrate:diff = %v, want it to refuse to overwrite (%s)", err, diffPath)
	}
}

func TestDBPullRefusesNamesThatAreNotIdentifiers(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct{ name, ddl, want string }{
		{"column", "CREATE TABLE ok (id INTEGER PRIMARY KEY, \"a`;b\" TEXT)", "a`;b"},
		{"table", "CREATE TABLE \"x\ny\" (id INTEGER PRIMARY KEY)", "x\\ny"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			a, db := pullDB(t)
			if _, err := db.ExecContext(t.Context(), tt.ddl); err != nil {
				t.Fatal(err)
			}

			dir := filepath.Join(t.TempDir(), "m")

			_, _, err := a.run(t, "db:pull", "-o", dir, "-models")
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("db:pull = %v, want an error naming %q", err, tt.want)
			}

			if _, statErr := os.Stat(dir); statErr == nil {
				t.Error("db:pull wrote files before refusing")
			}
		})
	}
}

func TestDBPullKnownLimitsAndFiles(t *testing.T) {
	t.Parallel()

	_, db := pullDB(t)

	if _, err := db.ExecContext(
		t.Context(),
		`CREATE TABLE odd (id INTEGER PRIMARY KEY AUTOINCREMENT, shape GEOMETRY)`,
	); err != nil {
		t.Fatal(err)
	}

	snap, err := schema.Inspect(t.Context(), db)
	if err != nil {
		t.Fatal(err)
	}

	odd, _ := snap.Table("odd")

	src, warnings, err := baselineGo(
		"migrations",
		"20261201000000_baseline",
		schema.Snapshot{Tables: []schema.TableInfo{odd}},
	)
	if err != nil || len(warnings) != 1 ||
		!strings.Contains(string(src), `db:pull: column "shape" has type "geometry"`) {
		t.Errorf("unknown type: warnings %v, err %v, src %s", warnings, err, src)
	}

	for in, want := range map[string]string{"users": "user", "categories": "category", "addresses": "address"} {
		if got := singularTable(in); got != want {
			t.Errorf("singularTable(%q) = %q, want %q", in, got, want)
		}
	}

	users, _ := snap.Table("users")

	model, err := modelGo("models", users)
	if err != nil || !strings.Contains(string(model), "type User struct") ||
		!strings.Contains(string(model), "orm.Model") {
		t.Errorf("modelGo = %s, %v", model, err)
	}
}

func TestDBPullCommandWritesFilesAndKeepsExistingModels(t *testing.T) {
	a, _ := pullDB(t)

	dir := t.TempDir()

	t.Chdir(dir)

	out, _, err := a.run(t, "db:pull", "-o", filepath.Join(dir, "database", "migrations"), "-models", "-table", "users")
	if err != nil || !strings.Contains(out, "Register it: add m") {
		t.Fatalf("db:pull = %q, %v", out, err)
	}

	model := filepath.Join(dir, "app", "models", "user.go")
	if _, err := os.Stat(model); err != nil {
		t.Fatalf("the model was not written: %v", err)
	}

	if err := os.WriteFile(model, []byte("package models\n// mine\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := a.run(
		t,
		"db:pull",
		"-o",
		filepath.Join(dir, "database", "again"),
		"-models",
		"-table",
		"users",
	); err != nil {
		t.Fatal(err)
	}

	if b, _ := os.ReadFile(model); !strings.Contains(string(b), "// mine") {
		t.Error("db:pull -models overwrote an existing model")
	}
}

func TestDBPullMarkApplied(t *testing.T) {
	const id = "20260102030405_baseline"

	clock := func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) }

	t.Run("not registered", func(t *testing.T) {
		a, _ := pullDB(t)
		a.clock = clock

		out, _, err := a.run(t, "db:pull", "-o", filepath.Join(t.TempDir(), "m"), "-mark-applied")
		if err != nil || !strings.Contains(out, "Not registered yet") {
			t.Fatalf("db:pull = %q, %v", out, err)
		}
	})

	t.Run("registered, then again", func(t *testing.T) {
		a, _ := pullDB(t)
		a.clock = clock
		a.migrations = []schema.Migration{{ID: id, Up: func(*schema.Schema) error { return nil }}}

		args := func() []string {
			return []string{"db:pull", "-o", filepath.Join(t.TempDir(), "m"), "-mark-applied"}
		}

		out, _, err := a.run(t, args()...)
		if err != nil || !strings.Contains(out, "Marked as applied: "+id) {
			t.Fatalf("first db:pull = %q, %v", out, err)
		}

		out, _, err = a.run(t, "migrate:status", "-json")
		if err != nil || !strings.Contains(out, `"ran": true`) && !strings.Contains(out, `"ran":true`) {
			t.Fatalf("migrate:status = %q, %v", out, err)
		}

		out, _, err = a.run(t, args()...)
		if err != nil || !strings.Contains(out, "Already applied: "+id) {
			t.Fatalf("second db:pull = %q, %v", out, err)
		}
	})
}

type diffModel struct {
	ID    int64 `db:"id"`
	Email string
	Name  string
}

func (diffModel) Table() string { return "people" }

func diffSetup(t *testing.T, extra func(s *schema.Schema)) appRun {
	t.Helper()

	a := newAppRun(t)
	a.models = []orm.ModelInfo{orm.ModelOf[diffModel]()}

	db, err := database.Open(t.Context(), a.dbURL)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	s := schema.New(t.Context(), db)
	if err := s.Create("people", func(t *schema.Table) {
		t.ID()
		t.String("email")
		t.String("nickname", 30).Nullable()
	}); err != nil {
		t.Fatal(err)
	}

	if extra != nil {
		extra(s)
	}

	return a
}

func TestMigrateDiffConsent(t *testing.T) {
	t.Parallel()

	a := diffSetup(t, nil)
	dir := filepath.Join(t.TempDir(), "migrations")

	// Without consent nothing is written and the block is the error.
	_, _, err := a.run(t, "migrate:diff", "-o", dir)
	if err == nil || !strings.Contains(err.Error(), "consent required for 1 destructive change(s)") ||
		!strings.Contains(err.Error(), "pass --delete people.nickname") {
		t.Fatalf("migrate:diff without consent = %v", err)
	}

	if _, statErr := os.Stat(dir); statErr == nil {
		t.Error("migrate:diff wrote something without consent")
	}

	// With --delete: the add of name and the drop of nickname.
	out, _, err := a.run(t, "migrate:diff", "-o", "-", "-delete", "people.nickname")
	if err != nil || !strings.Contains(out, `t.DropColumn("nickname")`) ||
		!strings.Contains(out, `t.String("name", 255).Nullable()`) {
		t.Fatalf("migrate:diff --delete = %q, %v", out, err)
	}

	// With --rename the two become one rename (the add of name is separate).
	out, _, err = a.run(t, "migrate:diff", "-o", "-", "-rename", "people.nickname:people.name")
	if err != nil || !strings.Contains(out, `t.RenameColumn("nickname", "name")`) ||
		strings.Contains(out, "DropColumn") {
		t.Fatalf("migrate:diff --rename = %q, %v", out, err)
	}

	if _, _, err := a.run(t, "migrate:diff", "-o", "-", "-delete", "people.zzz"); err == nil ||
		!strings.Contains(err.Error(), "--delete people.zzz matches no pending change") {
		t.Errorf("--delete of a no-op = %v", err)
	}

	if _, _, err := a.run(t, "migrate:diff", "-check"); err == nil {
		t.Error("-check passed although the models and the database differ")
	}
}

func TestMigrateDiffCreatesTablesAndWarns(t *testing.T) {
	t.Parallel()

	a := diffSetup(t, func(s *schema.Schema) {
		_ = s.Create("legacy_reports", func(t *schema.Table) { t.ID() })
	})
	a.models = append(a.models, orm.ModelOf[pullUser]())

	out, _, err := a.run(t, "migrate:diff", "-o", "-", "-delete", "people.nickname", "-delete", "legacy_reports")
	if err != nil || !strings.Contains(out, `s.Create("users"`) || !strings.Contains(out, `s.Drop("legacy_reports")`) {
		t.Fatalf("migrate:diff = %q, %v", out, err)
	}

	// Without the table's consent it is only a warning.
	out, errOut, err := a.run(t, "migrate:diff", "-o", "-", "-delete", "people.nickname")
	if err != nil || strings.Contains(out, "legacy_reports") ||
		!strings.Contains(errOut, "table legacy_reports has no model") {
		t.Errorf("unmodelled table: out %q, stderr %q, err %v", out, errOut, err)
	}
}

func TestMigrateDiffNoChanges(t *testing.T) {
	t.Parallel()

	a := diffSetup(t, nil)
	a.models = []orm.ModelInfo{orm.ModelOf[noDiffModel]()}

	if out, _, err := a.run(t, "migrate:diff", "-check"); err != nil || !strings.Contains(out, "No changes.") {
		t.Errorf("-check on matching schemas = %q, %v", out, err)
	}
}

type noDiffModel struct {
	ID       int64  `db:"id"`
	Email    string `db:"email"`
	Nickname string `db:"nickname"`
}

func (noDiffModel) Table() string { return "people" }

type indexedModel struct {
	ID    int64 `db:"id"`
	Email string
}

func (indexedModel) Table() string { return "members" }

type staffModel struct {
	ID       int64 `db:"id"`
	Email    string
	FullName string `db:"full_name"`
	Bio      string
}

func (staffModel) Table() string { return "staff" }

// A table that needs a Rebuild gets only the Rebuild: it absorbs the other
// drops and the adds, and a rename must carry RenamedFrom or its data is lost.
func TestMigrateDiffSQLiteRebuildAbsorbsTheTablesOtherChanges(t *testing.T) {
	t.Parallel()

	a := newAppRun(t)
	a.models = []orm.ModelInfo{orm.ModelOf[staffModel]()}

	db, err := database.Open(t.Context(), a.dbURL)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if err := schema.New(t.Context(), db).Create("staff", func(t *schema.Table) {
		t.ID()
		t.String("email").Unique()
		t.String("nickname", 30)
		t.String("phone", 30).Nullable()
		t.String("old_name", 30)
		t.Index("nickname")
	}); err != nil {
		t.Fatal(err)
	}

	out, _, err := a.run(t, "migrate:diff", "-o", "-", "-delete", "staff.nickname", "-delete", "staff.phone",
		"-rename", "staff.old_name:staff.full_name")
	if err != nil {
		t.Fatal(err)
	}

	if strings.Count(out, `s.Rebuild("staff"`) != 1 || strings.Contains(out, "s.Table(") ||
		strings.Contains(out, "DropColumn(") || strings.Contains(out, "RenameColumn(") {
		t.Fatalf("a rebuilt table must be changed by the Rebuild alone:\n%s", out)
	}

	if !strings.Contains(out, `schema.RenamedFrom("old_name", "full_name")`) {
		t.Errorf("the rename must keep its data through RenamedFrom:\n%s", out)
	}

	if !strings.Contains(out, `t.String("bio", 255).Nullable()`) {
		t.Errorf("the added column must be nullable, as in the alter path:\n%s", out)
	}
}

func TestMigrateDiffRebuildSaysWhatItDoesNotReproduce(t *testing.T) {
	t.Parallel()

	a := newAppRun(t)
	a.models = []orm.ModelInfo{orm.ModelOf[indexedModel]()}

	db, err := database.Open(t.Context(), a.dbURL)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	for _, q := range []string{
		"CREATE TABLE members (id INTEGER PRIMARY KEY AUTOINCREMENT, email VARCHAR(255) NOT NULL DEFAULT 'a@b.c' CHECK (length(email) > 3), nickname VARCHAR(30) NOT NULL)",
		"CREATE INDEX members_nickname ON members (nickname)",
		"CREATE INDEX members_lower_email ON members (lower(email))",
		"CREATE TRIGGER members_touch AFTER INSERT ON members BEGIN SELECT 1; END",
	} {
		if _, err := db.ExecContext(t.Context(), q); err != nil {
			t.Fatal(err)
		}
	}

	out, errOut, err := a.run(t, "migrate:diff", "-o", "-", "-delete", "members.nickname")
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{
		"migrate:diff did not reproduce: the default of email",
		"the expression index members_lower_email",
		"the CHECK constraints",
		"the triggers members_touch",
	} {
		if !strings.Contains(out, want) ||
			!strings.Contains(errOut, strings.TrimPrefix(want, "migrate:diff did not reproduce: ")) {
			t.Errorf("missing %q in the file or the warnings:\n%s\n---\n%s", want, out, errOut)
		}
	}

	if strings.Contains(out, `t.Index("")`) || strings.Contains(out, "lower") && strings.Contains(out, "t.Index(") {
		t.Errorf("an expression index must never be written as a column index:\n%s", out)
	}
}

func TestMigrateDiffSQLiteDropIndexedColumnUsesRebuild(t *testing.T) {
	t.Parallel()

	a := newAppRun(t)
	a.models = []orm.ModelInfo{orm.ModelOf[indexedModel]()}

	db, err := database.Open(t.Context(), a.dbURL)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	if err := schema.New(t.Context(), db).Create("members", func(t *schema.Table) {
		t.ID()
		t.String("email").Unique()
		t.String("nickname", 30)
		t.Index("nickname")
	}); err != nil {
		t.Fatal(err)
	}

	out, _, err := a.run(t, "migrate:diff", "-o", "-", "-delete", "members.nickname")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(out, "NoTransaction: true") || !strings.Contains(out, `s.Rebuild("members"`) ||
		strings.Contains(out, "t.DropColumn") {
		t.Fatalf("an indexed column on SQLite must be dropped with Rebuild:\n%s", out)
	}

	golden := filepath.Join("testdata", "migratediff", "rebuild.golden")

	got := regexp.MustCompile(`\d{14}`).ReplaceAllString(out, "TS")
	if *update {
		if err := os.MkdirAll(filepath.Dir(golden), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(golden, []byte(got), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatal(err)
	}

	if diff := cmp.Diff(string(want), got); diff != "" {
		t.Errorf("rebuild migration mismatch (-want +got):\n%s", diff)
	}

	// A column without an index is a plain DropColumn.
	b := diffSetup(t, nil)

	plain, _, err := b.run(t, "migrate:diff", "-o", "-", "-delete", "people.nickname")
	if err != nil || strings.Contains(plain, "Rebuild") || !strings.Contains(plain, `t.DropColumn("nickname")`) {
		t.Errorf("an unindexed column should be a DropColumn:\n%s", plain)
	}
}
