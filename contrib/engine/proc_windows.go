//go:build windows

package engine

import (
	"os/exec"
	"syscall"
)

// setProcessGroup starts the child in a new process group so a Ctrl event
// aimed at the server does not reach it, which is the closest Windows
// analogue of Setpgid.
//
// Windows has no process-group kill: taking a tree down needs a job
// object, which the standard library does not expose. killProcessGroup
// therefore kills the process it started, and a tool that spawns children
// can leave them behind — so prefer a tool's own timeout flag here, and
// give each run its own directory so a leftover child cannot be mistaken
// for a later run's work. Resource limits are refused on this platform
// for the same kind of reason (trap 3).
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.CreationFlags |= syscall.CREATE_NEW_PROCESS_GROUP
}

// killProcessGroup kills the started process. See setProcessGroup for why
// it cannot be the group.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}

	return cmd.Process.Kill()
}
