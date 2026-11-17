package engine_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/AlexGalhardo/vukem/contrib/engine"
)

// faketool builds testdata/faketool and returns its path. A real binary,
// started as a real child, so the environment, the working directory, the
// temp pinning, the output bounds and the cancellation are exercised
// against a process rather than a mock.
func faketool(t *testing.T) string {
	t.Helper()

	if testing.Short() {
		t.Skip("builds and runs a helper binary")
	}

	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not on PATH")
	}

	name := "faketool"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	// Built once per test into its own directory: a shared one would
	// race under t.Parallel.
	bin := filepath.Join(t.TempDir(), name)

	cmd := exec.CommandContext(t.Context(), "go", "build", "-o", bin, "./testdata/faketool")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build faketool: %v\n%s", err, out)
	}

	return bin
}

func find(t *testing.T, cfg engine.Config) *engine.Tool {
	t.Helper()

	if cfg.Path == "" {
		cfg.Path = faketool(t)
	}

	tool, err := engine.Find(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}

	return tool
}

// Trap 1: the child must not inherit the server's environment. The
// database URL and the bucket credentials live there, and a tool handed
// them can leak them through its own diagnostics.
func TestRunDoesNotInheritTheServerEnvironment(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{
		Name: "faketool",
		Env:  map[string]string{"ENGINE_TEST_EXTRA": "1"},
	})
	dir := t.TempDir()

	res, err := tool.Run(t.Context(), dir, "env")
	if err != nil {
		t.Fatalf("Run() = %v\n%s", err, res.Stderr)
	}

	// Asserted as a subset rather than by planting a secret: the test
	// process is the server here, and it already carries plenty the child
	// must not see (GOPATH, USERPROFILE, HOSTNAME, whatever CI injects).
	// A subset check catches every one of them, including ones nobody
	// thought to plant.
	allowed := map[string]bool{
		"PATH": true, "SystemRoot": true, "windir": true, "LANG": true, "LC_ALL": true,
		"HOME": true, "TMPDIR": true, "TEMP": true, "TMP": true,
		"ENGINE_TEST_EXTRA": true,
	}

	var leaked []string

	for line := range strings.Lines(res.Stdout) {
		key, _, ok := strings.Cut(strings.TrimRight(line, "\n"), "=")
		if !ok || key == "" {
			continue
		}

		if !allowed[key] {
			leaked = append(leaked, key)
		}
	}

	if len(leaked) > 0 {
		t.Errorf("the child saw %d variables it should not: %v", len(leaked), leaked)
	}

	// The host really does have more than that, so the check above is not
	// passing on an empty environment.
	if len(os.Environ()) <= len(allowed) {
		t.Skip("this host's environment is too small for the subset check to mean anything")
	}

	// PATH is deliberately inherited, so the tool can find its helpers.
	if !strings.Contains(res.Stdout, "PATH=") {
		t.Errorf("the child has no PATH:\n%s", res.Stdout)
	}
}

// Trap 2, the one found by a test written to fail: -dSAFER permits
// writing to the temp directory, and TMPDIR defaults to /tmp, so
// Ghostscript wrote /tmp/elsewhere/escaped.txt and reported success.
// Pinning the temp variables to the run's directory is what makes the
// sandbox include where the tool scratches.
func TestRunPinsTheTempDirectoryToTheRun(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool"})
	dir := t.TempDir()

	res, err := tool.Run(t.Context(), dir, "env")
	if err != nil {
		t.Fatalf("Run() = %v\n%s", err, res.Stderr)
	}

	for _, key := range []string{"TMPDIR", "TEMP", "TMP", "HOME"} {
		want := key + "=" + dir
		if !strings.Contains(res.Stdout, want) {
			t.Errorf("%s is not the run's directory; want %q in:\n%s", key, want, res.Stdout)
		}
	}

	// And a tool that actually writes to its temp directory lands inside
	// the run, not in the system one.
	res, err = tool.Run(t.Context(), dir, "write-temp")
	if err != nil {
		t.Fatalf("Run(write-temp) = %v\n%s", err, res.Stderr)
	}

	wrote := strings.TrimSpace(res.Stdout)
	if !strings.HasPrefix(wrote, dir) {
		t.Errorf("the tool wrote %q, outside the run's directory %q", wrote, dir)
	}

	if _, err := os.Stat(filepath.Join(dir, "scratch.txt")); err != nil {
		t.Errorf("scratch file is not in the run's directory: %v", err)
	}
}

