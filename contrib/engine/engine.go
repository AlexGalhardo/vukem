// Package engine runs an external binary the way a server has to: with
// an environment it built rather than inherited, a working directory the
// tool cannot reach out of, bounded output, resource limits, and a
// cancellation that takes the whole process tree with it.
//
// It exists because an application that shells out to ffmpeg, yt-dlp,
// Ghostscript or ImageMagick needs every one of those, and six of them
// are traps rather than preferences.
//
// # 1. The child must not inherit the server's environment
//
// os.Environ() holds the database URL, the bucket credentials and the
// application key. A tool that is handed them can leak them through its
// own diagnostics, its crash reports, or a format string an attacker
// controls. [Tool.Run] builds the environment from nothing: PATH (so the
// tool can find its own helpers), the platform's own minimum
// (SystemRoot and windir on Windows, without which most binaries refuse
// to start), LANG and LC_ALL when the host has them, HOME and the temp
// variables below. Anything else is [Config.Env], so adding one is a
// visible decision in the application's code.
//
// # 2. Stripping the environment is not enough: pin the temp directory
//
// A tool's sandbox has to include where it scratches, so TMPDIR, TEMP and
// TMP are set to the run's own directory alongside HOME.
//
// This one was found by a test written to fail, and measured here rather
// than taken on trust. The same hostile PostScript document, the same
// -dSAFER, one file written by the document to an absolute /tmp path:
//
//	Ghostscript 10.05.1, TMPDIR=/tmp (the default)
//	  wrote /tmp/escaped.txt and reported success
//	Ghostscript 10.05.1, TMPDIR pinned to the run's directory
//	  refused: "Last OS error: Permission denied", exit 1
//	Ghostscript 10.08.0, either way
//	  refused
//
// So -dSAFER is not inert: on 10.05.1 it permits writes under whatever
// TMPDIR names, and TMPDIR defaults to /tmp, which made the whole tree
// writable by a document. 10.08.0 fixed that, and pinning TMPDIR is what
// made the difference on the version that did not.
//
// Do not read this as a Ghostscript workaround that expired. The pinning
// is worth having whatever the tool and whatever its version: ffmpeg's
// muxers, yt-dlp's fragments and ImageMagick's scratch files all land in
// the temp directory, so pinning it keeps a run's temporary files inside
// the directory the caller is about to delete, keeps two concurrent runs
// from colliding on a shared name in /tmp, and means a tool that writes
// where it should not is contained by default rather than by a flag the
// next release might reinterpret.
//
// # 3. Resource limits have to go through prlimit(1)
//
// syscall.Setrlimit applies to the *caller*, and Go exposes no hook
// between fork and exec, so a limit set in the parent would bound the
// server instead of the tool. The only correct route is prlimit(1) from
// util-linux, which applies the limits and then execs. [Find] resolves it
// when [Config.Limits] is set and **refuses** when it cannot: on a
// platform without it, silently unlimited execution is the outcome worth
// refusing. Limits are Linux-only for that reason; everything else in
// this package applies everywhere, so an unlimited Tool works on Windows
// and macOS.
//
// # 4. Cancellation must kill the process group
//
// ffmpeg and yt-dlp spawn children. Killing the parent leaves them
// running, holding the working directory open — which on Windows also
// blocks the directory's removal, so a test's t.TempDir cleanup fails
// after the test passed. Every run starts its own process group and
// cancellation kills the group.
//
// # 5. The shape for that is Cmd.Cancel and Cmd.WaitDelay
//
// Not a watcher goroutine. exec.CommandContext with Cmd.Cancel killing the
// group and Cmd.WaitDelay bounding the wait for the pipes is the whole
// mechanism, and it is what keeps the context on the call rather than in a
// goroutine that outlives it.
//
// # 6. Captured output must be bounded
//
// A tool that prints a progress line every 200 ms for a 40-minute
// transcode produces megabytes on stderr. [Tool.Run] keeps a tail
// ([Config.MaxOutput], default [DefaultMaxOutput]) rather than everything,
// so a long run cannot exhaust the worker holding its own log. When the
// tool's *output* is the product — a video on stdout — use
// [Tool.RunStream], which never buffers it.
//
// # Threads are the caller's to place
//
// [Tool.CPUCount] reports the CPU budget a run may use, and this package
// never puts it in the arguments. It cannot: -threads is positional in
// ffmpeg (before an input it bounds that input's decoder, after the inputs
// it bounds the encoder, and the wrong one gives "at least one of its
// streams received no packets" with no cause named), and SVT-AV1 ignores
// -threads entirely and needs -svtav1-params lp= instead. A package that
// injected -threads would be wrong for both tools.
//
// Set [Config.CPUCount] from vukem/cpu's Count, which reads the container's
// cgroup quota. Do not use runtime.GOMAXPROCS for it: it rounds the quota
// up and never drops below 2, so on a 0.5-CPU quota it is four times the
// limit.
package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// DefaultMaxOutput is the tail of stdout and stderr [Tool.Run] keeps when
// [Config.MaxOutput] is zero.
const DefaultMaxOutput = 64 << 10

