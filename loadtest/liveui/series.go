package liveui

import (
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/loadtest"
)

// maxSnapshots is an hour of seconds: the longest run loadtest allows.
const maxSnapshots = 3600

// Snapshot is the state of one second of the run.
type Snapshot struct {
	// Second is the number of seconds since the start.
	Second int `json:"second"`
	// Reqs is the requests completed in this second, Failed those that failed.
	Reqs   uint64 `json:"reqs"`
	Failed uint64 `json:"failed"`
	// The percentiles are of this second only, in microseconds.
	P50Micros int64 `json:"p50_us"`
	P95Micros int64 `json:"p95_us"`
	P99Micros int64 `json:"p99_us"`
	// Active is the requests in flight at the end of the second.
	Active int `json:"active"`
	// TargetRate is the arrivals per second asked for (open model) or the virtual users (closed model).
	TargetRate int `json:"target_rate"`
	// Dropped, TotalReqs and TotalFailed are cumulative.
	Dropped     uint64 `json:"dropped"`
	TotalReqs   uint64 `json:"total_reqs"`
	TotalFailed uint64 `json:"total_failed"`
}

// series buckets results into seconds. A second closes when a later one starts or a tick passes it, and a second in
// which nothing finished still gets a zero snapshot, so a chart never skips.
type series struct {
	mu    sync.Mutex
	start time.Time
	// sec is the second that is still open.
	sec          int
	hist         loadtest.Histogram
	reqs, failed uint64
	totalReqs    uint64
	totalFailed  uint64
	snaps        []Snapshot
	metrics      loadtest.Metrics

	dropped        uint64
	active, target int
}

func newSeries(start time.Time) *series { return &series{start: start} }

// observe records a finished request in the second it finished in. A result that arrives for a second that already
// closed counts in the open one: it is never lost.
func (s *series) observe(r loadtest.Result) {
	s.mu.Lock()
	defer s.mu.Unlock()

	sec := int(r.Sent.Add(r.Service).Sub(s.start) / time.Second)
	if sec > s.sec {
		s.advance(sec)
	}

	s.hist.Record(r.Duration)
	s.reqs++
	s.totalReqs++

	if r.Failed() {
		s.failed++
		s.totalFailed++
	}

	s.metrics.Add(r)
}

// tick notes the live gauges and closes every second that has passed by now.
func (s *series) tick(now time.Time, dropped uint64, active, target int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.dropped, s.active, s.target = dropped, active, target

	s.advance(max(int(now.Sub(s.start)/time.Second), 0))
}

// finish closes the partial second the run ended in.
func (s *series) finish(now time.Time, dropped uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.dropped, s.active = dropped, 0

	s.advance(max(int(now.Sub(s.start)/time.Second), s.sec) + 1)
}

// advance closes seconds until sec is the open one. The caller holds the mutex.
func (s *series) advance(to int) {
	for s.sec < to {
		s.snaps = append(s.snaps, Snapshot{
			Second:      s.sec,
			Reqs:        s.reqs,
			Failed:      s.failed,
			P50Micros:   s.hist.Quantile(0.50).Microseconds(),
			P95Micros:   s.hist.Quantile(0.95).Microseconds(),
			P99Micros:   s.hist.Quantile(0.99).Microseconds(),
			Active:      s.active,
			TargetRate:  s.target,
			Dropped:     s.dropped,
			TotalReqs:   s.totalReqs,
			TotalFailed: s.totalFailed,
		})

		if len(s.snaps) > maxSnapshots {
			s.snaps = append(s.snaps[:0], s.snaps[len(s.snaps)-maxSnapshots:]...)
		}

		s.hist, s.reqs, s.failed = loadtest.Histogram{}, 0, 0
		s.sec++
	}
}

// snapshots returns a copy of the closed seconds.
func (s *series) snapshots() []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]Snapshot, len(s.snaps))
	copy(out, s.snaps)

	return out
}

// since returns the closed seconds after the given one.
func (s *series) since(second int) []Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []Snapshot

	for _, sn := range s.snaps {
		if sn.Second > second {
			out = append(out, sn)
		}
	}

	return out
}

// summary is the cumulative Summary of everything observed so far.
func (s *series) summary(elapsed time.Duration, dropped uint64) loadtest.Summary {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.metrics.Summary(elapsed, dropped)
}
