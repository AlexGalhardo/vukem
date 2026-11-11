package liveui

import (
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/loadtest"
)

var epoch = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

// result is a request that finished at the given offset from epoch, took d and had the given status.
func result(offset, d time.Duration, status int) loadtest.Result {
	return loadtest.Result{Sent: epoch.Add(offset - d), Service: d, Duration: d, Status: status, Name: "GET /"}
}

func TestSeriesBucketsBySecond(t *testing.T) {
	t.Parallel()

	s := newSeries(epoch)

	// Two requests finish in second 0, one in second 1, a failed one in second 1.
	s.observe(result(100*time.Millisecond, 10*time.Millisecond, 200))
	s.observe(result(900*time.Millisecond, 30*time.Millisecond, 200))
	s.observe(result(1500*time.Millisecond, 20*time.Millisecond, 200))
	s.observe(result(1700*time.Millisecond, 20*time.Millisecond, 500))

	s.tick(epoch.Add(2*time.Second), 0, 0, 0)

	snaps := s.snapshots()
	if len(snaps) != 2 {
		t.Fatalf("got %d snapshots, want 2: %+v", len(snaps), snaps)
	}

	first, second := snaps[0], snaps[1]
	if first.Second != 0 || first.Reqs != 2 || first.Failed != 0 {
		t.Errorf("second 0 = %+v", first)
	}

	// A percentile is the upper edge of its histogram bucket, exact to 3%; the top one is the exact maximum.
	if first.P50Micros < 10_000 || first.P50Micros > 10_320 || first.P99Micros != 30_000 {
		t.Errorf("second 0 percentiles p50=%d p99=%d, want about 10000 and 30000", first.P50Micros, first.P99Micros)
	}

	if second.Second != 1 || second.Reqs != 2 || second.Failed != 1 {
		t.Errorf("second 1 = %+v", second)
	}
}

func TestSeriesCumulative(t *testing.T) {
	t.Parallel()

	s := newSeries(epoch)

	for i := range 3 {
		s.observe(result(time.Duration(i)*time.Second+500*time.Millisecond, time.Millisecond, 200))
		s.observe(result(time.Duration(i)*time.Second+600*time.Millisecond, time.Millisecond, 503))
	}

	s.tick(epoch.Add(3*time.Second), 7, 2, 10)

	snaps := s.snapshots()
	if len(snaps) != 3 {
		t.Fatalf("snapshots = %d", len(snaps))
	}

	last := snaps[2]
	if last.TotalReqs != 6 || last.TotalFailed != 3 || last.Dropped != 7 || last.Active != 2 || last.TargetRate != 10 {
		t.Fatalf("last = %+v, want cumulative 6/3, dropped 7, active 2, target 10", last)
	}

	if snaps[0].TotalReqs != 2 || snaps[1].TotalReqs != 4 {
		t.Fatalf("running totals = %d, %d", snaps[0].TotalReqs, snaps[1].TotalReqs)
	}
}

func TestSeriesCapsAt3600(t *testing.T) {
	t.Parallel()

	s := newSeries(epoch)
	s.tick(epoch.Add(4000*time.Second), 0, 0, 0)

	snaps := s.snapshots()
	if len(snaps) != maxSnapshots || snaps[0].Second != 4000-maxSnapshots || snaps[len(snaps)-1].Second != 3999 {
		t.Fatalf("len=%d first=%d last=%d", len(snaps), snaps[0].Second, snaps[len(snaps)-1].Second)
	}
}

func TestSeriesEmptySecondIsZero(t *testing.T) {
	t.Parallel()

	s := newSeries(epoch)
	s.observe(result(500*time.Millisecond, time.Millisecond, 200))
	// Nothing finishes in seconds 1 and 2: the chart must not skip them.
	s.observe(result(3500*time.Millisecond, time.Millisecond, 200))
	s.tick(epoch.Add(4*time.Second), 0, 0, 0)

	snaps := s.snapshots()
	if len(snaps) != 4 {
		t.Fatalf("snapshots = %d, want 4 (seconds 0 to 3)", len(snaps))
	}

	for _, i := range []int{1, 2} {
		if z := snaps[i]; z.Reqs != 0 || z.Failed != 0 || z.P50Micros != 0 || z.P95Micros != 0 || z.P99Micros != 0 {
			t.Errorf("second %d = %+v, want zeros", i, z)
		}
	}
}

func TestSeriesLateResultCountsInTheOpenSecond(t *testing.T) {
	t.Parallel()

	s := newSeries(epoch)
	s.tick(epoch.Add(5*time.Second), 0, 0, 0)

	// A result stamped before the open second still counts, in the open one, and is never lost.
	s.observe(result(time.Second, time.Millisecond, 200))
	s.tick(epoch.Add(6*time.Second), 0, 0, 0)

	snaps := s.snapshots()
	if got := snaps[len(snaps)-1].TotalReqs; got != 1 {
		t.Fatalf("TotalReqs = %d, want the late result counted", got)
	}
}

func TestSeriesFinishClosesThePartialSecond(t *testing.T) {
	t.Parallel()

	s := newSeries(epoch)
	s.observe(result(200*time.Millisecond, time.Millisecond, 200))
	s.finish(epoch.Add(400*time.Millisecond), 0)

	snaps := s.snapshots()
	if len(snaps) != 1 || snaps[0].Reqs != 1 {
		t.Fatalf("snapshots = %+v, want the partial second 0 closed", snaps)
	}
}

func TestSeriesConcurrentFeedAndRead(t *testing.T) {
	t.Parallel()

	s := newSeries(epoch)

	var wg sync.WaitGroup

	wg.Go(func() {
		for i := range 2000 {
			s.observe(result(time.Duration(i)*10*time.Millisecond, time.Millisecond, 200))

			if i%100 == 0 {
				s.tick(epoch.Add(time.Duration(i)*10*time.Millisecond), 0, 0, 0)
			}
		}
	})

	wg.Go(func() {
		for range 2000 {
			_ = s.snapshots()
			_ = s.summary(time.Second, 0)
		}
	})

	wg.Wait()
}
