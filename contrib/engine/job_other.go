//go:build !windows

package engine

import "os/exec"

// runJob is the Windows Job Object of one run; elsewhere there is none and
// every method falls back to the process group.
type runJob struct{}

func newRunJob(Limits) (*runJob, error) { return nil, nil }

func (*runJob) assign(*exec.Cmd) error { return nil }

func (*runJob) kill(cmd *exec.Cmd) error { return killProcessGroup(cmd) }

func (*runJob) close() {}