// Trap 3: limits are refused where they cannot be applied, because
// unlimited execution that looks limited is the outcome worth refusing.
func TestLimitsAreRefusedWhereTheyCannotApply(t *testing.T) {
	t.Parallel()

	cfg := engine.Config{
		Path:   faketool(t),
		Name:   "faketool",
		Limits: engine.Limits{CPUTime: 10 * time.Second, AddressSpace: 1 << 30},
	}

	_, err := engine.Find(t.Context(), cfg)

	switch runtime.GOOS {
	case "linux":
		// Only prlimit's absence can refuse it here.
		if _, look := exec.LookPath("prlimit"); look != nil {
			if !errors.Is(err, engine.ErrLimitsUnsupported) {
				t.Fatalf("Find() without prlimit = %v, want ErrLimitsUnsupported", err)
			}

			return
		}

		if err != nil {
			t.Fatalf("Find() with prlimit present = %v", err)
		}
	case "windows":
		// A Job Object carries CPUTime and AddressSpace natively.
		if err != nil {
			t.Fatalf("Find() on windows = %v, want the limits applied", err)
		}
	default:
		if !errors.Is(err, engine.ErrLimitsUnsupported) {
			t.Fatalf("Find() on %s = %v, want ErrLimitsUnsupported", runtime.GOOS, err)
		}

		if !strings.Contains(err.Error(), runtime.GOOS) {
			t.Errorf("the refusal does not name the platform: %v", err)
		}
	}

	// A Tool without limits works everywhere, so the whole package is not
	// Linux-only.
	tool := find(t, engine.Config{Name: "faketool"})
	if _, err := tool.Run(t.Context(), t.TempDir(), "cwd"); err != nil {
		t.Errorf("an unlimited run failed on %s: %v", runtime.GOOS, err)
	}
}

// Traps 4 and 5: a cancelled run returns promptly, through Cmd.Cancel and
// Cmd.WaitDelay rather than a watcher goroutine.
func TestRunStopsWhenTheContextIsCancelled(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool"})

	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()

	start := time.Now()

	_, err := tool.Run(ctx, t.TempDir(), "sleep", "30s")
	if err == nil {
		t.Fatal("Run() = nil for a cancelled run")
	}

	if !errors.Is(err, engine.ErrRun) {
		t.Errorf("Run() = %v, want ErrRun", err)
	}

	// Well inside Cmd.WaitDelay, and nowhere near the tool's 30s.
	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("a cancelled run took %s to return", took)
	}
}

// Trap 6: captured output is a tail, so a tool printing a progress line
// every 200 ms for forty minutes cannot exhaust the worker holding its
// own log.
func TestRunBoundsCapturedOutput(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool", MaxOutput: 2 << 10})

	res, err := tool.Run(t.Context(), t.TempDir(), "spew", "5000")
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}

	for name, got := range map[string]string{"stdout": res.Stdout, "stderr": res.Stderr} {
		// The marker is part of the budget, so allow for it.
		if len(got) > (2<<10)+64 {
			t.Errorf("%s kept %d bytes, want a 2 KiB tail", name, len(got))
		}

		// A truncated log must never read as a whole one.
		if !strings.HasPrefix(got, "[earlier output dropped]") {
			t.Errorf("%s does not say output was dropped: %.80q", name, got)
		}

		// It is the **tail**: the last lines, not the first.
		if !strings.Contains(got, "4999") {
			t.Errorf("%s is not the tail of the stream: %.120q", name, got)
		}
	}

	if !res.StdoutTruncated || !res.StderrTruncated {
		t.Errorf("Truncated = stdout %v, stderr %v, want both true", res.StdoutTruncated, res.StderrTruncated)
	}
}

