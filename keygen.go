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

var errKeyGenerateArgs = fmt.Errorf("%s takes no arguments: %w", keyGenerateName, errUsage)

// keyGenerateCommand lists key:generate and serves App.Call. RunMain runs
// it through runKeyGenerate before building the app, which fails closed
// without a valid APP_KEY, so it takes precedence over an app command with
// the same name.
func keyGenerateCommand() Command {
	return CommandFunc{
		CommandName: keyGenerateName,
		Desc:        "Print a new random APP_KEY (never writes .env)",
		Fn: func(_ context.Context, app *App, args []string) error {
			return writeNewKey(app.stdout, args)
		},
	}
}

// writeNewKey prints only the key, so APP_KEY=$(./app key:generate) is safe.
func writeNewKey(w io.Writer, args []string) error {
	if len(args) > 0 {
		return errKeyGenerateArgs
	}

	_, err := fmt.Fprintln(w, config.GenerateKey())

	return err
}

// runKeyGenerate is key:generate without an app: the key goes to the
// WithStdout writer in opts (os.Stdout by default) and every diagnostic to
// stderr, so a typo never ends up inside a captured APP_KEY.
func runKeyGenerate(args []string, opts []Option) error {
	o := options{stdout: os.Stdout, stderr: os.Stderr}
	for _, opt := range opts {
		if err := opt(&o); err != nil {
			return err
		}
	}

	fset := flag.NewFlagSet(keyGenerateName, flag.ContinueOnError)
	fset.SetOutput(o.stderr)
	keyGenerateCommand().Flags(fset)

	if err := fset.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}

		return errUsage
	}

	err := writeNewKey(o.stdout, fset.Args())
	if errors.Is(err, errUsage) {
		fmt.Fprintln(o.stderr, err)
	}

	return err
}
