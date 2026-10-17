package vukem

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/crypt"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/router"
)

// noKeyApp builds an app without APP_KEY in env (development or test).
func noKeyApp(t *testing.T, env string, extra map[string]string, opts ...Option) (*App, error) {
	t.Helper()

	m := map[string]string{"APP_ENV": env}
	maps.Copy(m, extra)

	return New(append([]Option{WithSource(config.MapSource(m)), WithLogger(discardLogger())}, opts...)...)
}

func TestAppBootsWithoutKeyOutsideProduction(t *testing.T) {
	t.Parallel()

	for _, env := range []string{"development", "test"} {
		t.Run(env, func(t *testing.T) {
			t.Parallel()

			app, err := noKeyApp(t, env, nil, WithRoutes(func(r *router.Router, app *App) error {
				return APIMiddleware(app, r)
			}))
			if err != nil {
				t.Fatalf("New() without APP_KEY in %s = %v, want nil", env, err)
			}

			if !app.Key.IsZero() || app.Crypt != nil {
				t.Errorf("Key.IsZero() = %v, Crypt = %v, want zero key and nil Crypt", app.Key.IsZero(), app.Crypt)
			}

			if rec := doReq(t, app, "/up"); rec.Code != 200 {
				t.Errorf("GET /up = %d, want 200", rec.Code)
			}
		})
	}
}

func TestSecurity_A02_ProductionRequiresAppKey(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{name: "missing in production", env: map[string]string{"APP_ENV": "production"}, want: "APP_KEY is not set"},
		{
			name: "malformed in development",
			env:  map[string]string{"APP_ENV": "development", "APP_KEY": "base64:YWJj"},
			want: "at least 32 bytes",
		},
		{
			name: "bad previous key without a key",
			env:  map[string]string{"APP_ENV": "development", "APP_PREVIOUS_KEYS": "nope"},
			want: "APP_PREVIOUS_KEYS",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := New(WithSource(config.MapSource(tt.env)), WithLogger(discardLogger()))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("New() error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestSecurity_A04_MissingKeyFailsClosed(t *testing.T) {
	t.Parallel()

	t.Run("cookie sessions refuse to start", func(t *testing.T) {
		t.Parallel()

		app, err := noKeyApp(t, "development", nil)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := app.Sessions(); !errors.Is(err, config.ErrKeyMissing) {
			t.Fatalf("Sessions() error = %v, want ErrKeyMissing", err)
		}
	})

	t.Run("DefaultMiddleware is a boot error with cookie sessions", func(t *testing.T) {
		t.Parallel()

		_, err := noKeyApp(t, "development", nil, WithRoutes(defaultMiddleware))
		if !errors.Is(err, config.ErrKeyMissing) {
			t.Fatalf("New(DefaultMiddleware) error = %v, want ErrKeyMissing", err)
		}
	})

	t.Run("server-side sessions need no key", func(t *testing.T) {
		t.Parallel()

		if _, err := noKeyApp(t, "development", map[string]string{"SESSION_DRIVER": "memory"},
			WithRoutes(defaultMiddleware)); err != nil {
			t.Fatalf("New(DefaultMiddleware, SESSION_DRIVER=memory) = %v, want nil", err)
		}
	})

	t.Run("DeriveKey errors", func(t *testing.T) {
		t.Parallel()

		app, err := noKeyApp(t, "development", nil)
		if err != nil {
			t.Fatal(err)
		}

		if b, err := app.DeriveKey("signed.urls", 32); !errors.Is(err, config.ErrKeyMissing) || b != nil {
			t.Fatalf("DeriveKey() = %x, %v, want nil, ErrKeyMissing", b, err)
		}
	})

	t.Run("Key.Derive panics", func(t *testing.T) {
		t.Parallel()

		app, err := noKeyApp(t, "development", nil)
		if err != nil {
			t.Fatal(err)
		}

		if msg := panicMessage(func() { app.Key.Derive("signed.urls", 32) }); !strings.Contains(msg, "APP_KEY") {
			t.Fatalf("Key.Derive() on a missing key panicked with %q, want an APP_KEY message", msg)
		}
	})

	t.Run("Crypt fails closed", func(t *testing.T) {
		t.Parallel()

		app, err := noKeyApp(t, "development", nil)
		if err != nil {
			t.Fatal(err)
		}

		if _, err := app.Crypt.DecryptString("token"); !errors.Is(err, crypt.ErrNoKey) {
			t.Errorf("Crypt.DecryptString() error = %v, want crypt.ErrNoKey", err)
		}

		if msg := panicMessage(func() { app.Crypt.EncryptString("secret") }); !strings.Contains(msg, "APP_KEY") {
			t.Errorf("Crypt.EncryptString() panicked with %q, want an APP_KEY message", msg)
		}
	})

	t.Run("database has no encrypter", func(t *testing.T) {
		t.Parallel()

		url := "file:" + filepath.Join(testutil.TempDir(t), "k.db")

		app, err := noKeyApp(t, "development", map[string]string{"DATABASE_URL": url})
		if err != nil {
			t.Fatal(err)
		}

		t.Cleanup(func() { _ = app.Shutdown(t.Context()) })

		db, err := app.DB()
		if err != nil {
			t.Fatal(err)
		}

		// A typed nil *crypt.Encrypter would defeat orm's ErrNoEncrypter check.
		if enc := db.Encrypter(); enc != nil {
			t.Fatalf("DB().Encrypter() = %#v, want nil so encrypted columns fail closed", enc)
		}
	})
}

func TestDeriveKeyWithKey(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	got, err := app.DeriveKey("signed.urls", 32)
	if err != nil {
		t.Fatal(err)
	}

	if want := app.Key.Derive("signed.urls", 32); !bytes.Equal(got, want) {
		t.Fatalf("DeriveKey() = %x, want %x", got, want)
	}
}

func panicMessage(fn func()) (msg string) {
	defer func() { msg = fmt.Sprint(recover()) }()

	fn()

	return ""
}

func defaultMiddleware(r *router.Router, app *App) error { return DefaultMiddleware(app, r) }
