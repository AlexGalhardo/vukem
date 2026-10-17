package vukem

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/AlexGalhardo/vukem/config"
)

const keyGenerateName = "key:generate"

// keyGenerateCommand lists key:generate; RunMain runs it before building
// the app, which fails closed without a valid APP_KEY.
func keyGenerateCommand() Command {
	return CommandFunc{
		CommandName: keyGenerateName,
		Desc:        "Print a new random APP_KEY (never writes .env)",
		Fn: func(_ context.Context, app *App, _ []string) error {
			_, err := fmt.Fprintln(app.stdout, config.GenerateKey())
			return err
		},
	}
}

// runKeyGenerate prints a new APP_KEY to the WithStdout writer in opts
// (os.Stdout by default) without building the app, so images without the
// global CLI can create one: APP_KEY=$(./app key:generate).
func runKeyGenerate(args []string, opts []Option) error {
	var o options
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return err
		}
	}

	var w io.Writer = os.Stdout
	if o.stdout != nil {
		w = o.stdout
	}

	fset := flag.NewFlagSet(keyGenerateName, flag.ContinueOnError)
	fset.SetOutput(w)

	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return errUsage
	}

	if fset.NArg() > 0 {
		fmt.Fprintf(w, "%s takes no arguments\n", keyGenerateName)
		return errUsage
	}

	_, err := fmt.Fprintln(w, config.GenerateKey())

	return err
}
