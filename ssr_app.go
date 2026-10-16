package vukem

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/inertia"
)

func ssrCommands() []Command { return []Command{ssrStartCommand(), ssrCheckCommand()} }

// ssrStartCommand supervises the Inertia SSR server: it runs the bundle with
// node and restarts it when it crashes, until the command is stopped.
func ssrStartCommand() Command {
	var (
		bundle, runtime *string
		delay           *time.Duration
	)

	return CommandFunc{
		CommandName: "ssr:start",
		Desc:        "Run the Inertia SSR server (node bootstrap/ssr/ssr.js) and restart it if it crashes",
		DefineFlags: func(fs *flag.FlagSet) {
			bundle = fs.String("bundle", "bootstrap/ssr/ssr.js", "SSR bundle built by vite build --ssr")
			runtime = fs.String("runtime", "node", "JavaScript runtime that runs the bundle")
			delay = fs.Duration("restart-delay", time.Second, "wait before restarting a crashed server")
		},
		Fn: func(ctx context.Context, app *App, _ []string) error {
			if _, err := os.Stat(*bundle); err != nil {
				return fmt.Errorf("ssr:start: bundle %s not found (run npm run build): %w", *bundle, err)
			}

			for {
				cmd := exec.CommandContext(ctx, *runtime, *bundle) //nolint:gosec // G204: developer-supplied flags
				cmd.Stdout, cmd.Stderr = app.stdout, os.Stderr
				cmd.WaitDelay = 5 * time.Second

				err := cmd.Run()

				// Stopped (Ctrl+C) or a clean exit (POST /shutdown): done.
				if err == nil || ctx.Err() != nil {
					return nil //nolint:nilerr // the process was killed on purpose
				}

				app.Log.WarnContext(ctx, "ssr: server exited, restarting", "err", err, "delay", *delay)

				select {
				case <-ctx.Done():
					return nil
				case <-time.After(*delay):
				}
			}
		},
	}
}

func ssrCheckCommand() Command {
	var url *string

	return CommandFunc{
		CommandName: "ssr:check",
		Desc:        "Check that the Inertia SSR server answers /health (exit 1 otherwise)",
		DefineFlags: func(fs *flag.FlagSet) {
			url = fs.String("url", inertia.DefaultSSRURL, "SSR server URL")
		},
		Fn: func(ctx context.Context, app *App, _ []string) error {
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()

			req, err := http.NewRequestWithContext(
				ctx,
				http.MethodGet,
				strings.TrimSuffix(*url, "/")+"/health",
				http.NoBody,
			)
			if err != nil {
				return err
			}

			res, err := http.DefaultClient.Do(req)
			if err != nil {
				return fmt.Errorf("ssr:check: %w", err)
			}
			defer res.Body.Close()

			if res.StatusCode != http.StatusOK {
				return errors.New("ssr:check: " + res.Status)
			}

			_, err = fmt.Fprintln(app.stdout, "SSR server is healthy")

			return err
		},
	}
}
