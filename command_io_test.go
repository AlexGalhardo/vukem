package vukem

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
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
