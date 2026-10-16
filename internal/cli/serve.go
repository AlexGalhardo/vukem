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
	"time"
)

// snapshot maps watched files to their modification time.
type snapshot map[string]time.Time

func takeSnapshot(dir string) (snapshot, error) {
	s := snapshot{}

	err := walkSources(dir, func(p string, info fs.FileInfo) { s[p] = info.ModTime() })
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

	return func(ctx context.Context, env *Env, args []string) error {
		if !exists(env, filepath.Join("cmd", "app")) {
			return errors.New("no cmd/app in this directory: run lumek inside a Lumek project")
		}

		if hasVite(env) && !*noVite {
			vite := exec.CommandContext(ctx, npm(), "run", "dev") //nolint:gosec // G204: fixed program name
			vite.Dir, vite.Stdout, vite.Stderr = env.Dir, env.Stdout, env.Stderr

			if err := vite.Start(); err != nil {
				return fmt.Errorf("start vite: %w", err)
			}

			defer func() { _ = vite.Wait() }()
		}

		return watch(ctx, env, *interval, args)
	}
}

// watch builds and runs the app, rebuilding and restarting it when Go
// sources change. Templates reload inside the app (no restart needed).
// A failed build keeps the previous process running.
func watch(ctx context.Context, env *Env, interval time.Duration, args []string) error {
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

	start := func() {
		// Build to a separate file: Windows cannot overwrite a running .exe.
		if err := goBuild(ctx, env, next); err != nil {
			if ctx.Err() == nil {
				fmt.Fprintln(env.Stderr, "lumek serve: build failed, keeping the previous version:", err)
			}

			return
		}

		stop()

		if err := os.Rename(next, bin); err != nil {
			fmt.Fprintln(env.Stderr, "lumek serve:", err)
			return
		}

		cmd := exec.CommandContext(ctx, bin, args...)
		cmd.Dir, cmd.Stdout, cmd.Stderr = env.Dir, env.Stdout, env.Stderr

		if err := cmd.Start(); err != nil {
			fmt.Fprintln(env.Stderr, "lumek serve:", err)
			return
		}

		proc = cmd
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
				fmt.Fprintln(env.Stderr, "lumek serve:", err)
				continue
			}

			if last.changed(snap) {
				last = snap
				fmt.Fprintln(env.Stdout, "lumek serve: change detected, rebuilding")
				start()
			}
		}
	}
}
