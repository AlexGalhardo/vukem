package cpu

import (
	"runtime"
	"testing"
	"testing/fstest"
)

// A113: Go 1.25+ derives GOMAXPROCS from the cgroup quota, so a Go worker
// pool is already sized correctly, but it rounds **up** and never goes
// below 2. Measured on Go 1.27.2 under Docker on a 16-core host:
//
//	--cpus=0.5  GOMAXPROCS 2  (four times the quota)
//	--cpus=1.5  GOMAXPROCS 2
//	--cpus=2    GOMAXPROCS 2
//	--cpus=3    GOMAXPROCS 3
//	--cpus=4.5  GOMAXPROCS 5
//
// So sizing an external encoder's thread count from GOMAXPROCS
// over-subscribes exactly the quotas that are tightest, which is how
// x264 came to ask for about 24 threads under a 2-CPU limit and exit
// having written nothing.
func TestLimitFrom(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files fstest.MapFS
		want  float64
		ok    bool
	}{
		"v2 two cpus": {
			files: fstest.MapFS{"cpu.max": &fstest.MapFile{Data: []byte("200000 100000\n")}},
			want:  2, ok: true,
		},
		"v2 half a cpu": {
			files: fstest.MapFS{"cpu.max": &fstest.MapFile{Data: []byte("50000 100000\n")}},
			want:  0.5, ok: true,
		},
		"v2 unlimited": {
			files: fstest.MapFS{"cpu.max": &fstest.MapFile{Data: []byte("max 100000\n")}},
		},
		"v2 malformed": {
			files: fstest.MapFS{"cpu.max": &fstest.MapFile{Data: []byte("oops\n")}},
		},
		"v2 zero period": {
			files: fstest.MapFS{"cpu.max": &fstest.MapFile{Data: []byte("100000 0\n")}},
		},
		"v1 one and a half": {
			files: fstest.MapFS{
				"cpu/cpu.cfs_quota_us":  &fstest.MapFile{Data: []byte("150000\n")},
				"cpu/cpu.cfs_period_us": &fstest.MapFile{Data: []byte("100000\n")},
			},
			want: 1.5, ok: true,
		},
		"v1 unlimited": {
			files: fstest.MapFS{
				"cpu/cpu.cfs_quota_us":  &fstest.MapFile{Data: []byte("-1\n")},
				"cpu/cpu.cfs_period_us": &fstest.MapFile{Data: []byte("100000\n")},
			},
		},
		"v2 wins over v1": {
			files: fstest.MapFS{
				"cpu.max":               &fstest.MapFile{Data: []byte("400000 100000\n")},
				"cpu/cpu.cfs_quota_us":  &fstest.MapFile{Data: []byte("100000\n")},
				"cpu/cpu.cfs_period_us": &fstest.MapFile{Data: []byte("100000\n")},
			},
			want: 4, ok: true,
		},
		"no cgroup at all": {files: fstest.MapFS{}},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			got, ok := limitFrom(tt.files)
			if ok != tt.ok || got != tt.want {
				t.Errorf("limitFrom() = %v, %v, want %v, %v", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestCountFrom(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		files fstest.MapFS
		want  int
	}{
		// Rounded down, never up: a thread more than the quota is throttled
		// and counts against RLIMIT_NPROC, while a thread fewer only
		// leaves a fraction of a core idle.
		"half a cpu":      {fstest.MapFS{"cpu.max": &fstest.MapFile{Data: []byte("50000 100000")}}, 1},
		"one and a half":  {fstest.MapFS{"cpu.max": &fstest.MapFile{Data: []byte("150000 100000")}}, 1},
		"two cpus":        {fstest.MapFS{"cpu.max": &fstest.MapFile{Data: []byte("200000 100000")}}, 2},
		"four and a half": {fstest.MapFS{"cpu.max": &fstest.MapFile{Data: []byte("450000 100000")}}, 4},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			if got := countFrom(tt.files); got != tt.want {
				t.Errorf("countFrom() = %d, want %d", got, tt.want)
			}
		})
	}

	// No quota: the machine's CPUs, which is what an unconstrained process
	// may use.
	if got := countFrom(fstest.MapFS{}); got != runtime.NumCPU() {
		t.Errorf("countFrom(no cgroup) = %d, want NumCPU %d", got, runtime.NumCPU())
	}
}

// The exported entry points must not panic or return nonsense on a host
// with no cgroup filesystem (Windows, macOS, a bare Linux process).
func TestExportedFunctionsOnThisHost(t *testing.T) {
	t.Parallel()

	limit, ok := Limit()
	if ok && limit <= 0 {
		t.Errorf("Limit() = %v, true: a reported quota must be positive", limit)
	}

	if n := Count(); n < 1 {
		t.Errorf("Count() = %d, want at least 1", n)
	}
}
