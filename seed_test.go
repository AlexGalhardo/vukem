package vukem

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/schema"
)

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
