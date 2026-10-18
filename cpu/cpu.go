// Package cpu reports the CPU quota the container this process runs in was
// given, for sizing work the Go runtime does not size for you.
//
// Go 1.25 and later already derive GOMAXPROCS from the cgroup quota, so a
// Go worker pool scaled from runtime.GOMAXPROCS(0) is correct and this
// package adds nothing to it. What GOMAXPROCS cannot be used for is a
// thread count handed to an **external process**, because it rounds the
// quota up and never drops below 2. Measured on Go 1.27.2 under Docker on
// a 16-core host:
//
//	--cpus=0.5  GOMAXPROCS 2  (four times the quota)
//	--cpus=1.5  GOMAXPROCS 2
//	--cpus=3    GOMAXPROCS 3
//	--cpus=4.5  GOMAXPROCS 5
//
// Over-subscribing the tightest quotas is the dangerous direction: the
// threads are throttled, they count against RLIMIT_NPROC, and an encoder
// that cannot start its threads exits having written nothing with a
// message naming no cause.
//
// Use [Count] for the number to pass to ffmpeg's -threads, SVT-AV1's lp,
// VIPS_CONCURRENCY or any other pool outside the Go runtime, and [Limit]
// when the fraction itself matters.
//
// Only Linux has cgroups; everywhere else [Limit] reports no quota and
// [Count] falls back to the machine's CPUs.
package cpu

import (
	"io/fs"
	"math"
	"os"
	"runtime"
	"strconv"
	"strings"
)

// cgroupRoot is the mount point of the cgroup filesystem on Linux. The
// container's own limits are at its root, because a container sees its
// cgroup namespace rooted there.
const cgroupRoot = "/sys/fs/cgroup"

// Limit returns the CPU quota of this container in CPUs: 2 for
// --cpus=2, 0.5 for --cpus=0.5. ok is false when there is no quota, which
// includes every non-Linux host and a Linux process outside a limited
// cgroup.
//
// Prefer [Count] unless the fraction matters: a quota below one CPU is
// real, and a thread count has to be a whole number.
func Limit() (cpus float64, ok bool) { return limitFrom(os.DirFS(cgroupRoot)) }

// Count is the number of CPUs a pool outside the Go runtime may use: the
// quota rounded **down**, at least 1, or the machine's CPUs when there is
// no quota.
//
// Rounded down on purpose. A thread past the quota does not get more CPU —
// it gets throttled, and it still counts against RLIMIT_NPROC — while a
// thread fewer leaves at most a fraction of a core idle. GOMAXPROCS rounds
// the other way, which is why it is the wrong number to hand an external
// process.
func Count() int { return countFrom(os.DirFS(cgroupRoot)) }

func countFrom(fsys fs.FS) int {
	limit, ok := limitFrom(fsys)
	if !ok {
		return runtime.NumCPU()
	}

	return max(1, int(math.Floor(limit)))
}

// limitFrom reads the quota from a cgroup filesystem, cgroup v2 first: a
// host running both sees v2's unified hierarchy as authoritative, and a
// v1 file left over from it would report a different number.
func limitFrom(fsys fs.FS) (float64, bool) {
	if cpus, ok := cgroupV2(fsys); ok {
		return cpus, true
	}

	return cgroupV1(fsys)
}

// cgroupV2 reads "cpu.max": "<quota> <period>" in microseconds, where the
// quota is the literal "max" when unlimited.
func cgroupV2(fsys fs.FS) (float64, bool) {
	quota, period, found := strings.Cut(strings.TrimSpace(readFile(fsys, "cpu.max")), " ")
	if !found {
		return 0, false
	}

	return ratio(quota, period)
}

// cgroupV1 reads cpu.cfs_quota_us and cpu.cfs_period_us, where a quota of
// -1 means unlimited.
func cgroupV1(fsys fs.FS) (float64, bool) {
	return ratio(
		strings.TrimSpace(readFile(fsys, "cpu/cpu.cfs_quota_us")),
		strings.TrimSpace(readFile(fsys, "cpu/cpu.cfs_period_us")),
	)
}

// ratio turns a quota and a period in microseconds into CPUs. Anything
// unparsable, unlimited or non-positive reports no quota: a wrong number
// here sizes every pool in the process, so the fallback has to be "do not
// know" rather than a guess.
func ratio(quota, period string) (float64, bool) {
	q, err := strconv.ParseFloat(quota, 64)
	if err != nil || q <= 0 {
		return 0, false
	}

	p, err := strconv.ParseFloat(period, 64)
	if err != nil || p <= 0 {
		return 0, false
	}

	return q / p, true
}

func readFile(fsys fs.FS, name string) string {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return ""
	}

	return string(b)
}
