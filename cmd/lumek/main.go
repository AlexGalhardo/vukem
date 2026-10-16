// Command lumek is the Lumek CLI: generators, the dev server, builds and
// delegation of app commands to the project's binary.
//
//	go install github.com/AlexGalhardo/golang-framework/cmd/lumek@latest
package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/AlexGalhardo/golang-framework/internal/cli"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	code := cli.Run(ctx, os.Args[1:], cli.Env{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Getenv: os.LookupEnv,
	})

	stop()
	os.Exit(code)
}
