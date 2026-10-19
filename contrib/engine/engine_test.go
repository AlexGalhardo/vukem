package engine_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
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
