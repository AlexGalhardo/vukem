package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// A tool whose grandchild keeps the output pipe open (an esbuild service,
// a node worker) must not hang toolEnv once the tool itself has exited:
// on Windows CI that blocked internal/cli until the 10-minute timeout.
func TestToolEnvDoesNotWaitForGrandchildren(t *testing.T) {
	switch os.Getenv("VUKEM_CLI_TOOL_HELPER") {
	case "tool":
		child := exec.Command(os.Args[0], "-test.run=^TestToolEnvDoesNotWaitForGrandchildren$")
		child.Env = append(os.Environ(), "VUKEM_CLI_TOOL_HELPER=grandchild")
		child.Stdout = os.Stdout // inherits the pipe toolEnv reads

		if err := child.Start(); err != nil {
			os.Exit(3)
		}

		os.Exit(0)
	case "grandchild":
		// The working directory it inherits is the test's TempDir, which Windows
		// cannot delete while a process stands in it: leave it, then say so.
		_ = os.Chdir(os.TempDir())
		_ = os.WriteFile(os.Getenv("VUKEM_CLI_TOOL_MARKER"), nil, 0o600)

		time.Sleep(grandchildLife)
		os.Exit(0)
	}

	if testing.Short() {
		t.Skip("runs helper processes")
	}

	var out bytes.Buffer

	marker := filepath.Join(t.TempDir(), "grandchild-started")

	env := &Env{Dir: t.TempDir(), Stdout: &out, Stderr: &out, toolWaitDelay: 200 * time.Millisecond}
	start := time.Now()

	err := toolEnv(t.Context(), env, []string{"VUKEM_CLI_TOOL_HELPER=tool", "VUKEM_CLI_TOOL_MARKER=" + marker},
		os.Args[0], "-test.run=^TestToolEnvDoesNotWaitForGrandchildren$")
	if err != nil {
		t.Fatalf("toolEnv = %v, want nil once the tool exited 0", err)
	}

	// The bound also covers starting two copies of the race-instrumented
	// test binary, which takes seconds on a loaded Windows runner; it only
	// has to stay clearly below the grandchild's life.
	d := time.Since(start)
	if d > grandchildLife-3*time.Second {
		t.Errorf("toolEnv returned after %v, want it not to wait for the grandchild", d)
	}

	// Let the grandchild leave the TempDir and exit before they are removed
	// (Windows cannot delete a directory a process stands in, nor a running
	// executable). Its clock starts when it says it started, which on a loaded
	// machine is well after toolEnv returned.
	var started time.Time

	for deadline := time.Now().Add(time.Minute); time.Now().Before(deadline); {
		if _, err := os.Stat(marker); err == nil {
			started = time.Now()
			break
		}

		time.Sleep(50 * time.Millisecond)
	}

	if started.IsZero() {
		t.Fatal("the grandchild never started")
	}

	time.Sleep(grandchildLife + 500*time.Millisecond - time.Since(started))
}

const grandchildLife = 8 * time.Second

// combinedOutput is cmd.CombinedOutput that, like toolEnv, stops waiting
// for the output pipe shortly after cmd exits (npm and vite leave helper
// processes holding it).
func combinedOutput(cmd *exec.Cmd) ([]byte, error) {
	cmd.WaitDelay = defaultToolWaitDelay

	out, err := cmd.CombinedOutput()
	if errors.Is(err, exec.ErrWaitDelay) {
		err = nil
	}

	return out, err
}
