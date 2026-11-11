//go:build windows

package engine

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

// setProcessGroup starts the child in a new process group so a Ctrl event
// aimed at the server does not reach it, which is the closest Windows
// analogue of Setpgid. Taking the tree down is killProcessGroup's job.
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// killProcessGroup kills the process and its descendants with taskkill,
// which walks the tree by parent PID; the standard library has no job
// object. If taskkill cannot run or fails, it kills the process itself.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}

	// Never resolved through PATH: a planted taskkill.exe would run with
	// the server's privileges.
	if root := os.Getenv("SystemRoot"); root != "" {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		//nolint:gosec // G204: fixed System32 path, numeric pid
		kill := exec.CommandContext(ctx, filepath.Join(root, "System32", "taskkill.exe"),
			"/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
		if kill.Run() == nil {
			return nil
		}
	}

	return cmd.Process.Kill()
}