// DefaultTimeout bounds a run when [Config.Timeout] is zero and the
// caller's context has no deadline. A subprocess with neither is a hung
// worker nobody notices.
const DefaultTimeout = 5 * time.Minute

// Errors a caller distinguishes. ErrNotFound and ErrLimitsUnsupported are
// boot-time problems; the rest belong to a run.
var (
	// ErrNotFound reports a binary that could not be resolved.
	ErrNotFound = errors.New("engine: binary not found")
	// ErrLimitsUnsupported reports that Limits were asked for where they
	// cannot be applied: a platform other than Linux, or a Linux host
	// without prlimit(1) from util-linux. Refused rather than ignored,
	// because the alternative is unlimited execution that looks limited.
	ErrLimitsUnsupported = errors.New("engine: resource limits need prlimit(1) from util-linux on Linux")
	// ErrRun reports a tool that exited non-zero, was killed, or could not
	// be started. The Result returned alongside holds the captured output.
	ErrRun = errors.New("engine: run failed")
	// ErrDir reports a working directory that is missing or not a
	// directory. Every run needs one: it is the tool's HOME, its temp
	// directory and the only place it should be writing.
	ErrDir = errors.New("engine: working directory unusable")
)

// Limits bound one run. They are applied by prlimit(1), so they are
// Linux-only; a zero field is not limited by this package. See trap 3 in
// the package comment for why they cannot be set from Go.
type Limits struct {
	// CPUTime is RLIMIT_CPU, rounded up to whole seconds. The kernel
	// sends SIGKILL when it is exceeded, so it is the backstop for a tool
	// that ignores its own timeout.
	CPUTime time.Duration
	// AddressSpace is RLIMIT_AS in bytes: the ceiling on a decoder handed
	// a file that claims enormous dimensions.
	AddressSpace int64
	// FileSize is RLIMIT_FSIZE in bytes: the ceiling on what one run can
	// write, whatever the tool thinks it is producing.
	FileSize int64
	// Processes is RLIMIT_NPROC: the ceiling on threads and children.
	// Note that it counts per real user, not per process tree, so a
	// server running several tools shares the budget.
	Processes int
}

// zero reports whether no limit is set.
func (l Limits) zero() bool {
	return l.CPUTime <= 0 && l.AddressSpace <= 0 && l.FileSize <= 0 && l.Processes <= 0
}

// args are the prlimit(1) options for these limits.
func (l Limits) args() []string {
	var out []string

	if l.CPUTime > 0 {
		// Rounded up: a sub-second limit would be zero, which prlimit
		// reads as "no CPU time at all".
		secs := int64((l.CPUTime + time.Second - 1) / time.Second)
		out = append(out, "--cpu="+strconv.FormatInt(secs, 10))
	}

	if l.AddressSpace > 0 {
		out = append(out, "--as="+strconv.FormatInt(l.AddressSpace, 10))
	}

	if l.FileSize > 0 {
		out = append(out, "--fsize="+strconv.FormatInt(l.FileSize, 10))
	}

	if l.Processes > 0 {
		out = append(out, "--nproc="+strconv.Itoa(l.Processes))
	}

	return out
}

