//go:build windows

package engine

import (
	"fmt"
	"os/exec"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

// runJob is the Job Object one run lives in. It carries the native limits
// ([Limits]) and JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, so terminating or
// closing it takes the whole process tree down, grandchildren included,
// which is what taskkill walking the parent pids could miss.
//
// A nil *runJob is valid and means "no job": every method then falls back
// to the process group of the platform.
type runJob struct {
	h        windows.Handle
	limited  bool
	assigned atomic.Bool
}

// newRunJob creates the job for a run with l. Without limits a failure to
// create it is not fatal (the run falls back to taskkill); with limits it
// is, because a run that looks limited and is not is the outcome refused.
func newRunJob(l Limits) (*runJob, error) {
	limited := !l.zero()

	h, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		if !limited {
			return nil, nil
		}

		return nil, fmt.Errorf("engine: create the job object: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE

	if l.CPUTime > 0 {
		// 100 ns units of user-mode time per process; at least one tick,
		// since zero would mean no limit.
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_PROCESS_TIME
		info.BasicLimitInformation.PerProcessUserTimeLimit = max(l.CPUTime.Nanoseconds()/100, 1)
	}

	if l.AddressSpace > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_PROCESS_MEMORY
		info.ProcessMemoryLimit = uintptr(l.AddressSpace)
	}

	if l.Processes > 0 {
		info.BasicLimitInformation.LimitFlags |= windows.JOB_OBJECT_LIMIT_ACTIVE_PROCESS
		info.BasicLimitInformation.ActiveProcessLimit = uint32(
			min(l.Processes, 1<<31-1),
		)
	}

	_, err = windows.SetInformationJobObject(h, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))
	if err != nil {
		_ = windows.CloseHandle(h)

		if !limited {
			return nil, nil
		}

		return nil, fmt.Errorf("engine: set the job object limits: %w", err)
	}

	return &runJob{h: h, limited: limited}, nil
}

// assign puts the started process in the job. The child already runs when
// this is called (os/exec offers no way to start it suspended), so there is
// a short window in which it is unlimited and a grandchild it spawns in that
// window would escape the job (documented on [Limits]). If the assignment fails, a run
// without limits keeps the taskkill fallback, and a run with limits fails.
func (j *runJob) assign(cmd *exec.Cmd) error {
	if j == nil {
		return nil
	}

	err := assignProcess(j.h, cmd.Process.Pid)
	if err == nil {
		j.assigned.Store(true)
		return nil
	}

	if !j.limited {
		return nil
	}

	return fmt.Errorf("engine: assign the process to its job object: %w", err)
}

func assignProcess(job windows.Handle, pid int) error {
	// pid is a positive 32-bit Windows process id.
	p, err := windows.OpenProcess(
		windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE,
		false,
		uint32(pid), //nolint:gosec // G115: a Windows process id is a positive 32-bit value
	)
	if err != nil {
		return err
	}

	defer func() { _ = windows.CloseHandle(p) }()

	return windows.AssignProcessToJobObject(job, p)
}

// kill terminates every process of the job, or the process group when the
// process never joined it.
func (j *runJob) kill(cmd *exec.Cmd) error {
	if j != nil && j.assigned.Load() {
		return windows.TerminateJobObject(j.h, 1)
	}

	return killProcessGroup(cmd)
}

// close closes the job; KILL_ON_JOB_CLOSE ends any process that outlived
// the run. Call it after Wait, when nothing else uses the handle.
func (j *runJob) close() {
	if j != nil {
		_ = windows.CloseHandle(j.h)
	}
}
