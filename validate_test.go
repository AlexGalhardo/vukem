package vukem

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/router"
)

var errBadEdition = errors.New("APP_EDITION must be personal or family")

func editionCheck(app *App) error {
	if app.Source.Get("APP_EDITION") != "personal" {
		return errBadEdition
	}

	return nil
}

func TestWithValidateRunsBeforeKeyCheckAndRoutes(t *testing.T) {
	t.Parallel()

	routesRan := false
	// Production without APP_KEY: the framework would report the missing key.
	env := map[string]string{"APP_ENV": "production"}

	_, err := New(
		WithSource(testSource(env)), WithLogOutput(io.Discard),
		WithRoutes(func(*router.Router, *App) error { routesRan = true; return nil }),
		WithValidate(editionCheck),
	)
	if !errors.Is(err, errBadEdition) {
		t.Fatalf("New() error = %v, want the validation error", err)
	}

	if errors.Is(err, config.ErrKeyMissing) || strings.Contains(err.Error(), "APP_KEY") {
		t.Errorf("New() error = %q, want the app's error, not the APP_KEY one", err)
	}

	if !strings.HasPrefix(err.Error(), "vukem: validate: ") {
		t.Errorf("New() error = %q, want the vukem: validate prefix", err)
	}

	if routesRan {
		t.Error("routes ran after a failed validation")
	}
}

func TestWithValidatePassesAndSeesConfig(t *testing.T) {
	t.Parallel()

	var kind Kind

	routesRan := false
	env := map[string]string{"APP_KEY": testKey, "APP_EDITION": "personal", "APP_NAME": "Fin"}

	_, err := New(
		WithSource(testSource(env)), WithLogOutput(io.Discard),
		WithRoutes(func(*router.Router, *App) error { routesRan = true; return nil }),
		WithValidate(func(app *App) error {
			kind = app.Kind
			if app.Config.Name != "Fin" {
				t.Errorf("Config.Name = %q, want Fin", app.Config.Name)
			}

			return editionCheck(app)
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if !routesRan || kind != KindWeb {
		t.Errorf("routesRan = %v, kind = %v, want routes run and KindWeb", routesRan, kind)
	}
}

func TestWithValidateFirstErrorStopsLaterChecks(t *testing.T) {
	t.Parallel()

	second := false

	_, err := New(
		WithSource(testSource(map[string]string{"APP_KEY": testKey})), WithLogOutput(io.Discard),
		WithValidate(func(*App) error { return errBadEdition }),
		WithValidate(func(*App) error { second = true; return nil }),
	)
	if !errors.Is(err, errBadEdition) || second {
		t.Fatalf("err = %v, second ran = %v, want the first error and no second check", err, second)
	}
}

func TestWithValidateStopsCommandsAndServer(t *testing.T) {
	t.Parallel()

	getenv := testSource(map[string]string{"APP_ENV": "production"}).Lookup
	build := func(opts ...Option) (*App, error) {
		return New(append(opts, WithLogOutput(io.Discard), WithValidate(editionCheck))...)
	}

	for _, cmd := range []string{"about", "route:list", "serve"} {
		t.Run(cmd, func(t *testing.T) {
			t.Parallel()

			err := RunMain(
				t.Context(), []string{"app", cmd}, getenv, build,
				WithStdout(io.Discard), WithStderr(io.Discard),
			)
			if !errors.Is(err, errBadEdition) {
				t.Fatalf("RunMain(%s) error = %v, want the validation error", cmd, err)
			}
		})
	}
}
