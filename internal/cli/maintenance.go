package cli

import (
	"context"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/AlexGalhardo/vukem/middleware"
)

func downCmd(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	secret := set.String("secret", "", "let visitors of /<secret> through (bypass cookie)")
	withSecret := set.Bool("with-secret", false, "generate a random secret and print the bypass URL")
	retry := set.Duration("retry", 0, "Retry-After sent with the 503 (e.g. 60s)")

	return func(_ context.Context, env *Env, _ []string) error {
		if *withSecret && *secret == "" {
			*secret = rand.Text()
		}

		file := env.path(filepath.FromSlash(middleware.DefaultMaintenanceFile))
		if err := middleware.WriteMaintenance(
			file,
			middleware.MaintenanceState{Secret: *secret, Retry: *retry},
		); err != nil {
			return fmt.Errorf("down: %w", err)
		}

		fmt.Fprintln(env.Stdout, "The application is in maintenance mode (vukem up brings it back).")

		if *secret != "" {
			fmt.Fprintf(env.Stdout, "Bypass: open /%s once to get a cookie that lets you in.\n", *secret)
		}

		return nil
	}
}

func upCmd(*flag.FlagSet) func(context.Context, *Env, []string) error {
	return func(_ context.Context, env *Env, _ []string) error {
		err := os.Remove(env.path(filepath.FromSlash(middleware.DefaultMaintenanceFile)))
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("up: %w", err)
		}

		fmt.Fprintln(env.Stdout, "The application is live.")

		return nil
	}
}
