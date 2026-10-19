//go:build unix

package engine

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group, so cancelling
// a run reaches the children it spawned. ffmpeg and yt-dlp both spawn:
// killing only the parent leaves them running and holding the working
// directory open (trap 4).
func setProcessGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.Setpgid = true
}

// killProcessGroup signals the whole group. The negative pid is what
// makes it the group rather than the one process; a failure falls back to
// the process, because a group that is already gone is the normal case.
func killProcessGroup(cmd *exec.Cmd) error {
	if cmd.Process == nil {
		return nil
	}

	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err == nil {
		return nil
	}

	return cmd.Process.Kill()
}
