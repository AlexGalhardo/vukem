package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// snapshot maps watched files to their modification time.
type snapshot map[string]time.Time

func takeSnapshot(dir string) (snapshot, error) {
	s := snapshot{}

	err := walkSources(dir, func(p string, info fs.FileInfo) {
		// Generated from the watched .templ files before each build; counting
		// them would rebuild twice.
		if !strings.HasSuffix(p, "_templ.go") {
			s[p] = info.ModTime()
		}
	})
	if err != nil {
		return nil, err
	}

	if info, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
		s[".env"] = info.ModTime()
	}

	return s, nil
}

// changed reports whether files were added, removed or modified.
func (s snapshot) changed(next snapshot) bool {
	if len(s) != len(next) {
		return true
	}

	for p, t := range next {
		if old, ok := s[p]; !ok || !old.Equal(t) {
			return true
		}
	}

	return false
}

func serveCmd(set *flag.FlagSet) func(context.Context, *Env, []string) error {
	interval := set.Duration("poll", 500*time.Millisecond, "how often to check Go files for changes")
	noVite := set.Bool("no-vite", false, "do not start the Vite dev server")
	noCodegen := set.Bool("no-codegen", false, "do not regenerate routes.ts and the API clients after each build")

	return func(ctx context.Context, env *Env, args []string) error {
		if !exists(env, filepath.Join("cmd", "app")) {
			return errors.New("no cmd/app in this directory: run vukem inside a Vukem project")
		}

		if hasVite(env) && !*noVite {
			pm, err := packageManager(env.Dir, "")
			if err != nil {
				return err
			}

			vite := exec.CommandContext(ctx, pm.binary(), "run", "dev") //nolint:gosec // G204: known package manager
			vite.Dir, vite.Stdout, vite.Stderr = env.Dir, env.Stdout, env.Stderr

			if err := vite.Start(); err != nil {
				return fmt.Errorf("start vite: %w", err)
			}

			defer func() {
				_ = vite.Wait()
				// A killed dev server (Windows has no SIGTERM) cannot remove its
				// hot file; a stale one would point pages at a dead server.
				_ = os.Remove(env.path(filepath.Join("resources", "public", "hot")))
			}()
		}

		return watch(ctx, env, *interval, !*noCodegen, args)
	}
}

// watch builds and runs the app, rebuilding and restarting it when Go or
// templ sources change; templ components are generated before each build.
// Templates reload inside the app (no restart needed). A failed build keeps
// the previous process running. After each build, with codegen, the app's
// "codegen" command refreshes the generated frontend files that exist.
func watch(ctx context.Context, env *Env, interval time.Duration, codegen bool, args []string) error {
	dir := env.dirOrDot()
	binDir := env.path(filepath.Join(cacheDir, "serve"))
	bin := filepath.Join(binDir, exe("app"))
	next := filepath.Join(binDir, exe("app-next"))

	var proc *exec.Cmd

	stop := func() {
		if proc != nil {
			_ = proc.Process.Kill() // Windows has no SIGTERM; the app gets no graceful shutdown in dev
			_ = proc.Wait()
			proc = nil
		}
	}
	defer stop()

	var check viewCheckRun

	defer check.stop()

	// A codegen of the previous build is stopped before the next one: on
	// Windows it holds the binary the rebuild is about to replace.
	gen := codegenRun{enabled: codegen}

	defer gen.stop()

	start := func() {
		gen.stop()
		check.stop()

		if hasTempl(env) {
			if err := templGenerate(ctx, env); err != nil {
				if ctx.Err() == nil {
					fmt.Fprintln(env.Stderr, "vukem serve: templ generate failed, keeping the previous version:", err)
				}

				return
			}
		}

		// Build to a separate file: Windows cannot overwrite a running .exe.
		if err := goBuild(ctx, env, next); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintln(env.Stderr, "vukem serve: build failed, keeping the previous version:", err)
			}

			return
		}

		stop()

		if err := os.Rename(next, bin); err != nil {
			fmt.Fprintln(env.Stderr, "vukem serve:", err)
			return
		}

		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = env.Dir, env.Stdout, env.Stderr

		if err := cmd.Start(); err != nil {
			fmt.Fprintln(env.Stderr, "vukem serve:", err)
			return
		}

		proc = cmd

		gen.start(ctx, env, bin)

		check.start(ctx, env, bin)
	}

	last, err := takeSnapshot(dir)
	if err != nil {
		return err
	}

	start()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			snap, err := takeSnapshot(dir)
			if err != nil {
				fmt.Fprintln(env.Stderr, "vukem serve:", err)
				continue
			}

			if last.changed(snap) {
				last = snap
				fmt.Fprintln(env.Stdout, "vukem serve: change detected, rebuilding")
				start()
			}
		}
	}
}

// codegenRun is the codegen of one build, running beside the app.
type codegenRun struct {
	enabled bool
	cancel  context.CancelFunc
	done    chan struct{}
}

// start runs the built app's codegen in the background: the restart does not
// wait for it, and a failure or a timeout is a warning, never an error of the
// serve.
func (g *codegenRun) start(ctx context.Context, env *Env, bin string) {
	if !g.enabled {
		return
	}

	rctx, cancel := context.WithTimeout(ctx, codegenTimeout)
	g.cancel, g.done = cancel, make(chan struct{})

	go func() {
		defer close(g.done)

		err := runCodegen(rctx, env, bin)

		switch {
		case errors.Is(rctx.Err(), context.DeadlineExceeded):
			fmt.Fprintf(env.Stderr, "vukem serve: codegen timed out after %s; the app was restarted without waiting\n",
				codegenTimeout)
		case ctx.Err() != nil || rctx.Err() != nil:
			// Stopped by a newer rebuild or by the end of the serve.
		case err != nil:
			fmt.Fprintln(env.Stderr, "vukem serve: codegen failed:", err)
		}
	}()
}

// stop cancels the run and waits for the process to be gone.
func (g *codegenRun) stop() {
	if g.cancel != nil {
		g.cancel()
		<-g.done

		g.cancel = nil
	}
}

// runCodegen runs the built app's "codegen" command, which rewrites a
// generated file only when its bytes change, so the Go files it may touch
// cause at most one more rebuild and not a loop. PORT is zeroed so a binary
// that does not understand the command and starts its server instead takes a
// free port, not the one of the app that is running.
func runCodegen(ctx context.Context, env *Env, bin string) error {
	cmd := exec.CommandContext(ctx, bin, "codegen")
	cmd.Dir, cmd.Stdout, cmd.Stderr = env.Dir, env.Stdout, env.Stderr
	cmd.Env = append(os.Environ(), "PORT=0")
	cmd.WaitDelay = 2 * time.Second

	return cmd.Run()
}

// codegenTimeout bounds one codegen run: it only loads the app's routes and
// writes a few files, seconds at most, so a minute means the binary does not
// understand the command. A variable for the tests.
var codegenTimeout = 60 * time.Second
