package loadtest

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"strconv"
	"time"
)

const (
	// maxNames bounds the per-name breakdown: a Targeter that varies the path per request must not grow it without limit.
	maxNames  = 100
	otherName = "(other)"
)

// Trend is the summary of one duration metric.
type Trend struct {
	Min  time.Duration `json:"min_ns"`
	Mean time.Duration `json:"mean_ns"`
	P50  time.Duration `json:"p50_ns"`
	P90  time.Duration `json:"p90_ns"`
	P95  time.Duration `json:"p95_ns"`
	P99  time.Duration `json:"p99_ns"`
	Max  time.Duration `json:"max_ns"`
}

func trend(h *Histogram) Trend {
	return Trend{
		Min: h.Min(), Mean: h.Mean(), P50: h.Quantile(0.5), P90: h.Quantile(0.9), P95: h.Quantile(0.95),
		P99: h.Quantile(0.99), Max: h.Max(),
	}
}

// NameSummary is the part of a Summary that one request name accounts for.
type NameSummary struct {
	HTTPReqs        uint64  `json:"http_reqs"`
	HTTPReqFailed   float64 `json:"http_req_failed"`
	HTTPReqDuration Trend   `json:"http_req_duration"`
}

// Summary is the result of a run. It is a versioned contract: a new field is additive, a rename or a change of unit
// needs schema_version 2.
type Summary struct {
	SchemaVersion int    `json:"schema_version"`
	Target        string `json:"target"`
	Method        string `json:"method,omitempty"`
	// Model is "open" or "closed".
	Model       string        `json:"model"`
	Rate        string        `json:"rate,omitempty"`
	Concurrency int           `json:"concurrency,omitempty"`
	MaxWorkers  int           `json:"max_workers,omitempty"`
	Elapsed     time.Duration `json:"elapsed_ns"`
	// Interrupted is true when the context given to Run was cancelled before the run ended by itself (Ctrl+C).
	Interrupted bool `json:"interrupted,omitempty"`
	// HTTPReqs counts the requests that got a Result, successful or not.
	HTTPReqs uint64 `json:"http_reqs"`
	// RPS is HTTPReqs divided by the time the run spent sending, without the drain that waits for the requests in
	// flight: the throughput reached, not the rate asked for.
	RPS float64 `json:"rps"`
	// HTTPReqFailed is the fraction of requests with a transport error or a status of 400 or more.
	HTTPReqFailed float64 `json:"http_req_failed"`
	// HTTPReqDuration runs from the scheduled instant until the body was read, so in the open model the generator's
	// own lateness counts.
	HTTPReqDuration Trend `json:"http_req_duration"`
	// HTTPReqWaiting is the time to the first response byte, over the requests that got a response.
	HTTPReqWaiting    Trend `json:"http_req_waiting"`
	HTTPReqConnecting Trend `json:"http_req_connecting"`
	HTTPReqTLS        Trend `json:"http_req_tls_handshaking"`
	// DataSent and DataReceived count body bytes.
	DataSent     int64 `json:"data_sent"`
	DataReceived int64 `json:"data_received"`
	// DroppedIterations counts open-model arrivals that found MaxWorkers requests in flight. Those requests are in no
	// latency figure, so with drops the latency is understated.
	DroppedIterations uint64 `json:"dropped_iterations"`
	// Arrivals is HTTPReqs plus DroppedIterations: what the open model scheduled.
	Arrivals uint64 `json:"arrivals"`
	// Warnings are conditions that make the numbers misleading.
	Warnings []string `json:"warnings,omitempty"`
	// Status counts responses by code, and "error" for requests without one.
	Status    map[string]uint64      `json:"status"`
	Errors    map[string]uint64      `json:"errors,omitempty"`
	Histogram []Bucket               `json:"histogram"`
	ByName    map[string]NameSummary `json:"by_name,omitempty"`
}

type nameMetrics struct {
	reqs, failed uint64
	duration     Histogram
}

// Metrics aggregates Results. It is not safe for concurrent use.
type Metrics struct {
	reqs, failed      uint64
	duration, waiting Histogram
	connecting, tls   Histogram
	sent, received    int64
	status, errs      map[string]uint64
	names             map[string]*nameMetrics
}

