package vukem

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
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

	dbURL := "file:" + filepath.Join(t.TempDir(), "app.db")
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
