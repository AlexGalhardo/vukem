package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

const (
	// viewCheckTimeout bounds the check so a slow boot cannot hold a rebuild.
	viewCheckTimeout = 30 * time.Second
	// viewCheckWaitDelay is how long a cancelled check may take to let go of
	// its pipes after it is killed.
	viewCheckWaitDelay = 2 * time.Second
	// viewCheckOutputLimit caps what is read from the check.
	viewCheckOutputLimit = 64 << 10
)

// viewProblemLine is a finding printed by view:check: file:line:col: ...
var viewProblemLine = regexp.MustCompile(`^\S+:\d+:\d+: `) //nolint:gochecknoglobals // compiled once

// warnViewProblems runs the app's view:check after a successful build and
// prints what it found on stderr. It only warns: a failing check never
// stops the rebuild, and an app without typed views stays silent. Cancelling
// ctx (the next rebuild) kills the check.
func warnViewProblems(ctx context.Context, env *Env, bin string) {
	if !usesViewPages(env.dirOrDot()) {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, viewCheckTimeout)
	defer cancel()

	// Findings go to stdout; failures (a summary, a boot error) go to
	// stderr, which is dropped.
	out := &cappedBuffer{limit: viewCheckOutputLimit}

	cmd := exec.CommandContext(ctx, bin, "view:check")
	cmd.Dir, cmd.Stdout, cmd.WaitDelay = env.Dir, out, viewCheckWaitDelay
	err := cmd.Run()

	if lines := findingLines(out.String()); lines != "" {
		fmt.Fprintf(env.Stderr, "vukem serve: view:check found problems (not blocking):\n%s", lines)
		return
	}

	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		fmt.Fprintf(env.Stderr, "vukem serve: view:check could not run: no answer in %s\n", viewCheckTimeout)
	case ctx.Err() != nil:
		// Cancelled by the next rebuild: nothing to say.
	case err != nil:
		reportViewCheckFailure(env, err)
	}
}

// viewCheckRun is the view:check of the last build, running beside the app.
// The next rebuild (or leaving serve) stops it and waits, because on Windows
// it holds the binary the rebuild is about to replace.
type viewCheckRun struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (r *viewCheckRun) start(ctx context.Context, env *Env, bin string) {
	ctx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})

	go func() {
		defer close(r.done)

		warnViewProblems(ctx, env, bin)
	}()
}

func (r *viewCheckRun) stop() {
	if r.cancel != nil {
		r.cancel()
		<-r.done

		r.cancel = nil
	}
}

// reportViewCheckFailure says why a check without findings did not run:
// exit 1 is the check's own verdict (a missing declaration), anything else
// is a crash or a missing binary.
func reportViewCheckFailure(env *Env, err error) {
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return
	}

	if exit != nil {
		fmt.Fprintf(env.Stderr, "vukem serve: view:check could not run (exit %d)\n", exit.ExitCode())
		return
	}

	fmt.Fprintf(env.Stderr, "vukem serve: view:check could not run: %v\n", err)
}

// findingLines keeps the lines in file:line:col form, dropping boot logs.
func findingLines(out string) string {
	var b strings.Builder

	for line := range strings.SplitSeq(strings.TrimSuffix(out, "\n"), "\n") {
		if viewProblemLine.MatchString(line) {
			b.WriteString(line + "\n")
		}
	}

	return b.String()
}

// cappedBuffer keeps the first limit bytes written and drops the rest.
type cappedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.limit - c.buf.Len(); room > 0 {
		c.buf.Write(p[:min(room, len(p))])
	}

	return len(p), nil
}

// String is the kept output, cut at its last complete line when the limit
// was reached.
func (c *cappedBuffer) String() string {
	s := c.buf.String()
	if c.buf.Len() >= c.limit {
		if i := strings.LastIndexByte(s, '\n'); i >= 0 {
			s = s[:i+1]
		}
	}

	return s
}

// usesViewPages reports whether a Go source of the project calls
// WithViewPages; only those apps have anything for view:check to verify,
// and an app without it would just start its server for the command.
func usesViewPages(dir string) bool {
	found := false

	_ = walkSources(dir, func(p string, info fs.FileInfo) {
		if found || !strings.HasSuffix(p, ".go") || info.Size() > 1<<20 {
			return
		}

		if src, err := os.ReadFile(p); err == nil && bytes.Contains(src, []byte("WithViewPages(")) {
			found = true
		}
	})

	return found
}
