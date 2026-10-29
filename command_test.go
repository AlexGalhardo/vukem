package vukem

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/AlexGalhardo/vukem/database"
)

type echoCommand struct{}

func (echoCommand) Name() string        { return "test:echo" }
func (echoCommand) Description() string { return "echo stdin" }
func (echoCommand) Flags(*flag.FlagSet) {}
func (echoCommand) Run(_ context.Context, app *App, args []string) error {
	in, err := io.ReadAll(app.Stdin())
	if err != nil {
		return err
	}

	fmt.Fprintf(app.Stdout(), "out:%s:%s", strings.Join(args, ","), in)
	fmt.Fprint(app.Stderr(), "err:diag")

	return nil
}

func TestCommandWriters(t *testing.T) {
	t.Parallel()

	var out, errOut bytes.Buffer

	app := newTestApp(t, WithStdout(&out), WithStderr(&errOut), WithStdin(strings.NewReader("in")),
		WithProviders(ProviderFunc(func(app *App) error { app.AddCommand(echoCommand{}); return nil })))

	if err := runCommand(t.Context(), app, "test:echo", []string{"a", "b"}); err != nil {
		t.Fatal(err)
	}

	if got, want := out.String(), "out:a,b:in"; got != want {
		t.Errorf("command stdout = %q, want %q", got, want)
	}

	if got, want := errOut.String(), "err:diag"; got != want {
		t.Errorf("command stderr = %q, want %q", got, want)
	}
}

func TestCommandWritersDefaults(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)
	if app.Stdout() != os.Stdout || app.Stderr() != os.Stderr || app.Stdin() != os.Stdin {
		t.Errorf("Stdout/Stderr/Stdin = %v, %v, %v, want the process streams", app.Stdout(), app.Stderr(), app.Stdin())
	}
}

func TestResealCommand(t *testing.T) {
	t.Parallel()

	var gotBatch int

	ok := func(n int) ResealFunc {
		return func(_ context.Context, db database.Executor, batch int) (int, error) {
			if db == nil {
				t.Error("ResealFunc got a nil db")
			}

			gotBatch = batch

			return n, nil
		}
	}
	failing := func(context.Context, database.Executor, int) (int, error) { return 1, errors.New("boom") }

	var out bytes.Buffer

	app := newTestApp(
		t,
		WithSource(testSource(map[string]string{"DATABASE_URL": ":memory:"})),
		WithStdout(&out),
		WithProviders(ProviderFunc(func(app *App) error {
			app.AddCommand(ResealCommand(ok(2), ok(3)))
			return nil
		})),
	)

	if err := runCommand(t.Context(), app, "crypt:reseal", []string{"-batch", "50"}); err != nil {
		t.Fatal(err)
	}

	if out.String() != "re-sealed 5 values\n" || gotBatch != 50 {
		t.Errorf("crypt:reseal = %q (batch %d), want %q (batch 50)", out.String(), gotBatch, "re-sealed 5 values\n")
	}

	out.Reset()

	bad := newTestApp(
		t,
		WithSource(testSource(map[string]string{"DATABASE_URL": ":memory:"})),
		WithStdout(&out),
		WithProviders(ProviderFunc(func(app *App) error {
			app.AddCommand(ResealCommand(ok(2), failing, ok(3)))
			return nil
		})),
	)

	if err := runCommand(
		t.Context(),
		bad,
		"crypt:reseal",
		nil,
	); err == nil ||
		!strings.Contains(out.String(), "3 values before") {
		t.Errorf("crypt:reseal with a failing model = %v, output %q", err, out.String())
	}
}