// Config describes an external binary and how runs of it are bounded.
type Config struct {
	// Name is the binary to resolve on PATH ("ffmpeg"). Required unless
	// Path is set.
	Name string
	// Path is an absolute path from configuration, used instead of the
	// PATH lookup.
	Path string
	// VersionArgs reads the tool's version at Find time (default
	// "-version"); nil skips it. The probe reads **stderr as well as
	// stdout** and takes the first non-blank line, because poppler's
	// pdftotext and pdftoppm answer -v on stderr with exit 0 and a
	// stdout-only probe logs their version as "unknown" while they work
	// perfectly.
	VersionArgs []string
	// Env are extra variables for every run, on top of the minimal set
	// the package builds. Each one is a deliberate widening of what the
	// child can see (trap 1).
	Env map[string]string
	// Limits bound every run through prlimit(1) (Linux only).
	Limits Limits
	// CPUCount is the CPU budget a run may use, reported by
	// Tool.CPUCount and never placed in the arguments by this package.
	// Set it from vukem/cpu's Count.
	CPUCount int
	// Timeout bounds a run whose context has no deadline
	// (DefaultTimeout when zero).
	Timeout time.Duration
	// MaxOutput is the tail of stdout and stderr Run keeps
	// (DefaultMaxOutput when zero).
	MaxOutput int
	// Log receives the resolved path and version at Find time, so a boot
	// log records which build of the tool is in the image.
	Log *slog.Logger
}

// Tool is a resolved external binary, safe to share between goroutines:
// every run builds its own command, environment and limits.
type Tool struct {
	cfg     Config
	path    string
	prlimit string
	version string
}

// Find resolves the binary, applies the defaults, reads the version and
// logs it. It is a boot call: a missing tool, or limits that cannot be
// applied here, should stop the process rather than surface on the first
// request.
func Find(ctx context.Context, cfg Config) (*Tool, error) {
	if cfg.Name == "" && cfg.Path == "" {
		return nil, fmt.Errorf("%w: Config needs Name or Path", ErrNotFound)
	}

	if cfg.MaxOutput <= 0 {
		cfg.MaxOutput = DefaultMaxOutput
	}

	if cfg.Timeout <= 0 {
		cfg.Timeout = DefaultTimeout
	}

	if cfg.VersionArgs == nil {
		cfg.VersionArgs = []string{"-version"}
	}

	path, err := resolve(cfg)
	if err != nil {
		return nil, err
	}

	t := &Tool{cfg: cfg, path: path}

	if !cfg.Limits.zero() {
		if runtime.GOOS != "linux" {
			return nil, fmt.Errorf("%w (this is %s)", ErrLimitsUnsupported, runtime.GOOS)
		}

		if t.prlimit, err = exec.LookPath("prlimit"); err != nil {
			return nil, fmt.Errorf("%w: %w", ErrLimitsUnsupported, err)
		}
	}

	t.version = t.probeVersion(ctx)

	if cfg.Log != nil {
		cfg.Log.InfoContext(ctx, "external tool resolved",
			"tool", cfg.Name, "path", path, "version", t.version, "limits", !cfg.Limits.zero())
	}

	return t, nil
}

// resolve finds the binary: an explicit Path as given, otherwise a PATH
// lookup of Name.
func resolve(cfg Config) (string, error) {
	if cfg.Path != "" {
		info, err := os.Stat(cfg.Path)
		if err != nil {
			return "", fmt.Errorf("%w: %s: %w", ErrNotFound, cfg.Path, err)
		}

		if info.IsDir() {
			return "", fmt.Errorf("%w: %s is a directory", ErrNotFound, cfg.Path)
		}

		return cfg.Path, nil
	}

	path, err := exec.LookPath(cfg.Name)
	if err != nil {
		return "", fmt.Errorf("%w: %q: %w", ErrNotFound, cfg.Name, err)
	}

	return path, nil
}

