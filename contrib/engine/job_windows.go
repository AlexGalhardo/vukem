//go:build windows

package engine

import (
	"errors"
	"fmt"
	"os/exec"
	"sync/atomic"
	"syscall"
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
	// suspended is set by prepare: the process was created suspended and
	// assign must resume it.
	suspended bool
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

// prepare makes cmd start suspended, so the process runs no code, spawns
// nothing and allocates nothing before assign has put it in the job. It
// merges with the creation flags that Prepare may have set.
func (j *runJob) prepare(cmd *exec.Cmd) {
	if j == nil {
		return
	}

	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}

	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	j.suspended = true
}

// assign puts the suspended process in the job and resumes it. If the
// assignment fails, a run without limits resumes anyway and keeps the
// taskkill fallback; a run with limits returns the error and the caller kills
// the process, which has not run any code.
func (j *runJob) assign(cmd *exec.Cmd) error {
	if j == nil {
		return nil
	}

	err := assignProcess(j.h, cmd.Process.Pid)
	if err == nil {
		j.assigned.Store(true)
	} else if j.limited {
		return fmt.Errorf("engine: assign the process to its job object: %w", err)
	}

	if err := resumeProcess(cmd.Process.Pid); err != nil {
		return fmt.Errorf("engine: resume the process: %w", err)
	}

	return nil
}

// resumeProcess resumes the threads of the suspended process pid; a process
// created suspended has exactly one.
func resumeProcess(pid int) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}

	defer func() { _ = windows.CloseHandle(snap) }()

	entry := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	if err := windows.Thread32First(snap, &entry); err != nil {
		return err
	}

	resumed := false

	for {
		if entry.OwnerProcessID == uint32(pid) { //nolint:gosec // G115: a Windows process id is a positive 32-bit value
			if err := resumeThread(entry.ThreadID); err != nil {
				return err
			}

			resumed = true
		}

		if err := windows.Thread32Next(snap, &entry); err != nil {
			break
		}
	}

	if !resumed {
		return errors.New("no thread found")
	}

	return nil
}

func resumeThread(id uint32) error {
	h, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, id)
	if err != nil {
		return err
	}

	defer func() { _ = windows.CloseHandle(h) }()

	_, err = windows.ResumeThread(h)

	return err
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
