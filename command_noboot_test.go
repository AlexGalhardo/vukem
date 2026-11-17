package vukem

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/config"
)

// noBootPing talks to nothing and needs no app. It is the shape of a command
// that only reaches another process.
type noBootPing struct {
	ran *bool
}

func (noBootPing) Name() string        { return "remote:ping" }
func (noBootPing) Description() string { return "ping another process" }
func (noBootPing) Flags(*flag.FlagSet) {}
func (noBootPing) NoBoot() bool        { return true }

func (c noBootPing) Run(context.Context, *App, []string) error {
	if c.ran != nil {
		*c.ran = true
	}

	return nil
}

type noBootExit struct{}

func (noBootExit) Name() string        { return "remote:fail" }
func (noBootExit) Description() string { return "exit with a code and no text" }
func (noBootExit) Flags(*flag.FlagSet) {}
func (noBootExit) NoBoot() bool        { return true }
func (noBootExit) Run(context.Context, *App, []string) error {
	return &ExitError{Code: 7}
}

func TestRunMainNoBootSkipsBuildWithoutAppKey(t *testing.T) {
	t.Parallel()

	var built bool

	var ran bool

	build := func(_ ...Option) (*App, error) {
		built = true

		return nil, errors.New("config: APP_KEY is not set")
	}
	getenv := func(string) (string, bool) { return "", false }

	err := RunMain(t.Context(), []string{"app", "remote:ping"}, getenv, build, WithCommands(noBootPing{ran: &ran}))
	if err != nil {
		t.Fatalf("RunMain(remote:ping) = %v, want nil without APP_KEY", err)
	}

	if !ran {
		t.Fatal("NoBoot command did not run")
	}

	if built {
		t.Fatal("RunMain built the app for a NoBoot command")
	}
}

func TestRunMainUnknownCommandAndFlagSkipAppKey(t *testing.T) {
	t.Parallel()

	getenv := func(string) (string, bool) { return "", false }
	build := func(_ ...Option) (*App, error) {
		return nil, errors.New("config: APP_KEY is not set")
	}

	tests := []struct {
		name string
		args []string
	}{
		{name: "unknown command", args: []string{"app", "migrte"}},
		{name: "unknown flag", args: []string{"app", "route:list", "--jsn"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var errOut bytes.Buffer

			err := RunMain(t.Context(), tt.args, getenv, build, WithStderr(&errOut))
			if !errors.Is(err, ErrUsage) {
				t.Fatalf("RunMain(%v) = %v, want an unknown-command error", tt.args, err)
			}

			if !strings.Contains(errOut.String(), "did you mean") {
				t.Errorf("stderr = %q, want a suggestion", errOut.String())
			}

			if strings.Contains(err.Error(), "APP_KEY") || strings.Contains(errOut.String(), "APP_KEY") {
				t.Errorf("error = %v, stderr = %q, want no APP_KEY failure", err, errOut.String())
			}
		})
	}
}

func TestRunMainProviderNoBootWithoutAppKey(t *testing.T) {
	t.Parallel()

	var ran bool

	build := func(opts ...Option) (*App, error) {
		opts = append(opts, WithLogger(discardLogger()), WithProviders(ProviderFunc(func(app *App) error {
			app.AddCommand(noBootPing{ran: &ran})

			return nil
		})))

		return New(opts...)
	}
	getenv := func(string) (string, bool) { return "", false }

	err := RunMain(t.Context(), []string{"app", "remote:ping"}, getenv, build)
	if err != nil {
		t.Fatalf("RunMain(remote:ping) = %v, want nil without APP_KEY", err)
	}

	if !ran {
		t.Fatal("provider NoBoot command did not run")
	}
}

func TestRunMainOrdinaryCommandStillRequiresAppKey(t *testing.T) {
	t.Parallel()

	getenv := func(string) (string, bool) { return "", false }
	err := RunMain(t.Context(), []string{"app", "migrate"}, getenv, func(opts ...Option) (*App, error) {
		opts = append(opts, WithLogger(discardLogger()))

		return New(opts...)
	})
	if !errors.Is(err, config.ErrKeyMissing) {
		t.Fatalf("RunMain(migrate) = %v, want a missing APP_KEY", err)
	}
}

func TestMainWithSilentExitError(t *testing.T) {
	// MainWith reads these package entry points. Not parallel.
	prevArgs, prevEnv, prevErr := osArgs, lookupEnv, stderr
	t.Cleanup(func() {
		osArgs, lookupEnv, stderr = prevArgs, prevEnv, prevErr
	})

	var errOut bytes.Buffer

	osArgs = func() []string { return []string{"app", "remote:fail"} }
	lookupEnv = func(string) (string, bool) { return "", false }
	stderr = func() io.Writer { return &errOut }

	var built bool

	code := MainWith(func(_ ...Option) (*App, error) {
		built = true

		return nil, errors.New("config: APP_KEY is not set")
	}, WithCommands(noBootExit{}))

	if code != 7 {
		t.Fatalf("MainWith() = %d, want 7", code)
	}

	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", errOut.String())
	}

	if built {
		t.Fatal("MainWith built the app for a NoBoot command")
	}
}