// Path is where the binary was resolved.
func (t *Tool) Path() string { return t.path }

// Version is the first non-blank line the version probe printed, on
// either stream, or "unknown".
func (t *Tool) Version() string { return t.version }

// CPUCount is the CPU budget a run may use, from Config.CPUCount. Place
// it in the arguments yourself: see the package comment for why this
// package will not.
func (t *Tool) CPUCount() int { return t.cfg.CPUCount }

// Result is what one run produced.
type Result struct {
	// Stdout and Stderr are the **tail** of each stream, up to
	// Config.MaxOutput. Stdout is empty for RunStream.
	Stdout, Stderr string
	// ExitCode is the process's exit code (-1 when it never ran or was
	// killed by a signal).
	ExitCode int
	// Duration is how long the run took.
	Duration time.Duration
}

// Run executes the tool in dir and captures the tail of both streams.
// dir is the run's working directory, HOME and temp directory, so give
// each run its own and delete it afterwards.
//
// A non-zero exit is an error wrapping [ErrRun]; the Result is returned
// either way, so a caller can log the tail of stderr on failure.
func (t *Tool) Run(ctx context.Context, dir string, args ...string) (Result, error) {
	out := &tailWriter{max: t.cfg.MaxOutput}

	res, err := t.run(ctx, dir, out, args)
	res.Stdout = out.String()

	return res, err
}

// RunStream executes the tool in dir and copies its standard output to w
// as it arrives, so a tool whose product is its stdout — a video, an
// archive — never fills the worker's memory. Stderr is still captured as
// a tail, because that is where these tools explain themselves.
func (t *Tool) RunStream(ctx context.Context, dir string, w io.Writer, args ...string) (Result, error) {
	if w == nil {
		return Result{}, fmt.Errorf("%w: RunStream needs a writer", ErrRun)
	}

	return t.run(ctx, dir, w, args)
}

func (t *Tool) run(ctx context.Context, dir string, stdout io.Writer, args []string) (Result, error) {
	if err := checkDir(dir); err != nil {
		return Result{}, err
	}

	// Only when the caller has not bounded the run itself, so a caller's
	// shorter deadline is never widened.
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc

		ctx, cancel = context.WithTimeout(ctx, t.cfg.Timeout)
		defer cancel()
	}

	name, full := t.path, args
	if t.prlimit != "" {
		// prlimit applies the limits and then execs the tool, which is
		// the only point at which they bound the tool and not the server.
		name = t.prlimit
		full = append(append(t.cfg.Limits.args(), "--", t.path), args...)
	}

	stderr := &tailWriter{max: t.cfg.MaxOutput}

	cmd := exec.CommandContext(ctx, name, full...)
	cmd.Dir = dir
	cmd.Env = t.environ(dir)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	// Nothing to read: a tool waiting on stdin would otherwise hang until
	// the timeout rather than failing at once.
	cmd.Stdin = nil

	setProcessGroup(cmd)

	// Cancel kills the group, not just the parent: ffmpeg and yt-dlp
	// spawn children that keep the working directory open. WaitDelay
	// bounds the wait for the pipes after that, so a child holding stderr
	// open cannot keep Wait from returning.
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = 5 * time.Second

	start := time.Now()
	err := cmd.Run()
	res := Result{Stderr: stderr.String(), ExitCode: cmd.ProcessState.ExitCode(), Duration: time.Since(start)}

	if err == nil {
		return res, nil
	}

	// The tail of stderr is what explains the failure; the exit code
	// alone rarely does.
	return res, fmt.Errorf("%w: %s exited %d after %s: %w",
		ErrRun, t.cfg.Name, res.ExitCode, res.Duration.Round(time.Millisecond), err)
}

// checkDir refuses a working directory that is missing or is a file: the
// tool would start, fail obscurely, and write wherever it happened to be.
func checkDir(dir string) error {
	if dir == "" {
		return fmt.Errorf("%w: a run needs its own directory", ErrDir)
	}

	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrDir, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("%w: %s is not a directory", ErrDir, dir)
	}

	return nil
}

