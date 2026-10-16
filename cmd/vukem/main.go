// Command vukem is the Vukem CLI: generators, the dev server, builds and
// delegation of app commands to the project's binary.
//
//	go install github.com/AlexGalhardo/vukem/cmd/vukem@latest
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/AlexGalhardo/vukem/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	info, err := os.Stdin.Stat()

	code := cli.Run(ctx, os.Args[1:], cli.Env{
		Interactive: err == nil && info.Mode()&os.ModeCharDevice != 0,
		Stdin:       os.Stdin,
		Stdout:      os.Stdout,
		Stderr:      os.Stderr,
		Getenv:      os.LookupEnv,
	})

	stop()
	os.Exit(code)
}
