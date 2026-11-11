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

	start := func() {
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

		refreshGenerated(ctx, env, bin, codegen)

		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = env.Dir, env.Stdout, env.Stderr

		if err := cmd.Start(); err != nil {
			fmt.Fprintln(env.Stderr, "vukem serve:", err)
			return
		}

		proc = cmd

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

// refreshGenerated runs the codegen step when enabled and reports a failure
// without stopping the restart.
func refreshGenerated(ctx context.Context, env *Env, bin string, enabled bool) {
	if !enabled {
		return
	}

	if err := runCodegen(ctx, env, bin); err != nil && ctx.Err() == nil {
		fmt.Fprintln(env.Stderr, "vukem serve: codegen failed:", err)
	}
}

// runCodegen runs the built app's "codegen" command, which rewrites a
// generated file only when its bytes change, so the Go files it may touch
// cause at most one more rebuild and not a loop.
func runCodegen(ctx context.Context, env *Env, bin string) error {
	cmd := exec.CommandContext(ctx, bin, "codegen")
	cmd.Dir, cmd.Stdout, cmd.Stderr = env.Dir, env.Stdout, env.Stderr

	return cmd.Run()
}
