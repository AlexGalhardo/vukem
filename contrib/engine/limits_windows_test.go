//go:build windows

package engine_test

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"testing"
	"time"

	"golang.org/x/sys/windows"

	"github.com/AlexGalhardo/vukem/contrib/engine"
)

const stillActive = 259

func processAlive(t *testing.T, pid uint32) bool {
	t.Helper()

	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return false
	}

	defer func() { _ = windows.CloseHandle(h) }()

	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		t.Fatal(err)
	}

	return code == stillActive
}

func TestJobObjectStopsAChildOverTheMemoryLimit(t *testing.T) {
	t.Parallel()

	bin := faketool(t)
	tool := find(t, engine.Config{Path: bin, Name: "faketool", Limits: engine.Limits{AddressSpace: 256 << 20}})

	// Well inside the limit: a limited run is not over-restricted.
	res, err := tool.Run(t.Context(), t.TempDir(), "alloc", "16")
	if err != nil {
		t.Fatalf("Run(alloc 16) = %v, want success under the limit", err)
	}

	if res.Stdout == "" {
		t.Error("Run(alloc 16) printed nothing")
	}

	// Far over it: the allocation fails and the job kills the child before it reports.
	res, err = tool.Run(t.Context(), t.TempDir(), "alloc", "2048")
	if !errors.Is(err, engine.ErrMemoryLimit) {
		t.Fatalf("Run(alloc 2048) = %v, want ErrMemoryLimit", err)
	}

	if res.Stdout != "" {
		t.Errorf("the child reported %q after exceeding its memory limit", res.Stdout)
	}

	// The same allocation without a limit would not be stopped by the package.
	free := find(t, engine.Config{Path: bin, Name: "faketool"})
	if _, err := free.Run(t.Context(), t.TempDir(), "alloc", "300"); err != nil {
		t.Errorf("unlimited Run(alloc 300) = %v", err)
	}
}

func TestJobObjectStopsAChildOverTheCPULimit(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool", Limits: engine.Limits{CPUTime: time.Second}})

	start := time.Now()

	if _, err := tool.Run(t.Context(), t.TempDir(), "spin"); !errors.Is(err, engine.ErrRun) {
		t.Fatalf("Run(spin) = %v, want ErrRun", err)
	}

	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("a CPU-limited run took %s", took)
	}
}

func TestJobObjectProcessLimitRefusesChildren(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool", Limits: engine.Limits{Processes: 1}})

	start := time.Now()
	res, err := tool.Run(t.Context(), t.TempDir(), "spawn")

	if !errors.Is(err, engine.ErrRun) {
		t.Fatalf("Run(spawn) = %v (stderr %q), want ErrRun", err, res.Stderr)
	}

	if took := time.Since(start); took > 20*time.Second {
		t.Errorf("the run waited %s for a child the limit should refuse", took)
	}
}

func TestJobObjectKillsGrandchildrenOnCancel(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool"})

	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()

	res, err := tool.Run(ctx, t.TempDir(), "spawn")
	if !errors.Is(err, engine.ErrRun) {
		t.Fatalf("Run(spawn) = %v, want ErrRun after the deadline", err)
	}

	pid := grandchildPID(t, res.Stderr)

	deadline := time.Now().Add(10 * time.Second)
	for processAlive(t, pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d still running after the run was cancelled", pid)
		}

		time.Sleep(50 * time.Millisecond)
	}
}

func TestJobObjectKillsLeftoversWhenTheRunEnds(t *testing.T) {
	t.Parallel()

	tool := find(t, engine.Config{Name: "faketool"})

	// The tool exits at once and leaves its helper running.
	res, err := tool.Run(t.Context(), t.TempDir(), "orphan")
	if err != nil {
		t.Fatalf("Run(orphan) = %v", err)
	}

	pid := grandchildPID(t, res.Stderr)

	deadline := time.Now().Add(10 * time.Second)
	for processAlive(t, pid) {
		if time.Now().After(deadline) {
			t.Fatalf("grandchild %d outlived the job", pid)
		}

		time.Sleep(50 * time.Millisecond)
	}
}

func TestJobObjectKillsChunkedAllocationPromptly(t *testing.T) {
	t.Parallel()

	bin := faketool(t)
	tool := find(t, engine.Config{
		Path: bin, Name: "faketool", Limits: engine.Limits{AddressSpace: 192 << 20},
	})

	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()

	start := time.Now()
	res, err := tool.Run(ctx, t.TempDir(), "alloc-chunks", "64")
	took := time.Since(start)

	if took > 15*time.Second {
		t.Fatalf("chunked allocation took %s (stderr %q), want a kill well under 60s", took, res.Stderr)
	}

	if !errors.Is(err, engine.ErrMemoryLimit) {
		t.Fatalf("Run(alloc-chunks) = %v after %s, stderr %q, want ErrMemoryLimit", err, took, res.Stderr)
	}
}

func TestLimitsForRunsWithFileSizeOnWindows(t *testing.T) {
	t.Parallel()

	bin := faketool(t)
	// The caller does not zero FileSize. For drops the field Windows cannot apply.
	limits := engine.Limits{AddressSpace: 256 << 20, FileSize: 1 << 20}.For("windows")
	tool := find(t, engine.Config{Path: bin, Name: "faketool", Limits: limits})

	if _, err := tool.Run(t.Context(), t.TempDir(), "alloc", "16"); err != nil {
		t.Fatalf("Run with For(windows) = %v, want success", err)
	}
}

func TestFileSizeLimitIsRefusedOnWindows(t *testing.T) {
	t.Parallel()

	_, err := engine.Find(t.Context(), engine.Config{
		Path: faketool(t), Name: "faketool", Limits: engine.Limits{FileSize: 1 << 20},
	})
	if !errors.Is(err, engine.ErrLimitsUnsupported) {
		t.Fatalf("Find(FileSize) = %v, want ErrLimitsUnsupported", err)
	}
}

var grandchildRE = regexp.MustCompile(`grandchild (\d+)`)

func grandchildPID(t *testing.T, stderr string) uint32 {
	t.Helper()

	m := grandchildRE.FindStringSubmatch(stderr)
	if m == nil {
		t.Fatalf("the child never reported a grandchild pid; stderr: %q", stderr)
	}

	pid, err := strconv.ParseUint(m[1], 10, 32)
	if err != nil {
		t.Fatal(err)
	}

	return uint32(pid)
}

func TestJobObjectProcessLimitHoldsFromTheFirstInstruction(t *testing.T) {
	t.Parallel()

	bin := faketool(t)
	tool := find(t, engine.Config{Path: bin, Name: "faketool", Limits: engine.Limits{Processes: 1}})

	// A child started before the process joined its job would escape the
	// limit; many runs make a race visible.
	for i := range 50 {
		res, err := tool.Run(t.Context(), t.TempDir(), "orphan")

		if !errors.Is(err, engine.ErrRun) {
			t.Fatalf("run %d: Run(orphan) = %v, want ErrRun (the grandchild must not start)", i, err)
		}

		if grandchildRE.MatchString(res.Stderr) {
			t.Fatalf("run %d: a grandchild started under Processes=1: %q", i, res.Stderr)
		}
	}
}
