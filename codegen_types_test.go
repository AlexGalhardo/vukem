package vukem

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/router"
)

// unreachableDB stands for a provider whose Boot opens a connection.
type unreachableDB struct{}

func (unreachableDB) Register(*App) error { return nil }

func (unreachableDB) Boot(context.Context, *App) error {
	return errors.New("dial tcp: database unreachable")
}

func TestCodegenTypesWithoutDatabaseOrKey(t *testing.T) {
	t.Parallel()

	env := config.MapSource(map[string]string{}) // no APP_KEY, no APP_ENV, no database

	tests := []struct {
		name    string
		routes  func(*router.Router, *App) error
		wantErr error
	}{
		{name: "routes only", routes: openAPIRoutes},
		{
			name: "routes that derive a key fail closed",
			routes: func(r *router.Router, app *App) error {
				if _, err := app.DeriveKey("signing", 32); err != nil {
					return err
				}

				return openAPIRoutes(r, app)
			},
			wantErr: config.ErrKeyMissing,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var out bytes.Buffer

			build := func(opts ...Option) (*App, error) {
				return New(append(opts, WithLogger(discardLogger()), WithStdout(&out),
					WithProviders(unreachableDB{}), WithRoutes(tt.routes))...)
			}

			err := RunMain(t.Context(), []string{"app", "codegen:types", "-zod"}, env.Lookup, build)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("codegen:types error = %v, want %v", err, tt.wantErr)
			}

			if tt.wantErr == nil && !strings.Contains(out.String(), "export type PingResponse = PingOut;") {
				t.Errorf("codegen:types output = %q", out.String())
			}
		})
	}
}

func TestSecurity_A02_WarmupToleratesOnlyAMissingKey(t *testing.T) {
	t.Parallel()

	warmup := func(env map[string]string) (*App, error) {
		return New(WithKind(KindWarmup), WithSource(config.MapSource(env)), WithLogger(discardLogger()))
	}

	for _, env := range []map[string]string{{}, {"APP_ENV": "production"}} {
		app, err := warmup(env)
		if err != nil {
			t.Fatalf("New(warmup, %v) without APP_KEY = %v, want nil", env, err)
		}

		if _, err := app.DeriveKey("x", 32); !errors.Is(err, config.ErrKeyMissing) {
			t.Errorf("warmup DeriveKey error = %v, want ErrKeyMissing", err)
		}

		if app.Crypt != nil {
			t.Error("warmup app without APP_KEY has a Crypt")
		}

		if _, err := app.Sessions(); !errors.Is(err, config.ErrKeyMissing) {
			t.Errorf("warmup Sessions() error = %v, want ErrKeyMissing", err)
		}
	}

	// A warmup app never serves or starts runners, even when asked to.
	app, err := warmup(map[string]string{})
	if err != nil {
		t.Fatal(err)
	}

	app.AddRunner("worker", RunnerFunc(func(context.Context) error {
		t.Error("warmup app started a runner")
		return nil
	}))

	if err := app.Run(t.Context()); !errors.Is(err, ErrWarmupRun) {
		t.Errorf("warmup Run() = %v, want ErrWarmupRun", err)
	}

	if _, err := warmup(map[string]string{"APP_KEY": "base64:YWJj"}); err == nil {
		t.Error("New(warmup) with a malformed APP_KEY = nil, want error")
	}

	// Any other kind keeps refusing a missing key when APP_ENV is unset.
	if _, err := New(WithKind(KindConsole), WithSource(config.MapSource(map[string]string{})),
		WithLogger(discardLogger())); !errors.Is(err, config.ErrKeyMissing) {
		t.Errorf("New(console) without APP_KEY = %v, want ErrKeyMissing", err)
	}
}
