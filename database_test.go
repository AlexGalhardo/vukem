package vukem

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/queue"
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