// inherited are the only host variables a run sees. PATH lets the tool
// find its own helpers (yt-dlp calls ffmpeg); SystemRoot and windir exist
// only on Windows, where most binaries refuse to start without them; LANG
// and LC_ALL keep a tool's number formatting predictable. None of them
// carries an application secret.
var inherited = []string{"PATH", "SystemRoot", "windir", "LANG", "LC_ALL"} //nolint:gochecknoglobals // read-only

// environ builds the child's environment from nothing. See traps 1 and 2.
func (t *Tool) environ(dir string) []string {
	env := make([]string, 0, len(inherited)+4+len(t.cfg.Env))

	for _, k := range inherited {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}

	// HOME and the temp triple all point at the run's own directory: a
	// tool that scratches, caches or writes a config lands inside the
	// directory the caller is about to delete, and -dSAFER's permitted
	// temp writes go there instead of /tmp.
	env = append(env,
		"HOME="+dir,
		"TMPDIR="+dir,
		"TEMP="+dir,
		"TMP="+dir,
	)

	for k, v := range t.cfg.Env {
		env = append(env, k+"="+v)
	}

	return env
}

// probeVersion runs the version arguments and returns the first non-blank
// line of either stream. A probe that fails is not fatal: the tool may
// not answer the flag, and the version is for the log.
func (t *Tool) probeVersion(ctx context.Context) string {
	if len(t.cfg.VersionArgs) == 0 {
		return "unknown"
	}

	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var out, errOut strings.Builder

	// G204 is reported here and not on the identical launch in run(),
	// where the same t.path and a caller's args reach exec through two
	// locals: the rule is matching the spelling, not a flow. Both
	// launches take the binary this package resolved and arguments the
	// application wrote in its own source, which is this package's
	// entire contract; no request data reaches either, and a
	// client-supplied file name becomes a bounded path through
	// Sanitized rather than an argument.
	cmd := exec.CommandContext(ctx, t.path, t.cfg.VersionArgs...) //nolint:gosec // G204: see above
	cmd.Stdout, cmd.Stderr = &out, &errOut
	cmd.Env = t.environ(os.TempDir())
	setProcessGroup(cmd)

	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = time.Second

	_ = cmd.Run() // a tool that refuses the flag still has a version

	// Both streams, stdout first: poppler answers -v on stderr with exit
	// 0, so a stdout-only probe records "unknown" for a working tool.
	if v := firstLine(out.String()); v != "" {
		return v
	}

	if v := firstLine(errOut.String()); v != "" {
		return v
	}

	return "unknown"
}

// firstLine is the first line with anything on it, trimmed.
func firstLine(s string) string {
	for line := range strings.Lines(s) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}

	return ""
}

// tailWriter keeps the last max bytes written to it. A tool that prints a
// progress line every 200 ms for forty minutes would otherwise hold
// megabytes per run in the worker (trap 6).
type tailWriter struct {
	max  int
	buf  []byte
	over bool
}

func (w *tailWriter) Write(p []byte) (int, error) {
	n := len(p)

	if len(p) >= w.max {
		w.buf = append(w.buf[:0], p[len(p)-w.max:]...)
		w.over = true

		return n, nil
	}

	if len(w.buf)+len(p) > w.max {
		drop := len(w.buf) + len(p) - w.max
		w.buf = w.buf[drop:]
		w.over = true
	}

	w.buf = append(w.buf, p...)

	return n, nil
}

// String is the tail, with a marker when anything was dropped so a reader
// is never shown a truncated log as if it were whole.
func (w *tailWriter) String() string {
	if !w.over {
		return string(w.buf)
	}

	return "[earlier output dropped]\n" + string(w.buf)
}

// Sanitized is dir joined with name, refusing anything that leaves dir.
// A tool is told the names of its own input and output files, and those
// names often come from a client: this keeps one from naming a path
// outside the run.
func Sanitized(dir, name string) (string, error) {
	if name == "" || strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		return "", fmt.Errorf("%w: %q is not a plain file name", ErrDir, name)
	}

	return filepath.Join(dir, name), nil
}