// A short run is not flagged, so StdoutTruncated is safe to branch on.
func TestRunDoesNotFlagShortOutput(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool", MaxOutput: 64 << 10})

	res, err := tool.Run(t.Context(), t.TempDir(), "spew", "5")
	if err != nil {
		t.Fatalf("Run() = %v", err)
	}

	if res.StdoutTruncated || res.StderrTruncated {
		t.Errorf("Truncated = stdout %v, stderr %v, want both false", res.StdoutTruncated, res.StderrTruncated)
	}
}

// And the streaming form never buffers the product at all.
func TestRunStreamDoesNotBufferStdout(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool", MaxOutput: 1 << 10})

	var got bytes.Buffer

	res, err := tool.RunStream(t.Context(), t.TempDir(), &got, "stream", "512")
	if err != nil {
		t.Fatalf("RunStream() = %v\n%s", err, res.Stderr)
	}

	if got.Len() != 512*1024 {
		t.Errorf("streamed %d bytes, want %d", got.Len(), 512*1024)
	}

	if res.Stdout != "" {
		t.Errorf("RunStream kept stdout as well: %.80q", res.Stdout)
	}

	if _, err := tool.RunStream(t.Context(), t.TempDir(), nil, "cwd"); err == nil {
		t.Error("RunStream(nil writer) = nil, want an error")
	}
}

func TestRunStreamsDeliversStderrLive(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool"})

	var out, live bytes.Buffer

	res, err := tool.RunStreams(t.Context(), t.TempDir(), &out, &live, "spew", "3")
	if err != nil {
		t.Fatalf("RunStreams() = %v\n%s", err, res.Stderr)
	}

	const want = "err 0\nerr 1\nerr 2\n"
	if got := strings.ReplaceAll(live.String(), "\r\n", "\n"); got != want {
		t.Errorf("live stderr = %q, want %q", got, want)
	}

	if got := strings.ReplaceAll(res.Stderr, "\r\n", "\n"); got != want {
		t.Errorf("Result.Stderr = %q, want the tail as well: %q", got, want)
	}

	if !strings.Contains(out.String(), "out 2") {
		t.Errorf("stdout = %q, want the tool output", out.String())
	}

	if _, err := tool.RunStreams(t.Context(), t.TempDir(), &out, nil, "cwd"); err == nil {
		t.Error("RunStreams(nil stderr) = nil, want an error")
	}
}

// The version probe reads stderr as well as stdout and takes the first
// non-blank line: poppler's pdftotext answers -v on stderr with exit 0,
// so a stdout-only probe logs "unknown" for a tool that works perfectly.
func TestFindReadsTheVersionFromEitherStream(t *testing.T) {
	t.Parallel()

	bin := faketool(t)

	for name, tt := range map[string]struct {
		args []string
		want string
	}{
		"stdout, like ffmpeg":     {[]string{"-version"}, "faketool version 1.2.3"},
		"stderr, like poppler":    {[]string{"-version-stderr"}, "faketool stderr 4.5.6"},
		"a flag it does not know": {[]string{"-nope"}, "faketool: unknown mode"},
		"probe skipped":           {[]string{}, "unknown"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tool := find(t, engine.Config{Name: "faketool", Path: bin, VersionArgs: tt.args})
			if got := tool.Version(); !strings.Contains(got, tt.want) {
				t.Errorf("Version() = %q, want it to contain %q", got, tt.want)
			}
		})
	}
}

// A missing tool is a boot error, not a first-request one.
func TestFindRefusesAMissingTool(t *testing.T) {
	t.Parallel()

	for name, cfg := range map[string]engine.Config{
		"not on PATH":   {Name: "vukem-no-such-tool-exists"},
		"missing path":  {Path: filepath.Join(t.TempDir(), "nope")},
		"a directory":   {Path: t.TempDir()},
		"nothing given": {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := engine.Find(t.Context(), cfg); !errors.Is(err, engine.ErrNotFound) {
				t.Fatalf("Find(%+v) = %v, want ErrNotFound", cfg, err)
			}
		})
	}
}

