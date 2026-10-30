package cli

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
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
		time.Sleep(3 * time.Second)
		os.Exit(0)
	}

	if testing.Short() {
		t.Skip("runs helper processes")
	}

	var out bytes.Buffer

	env := &Env{Dir: t.TempDir(), Stdout: &out, Stderr: &out, toolWaitDelay: 200 * time.Millisecond}
	start := time.Now()

	err := toolEnv(t.Context(), env, []string{"VUKEM_CLI_TOOL_HELPER=tool"},
		os.Args[0], "-test.run=^TestToolEnvDoesNotWaitForGrandchildren$")
	if err != nil {
		t.Fatalf("toolEnv = %v, want nil once the tool exited 0", err)
	}

	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("toolEnv returned after %v, want it not to wait for the grandchild", d)
	}

	// Let the grandchild exit before the test binary is removed (Windows
	// cannot delete a running executable).
	time.Sleep(3 * time.Second)
}

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
