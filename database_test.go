package vukem

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/schema"
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

	dbURL := "file:" + filepath.Join(t.TempDir(), "app.db")

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

			dbURL := "file:" + filepath.Join(t.TempDir(), "app.db")
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
		WithSource(testSource(map[string]string{"DATABASE_URL": "file:" + filepath.Join(t.TempDir(), "h.db")})),
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
		WithSource(testSource(map[string]string{"DATABASE_URL": "file:" + filepath.Join(t.TempDir(), "c.db")})),
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