// A run needs its own directory: the tool's HOME, temp directory and the
// only place it should write.
func TestRunRefusesAnUnusableDirectory(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool"})

	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	for name, dir := range map[string]string{
		"empty":   "",
		"missing": filepath.Join(t.TempDir(), "nope"),
		"a file":  file,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if _, err := tool.Run(t.Context(), dir, "cwd"); !errors.Is(err, engine.ErrDir) {
				t.Fatalf("Run(%q) = %v, want ErrDir", dir, err)
			}
		})
	}

	// And the run really happens in the directory it was given.
	dir := t.TempDir()

	res, err := tool.Run(t.Context(), dir, "cwd")
	if err != nil {
		t.Fatal(err)
	}

	// Compared through EvalSymlinks: macOS resolves /var to /private/var.
	got, err := filepath.EvalSymlinks(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatal(err)
	}

	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	if got != want {
		t.Errorf("the run happened in %q, want %q", got, want)
	}
}

// A non-zero exit is an error, and the Result still carries the tail of
// stderr, which is where these tools explain themselves.
func TestRunReportsAFailureWithItsOutput(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool"})

	res, err := tool.Run(t.Context(), t.TempDir(), "fail", "3")
	if !errors.Is(err, engine.ErrRun) {
		t.Fatalf("Run() = %v, want ErrRun", err)
	}

	if res.ExitCode != 3 {
		t.Errorf("ExitCode = %d, want 3", res.ExitCode)
	}

	if !strings.Contains(res.Stderr, "deliberate failure") {
		t.Errorf("Stderr = %q, want the tool's own message", res.Stderr)
	}

	if !strings.Contains(err.Error(), "exited 3") {
		t.Errorf("error does not name the exit code: %v", err)
	}
}

// Config.Env is the only way to widen what the child sees, so each
// addition is visible in the application's code.
func TestConfigEnvIsTheOnlyWidening(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{
		Name: "faketool",
		Env:  map[string]string{"VIPS_CONCURRENCY": "2"},
	})

	res, err := tool.Run(t.Context(), t.TempDir(), "env")
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(res.Stdout, "VIPS_CONCURRENCY=2") {
		t.Errorf("Config.Env did not reach the child:\n%s", res.Stdout)
	}
}

// The CPU budget is reported and never placed in the arguments: -threads
// is positional in ffmpeg and SVT-AV1 ignores it entirely, so a package
// that injected it would be wrong for both.
func TestCPUCountIsReportedAndNeverInjected(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool", CPUCount: 3})

	if got := tool.CPUCount(); got != 3 {
		t.Errorf("CPUCount() = %d, want 3", got)
	}

	res, err := tool.Run(t.Context(), t.TempDir(), "env")
	if err != nil {
		t.Fatal(err)
	}

	for _, injected := range []string{"-threads", "THREADS=", "lp="} {
		if strings.Contains(res.Stdout, injected) {
			t.Errorf("the package injected %q:\n%s", injected, res.Stdout)
		}
	}
}

// Sanitized keeps a client-supplied file name inside the run.
func TestSanitized(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	got, err := engine.Sanitized(dir, "out.mp4")
	if err != nil || got != filepath.Join(dir, "out.mp4") {
		t.Fatalf("Sanitized(ok) = %q, %v", got, err)
	}

	for _, name := range []string{"", ".", "..", "../out.mp4", "sub/out.mp4", `..\out.mp4`} {
		if _, err := engine.Sanitized(dir, name); !errors.Is(err, engine.ErrDir) {
			t.Errorf("Sanitized(%q) = %v, want ErrDir", name, err)
		}
	}
}