// Add records one Result.
func (m *Metrics) Add(r Result) {
	if m.status == nil {
		m.status, m.errs, m.names = map[string]uint64{}, map[string]uint64{}, map[string]*nameMetrics{}
	}

	m.reqs++
	m.sent += r.BytesOut
	m.received += r.BytesIn
	m.duration.Record(r.Duration)

	// Only real connects: a reused connection has none, and its zeros would bury the series.
	if r.Connecting > 0 {
		m.connecting.Record(r.Connecting)
	}

	if r.TLS > 0 {
		m.tls.Record(r.TLS)
	}

	if r.Status > 0 {
		m.waiting.Record(r.Waiting)
		m.status[strconv.Itoa(r.Status)]++
	} else {
		m.status["error"]++
	}

	if r.Err != "" {
		m.errs[r.Err]++
	}

	n := m.nameOf(r.Name)
	n.reqs++
	n.duration.Record(r.Duration)

	if r.Failed() {
		m.failed++
		n.failed++
	}
}

func (m *Metrics) nameOf(name string) *nameMetrics {
	if n, ok := m.names[name]; ok {
		return n
	}

	if len(m.names) >= maxNames {
		name = otherName
		if n, ok := m.names[name]; ok {
			return n
		}
	}

	n := &nameMetrics{}
	m.names[name] = n

	return n
}

// Summary snapshots the metrics. The run's identity (Target, Model, ...) is left empty: Runner.Summarize fills it.
func (m *Metrics) Summary(elapsed time.Duration, dropped uint64) Summary {
	s := Summary{
		SchemaVersion: 1, Elapsed: elapsed, HTTPReqs: m.reqs, DroppedIterations: dropped,
		HTTPReqDuration: trend(&m.duration), HTTPReqWaiting: trend(&m.waiting),
		HTTPReqConnecting: trend(&m.connecting), HTTPReqTLS: trend(&m.tls),
		DataSent: m.sent, DataReceived: m.received, Status: map[string]uint64{}, Histogram: m.duration.Buckets(10),
	}

	maps.Copy(s.Status, m.status)

	if len(m.errs) > 0 {
		s.Errors = maps.Clone(m.errs)
	}

	s.Arrivals = m.reqs + dropped

	if dropped > 0 {
		s.Warnings = []string{
			fmt.Sprintf(
				"%d arrivals were dropped because no worker was free: latency is understated, the dropped requests are in no latency figure",
				dropped,
			),
		}
	}

	if elapsed > 0 {
		s.RPS = float64(m.reqs) / elapsed.Seconds()
	}

	if m.reqs > 0 {
		s.HTTPReqFailed = float64(m.failed) / float64(m.reqs)
	}

	// One name repeats the totals; the breakdown only helps when there are several.
	if len(m.names) > 1 {
		s.ByName = make(map[string]NameSummary, len(m.names))

		for k, n := range m.names {
			s.ByName[k] = NameSummary{
				HTTPReqs:        n.reqs,
				HTTPReqFailed:   float64(n.failed) / float64(n.reqs),
				HTTPReqDuration: trend(&n.duration),
			}
		}
	}

	return s
}

// Summarize snapshots m with the identity of this run. Call it once the run has ended (Results is closed) for the
// final report, or while it runs for a live one.
func (r *Runner) Summarize(m *Metrics) Summary {
	s := m.Summary(r.Elapsed(), r.Dropped())
	s.Target, s.Method = sanitizedTarget(r.target.URL), r.target.Method

	if w := r.generationWindow(); w > 0 {
		s.RPS = float64(s.HTTPReqs) / w.Seconds()
	}

	if s.Method == "" {
		s.Method = "GET"
	}

	if r.open {
		s.Model, s.Rate, s.MaxWorkers = "open", r.rate.Load().String(), r.cfg.MaxWorkers
	} else {
		s.Model, s.Concurrency = "closed", r.cfg.Concurrency
	}

	s.Interrupted = r.parent.Err() != nil

	return s
}

// sanitizedTarget drops the query and the fragment: they can hold a token, and the report is shared.
func sanitizedTarget(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}

	u.RawQuery, u.Fragment = "", ""

	return u.String()
}

// Run is Start plus collecting every Result into a Summary. It returns an error only when the configuration or the
// target is refused; a cancelled ctx ends the run early with Summary.Interrupted set.
func Run(ctx context.Context, cfg Config, t Targeter) (Summary, error) {
	r, err := Start(ctx, cfg, t)
	if err != nil {
		return Summary{}, err
	}

	var m Metrics

	for res := range r.Results() {
		m.Add(res)
	}

	return r.Summarize(&m), nil
}