// The version probe runs in a private directory under Config.ProbeDir, not
// in the shared system temp directory, and leaves nothing behind.
func TestVersionProbeRunsInItsOwnDirectory(t *testing.T) {
	t.Parallel()

	parent := t.TempDir()
	tool := find(t, engine.Config{Name: "faketool", VersionArgs: []string{"cwd"}, ProbeDir: parent})

	probe := tool.Version()

	pi, err := os.Stat(filepath.Dir(probe))
	if err != nil {
		t.Fatalf("probe ran in %q, which cannot be read: %v", probe, err)
	}

	want, err := os.Stat(parent)
	if err != nil {
		t.Fatal(err)
	}

	if !os.SameFile(pi, want) {
		t.Errorf("probe ran in %q, want a directory inside %q", probe, parent)
	}

	entries, err := os.ReadDir(parent)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 0 {
		t.Errorf("probe left %d entries in %q, want none", len(entries), parent)
	}
}

// Prepare is the caller's hook on the command of every run, for limits this
// package cannot apply on every system.
func TestRunCallsPrepareWithTheFinishedCommand(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	var gotDir string

	tool := find(t, engine.Config{Name: "faketool", Prepare: func(cmd *exec.Cmd) {
		gotDir = cmd.Dir
		cmd.Env = append(cmd.Env, "PREPARED=yes")
	}})

	res, err := tool.Run(t.Context(), dir, "env")
	if err != nil {
		t.Fatalf("Run() = %v: %s", err, res.Stderr)
	}

	if gotDir != dir {
		t.Errorf("Prepare saw Dir %q, want the run directory %q", gotDir, dir)
	}

	if !strings.Contains(res.Stdout, "PREPARED=yes") {
		t.Errorf("the tool did not see the variable Prepare added: %s", res.Stdout)
	}
}

// patternReader yields n bytes of a repeating pattern in small chunks, so the
// copy to the child takes many writes.
type patternReader struct{ left int }

func (r *patternReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}

	n := min(len(p), r.left, 4096)
	for i := range n {
		p[i] = byte(i)
	}

	r.left -= n

	return n, nil
}

// waitGoroutines fails when the number of goroutines does not settle back to
// at most base within a few seconds.
func waitGoroutines(t *testing.T, base int) {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for runtime.NumGoroutine() > base {
		if time.Now().After(deadline) {
			t.Fatalf("goroutines = %d, want at most %d: a run leaked", runtime.NumGoroutine(), base)
		}

		time.Sleep(20 * time.Millisecond)
	}
}

// VF-031: ffmpeg reads Go-composed frames from pipe:0, so the input is
// per run, the child sees its end, and the output still comes back.
func TestRunWithFeedsTheChildStandardInput(t *testing.T) {
	tool := find(t, engine.Config{Name: "faketool"})
	base := runtime.NumGoroutine()

	const size = 5<<20 + 17 // several pipe buffers, not a round number

	res, err := tool.RunWith(t.Context(), engine.RunOptions{
		Dir:   t.TempDir(),
		Stdin: &patternReader{left: size},
	}, "stdin-len")
	if err != nil {
		t.Fatalf("RunWith() = %v\n%s", err, res.Stderr)
	}

	if got, want := strings.TrimSpace(res.Stdout), strconv.Itoa(size); got != want {
		t.Errorf("RunWith(stdin %d bytes) stdout = %q, want %q", size, got, want)
	}

	waitGoroutines(t, base)
}

func TestRunWithOSFileStdinAndStdout(t *testing.T) {
	tool := find(t, engine.Config{Name: "faketool"})
	dir := t.TempDir()

	inPath := filepath.Join(dir, "in.bin")
	if err := os.WriteFile(inPath, []byte("abcdef"), 0o600); err != nil {
		t.Fatal(err)
	}

	in, err := os.Open(inPath)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = in.Close() })

	outPath := filepath.Join(dir, "out.txt")

	out, err := os.Create(outPath)
	if err != nil {
		t.Fatal(err)
	}

	res, err := tool.RunWith(t.Context(), engine.RunOptions{
		Dir: dir, Stdin: in, Stdout: out,
	}, "stdin-len")
	if err != nil {
		t.Fatalf("RunWith(*os.File) = %v\n%s", err, res.Stderr)
	}

	if err := out.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatal(err)
	}

	if strings.TrimSpace(string(got)) != "6" {
		t.Fatalf("stdout file = %q, want 6", got)
	}

	if res.Stdout != "" {
		t.Fatalf("Result.Stdout = %q, want empty when Stdout is an *os.File", res.Stdout)
	}
}

func TestSuspendedStartIsDocumented(t *testing.T) {
	t.Parallel()

	src, err := os.ReadFile("engine.go")
	if err != nil {
		t.Fatal(err)
	}

	text := string(src)
	for _, name := range []string{"func (t *Tool) Run(", "type Limits struct"} {
		i := strings.Index(text, name)
		if i < 0 {
			t.Fatalf("engine.go has no %s", name)
		}

		chunk := text[max(0, i-900):i]
		if !strings.Contains(chunk, "created suspended") || !strings.Contains(chunk, "first output byte") {
			t.Errorf("%s godoc = %q, want the suspended-start sentence", name, chunk)
		}
	}
}

func TestRunWithStreamsStdoutAndStderrAlongsideStdin(t *testing.T) {
	tool := find(t, engine.Config{Name: "faketool"})

	var out, live bytes.Buffer

	res, err := tool.RunWith(t.Context(), engine.RunOptions{
		Dir:    t.TempDir(),
		Stdin:  strings.NewReader("abcdef"),
		Stdout: &out,
		Stderr: &live,
	}, "stdin-len")
	if err != nil {
		t.Fatalf("RunWith() = %v\n%s", err, res.Stderr)
	}

	if got := strings.TrimSpace(out.String()); got != "6" {
		t.Errorf("streamed stdout = %q, want 6", got)
	}

	if res.Stdout != "" {
		t.Errorf("Result.Stdout = %q, want empty when Stdout is set", res.Stdout)
	}
}

func TestRunWithoutStdinStillGivesAnEmptyInput(t *testing.T) {
	tool := find(t, engine.Config{Name: "faketool"})

	res, err := tool.RunWith(t.Context(), engine.RunOptions{Dir: t.TempDir()}, "stdin-len")
	if err != nil || strings.TrimSpace(res.Stdout) != "0" {
		t.Errorf("RunWith(no stdin) = %q, %v; want 0 bytes and no error", res.Stdout, err)
	}
}

// A cancelled context ends the run and kills the tree even though the tool
// is still waiting for more input.
func TestRunWithStopsWhenTheContextIsCancelledMidStream(t *testing.T) {
	tool := find(t, engine.Config{Name: "faketool"})
	base := runtime.NumGoroutine()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Some bytes, then silence until the context is done: the tool never sees EOF.
	pr, pw := io.Pipe()

	go func() {
		_, _ = pw.Write([]byte("partial"))
		<-ctx.Done()
		_ = pw.CloseWithError(ctx.Err())
	}()

	time.AfterFunc(300*time.Millisecond, cancel)

	start := time.Now()

	_, err := tool.RunWith(ctx, engine.RunOptions{Dir: t.TempDir(), Stdin: pr}, "stdin-len")
	if !errors.Is(err, engine.ErrRun) {
		t.Fatalf("RunWith() = %v, want ErrRun", err)
	}

	if took := time.Since(start); took > 10*time.Second {
		t.Errorf("a cancelled run took %s to return", took)
	}

	waitGoroutines(t, base)
}

// A reader that fails must not look like a stream that ended: the child
// would write a truncated video and report success.
func TestRunWithEndsTheRunWhenTheReaderFails(t *testing.T) {
	tool := find(t, engine.Config{Name: "faketool"})
	base := runtime.NumGoroutine()
	boom := errors.New("frame source failed")

	r := io.MultiReader(strings.NewReader("some frames"), iotest.ErrReader(boom))

	res, err := tool.RunWith(t.Context(), engine.RunOptions{Dir: t.TempDir(), Stdin: r}, "stdin-len")
	if !errors.Is(err, boom) || !errors.Is(err, engine.ErrRun) {
		t.Fatalf("RunWith() = %v, want ErrRun wrapping the reader error", err)
	}

	if strings.TrimSpace(res.Stdout) != "" {
		t.Errorf("stdout = %q: the tool completed on a truncated stream", res.Stdout)
	}

	waitGoroutines(t, base)
}
