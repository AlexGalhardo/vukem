package loadtest

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"math/bits"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	defaultConcurrency = 10
	defaultMaxWorkers  = 1000
	defaultConnections = 100
	resultBuffer       = 256
)

// Config configures a run. Rate.Freq > 0 selects the open model, where requests arrive at a constant rate whether or
// not earlier ones have finished; otherwise Concurrency workers loop (the closed model).
type Config struct {
	// Rate is the open-model arrival rate.
	Rate Rate
	// Concurrency is the number of closed-model workers; it defaults to 10 when Rate is zero.
	Concurrency int
	// Duration ends the run; 0 with Requests > 0 means "until Requests were sent".
	Duration time.Duration
	// Requests ends the run after that many requests (arrivals, in the open model); 0 means no limit. At most
	// 360 million. A run with Requests and no Duration is still capped at one hour.
	Requests uint64
	// MaxWorkers caps the requests in flight in the open model; arrivals past it are dropped. Default 1000.
	MaxWorkers int
	// Timeout bounds each request. Default 30s.
	Timeout time.Duration
	// Connections is the number of idle connections kept per host. Default max(Concurrency, 100) in the closed model
	// and max(MaxWorkers, 100) in the open model.
	Connections int
	// NoKeepAlive opens a connection per request.
	NoKeepAlive bool
	// HTTP2 negotiates HTTP/2 over TLS.
	HTTP2 bool
	// AllowRemote lists hosts, besides loopback, that may be targeted (exact match, ignoring case and port).
	AllowRemote []string
	// UserAgent is sent unless the request sets its own. Default "vukem-bench".
	UserAgent string
	// Client replaces the generated HTTP client (tests). A client of your own gets none of the protections of the
	// generated one: it may use a proxy, follow redirects and dial any address, and the run does not close its idle
	// connections. The target host is still checked for every request.
	Client *http.Client
	// Now replaces time.Now for timestamps (tests). Waiting still uses real timers, so the clock must advance.
	Now func() time.Time
}

func (c Config) withDefaults() Config {
	if c.Rate.Freq == 0 {
		c.Concurrency = cmp.Or(c.Concurrency, defaultConcurrency)
	}

	c.MaxWorkers = cmp.Or(c.MaxWorkers, defaultMaxWorkers)

	if c.Rate.Freq > 0 {
		c.Connections = cmp.Or(c.Connections, max(c.MaxWorkers, defaultConnections))
	} else {
		c.Connections = cmp.Or(c.Connections, max(c.Concurrency, defaultConnections))
	}
	c.Timeout = cmp.Or(c.Timeout, defaultTimeout)
	c.UserAgent = cmp.Or(c.UserAgent, defaultUserAgent)

	if c.Now == nil {
		c.Now = time.Now
	}

	return c
}

// Runner is a run in progress. Keep reading Results until it is closed: the workers block on a full channel. A
// consumer that stops reading must call Stop, which releases them and discards the results they could not deliver.
type Runner struct {
	cfg    Config
	open   bool
	target Request
	t      Targeter
	c      *client
	now    func() time.Time

	parent    context.Context
	reqCtx    context.Context
	genCtx    context.Context
	reqCancel context.CancelFunc
	genCancel context.CancelFunc
	timer     *time.Timer

	results chan Result
	done    chan struct{}
	wg      sync.WaitGroup

	begin time.Time
	end   time.Time

	abandon     chan struct{}
	abandonOnce sync.Once
	genEnd      atomic.Pointer[time.Time]

	rate     atomic.Pointer[Rate]
	rateCh   chan struct{}
	dropped  atomic.Uint64
	inflight atomic.Int64

	// newWork makes the function a worker runs for every arrival (open model) or turn of its loop (closed model).
	newWork func(worker int) workFn
	// stages, when set, drive the number of virtual users or the arrival rate.
	stages    []Stage
	scenarios []string

	iterations, iterFailed, panics atomic.Uint64
	firstPanic                     atomic.Pointer[string]
}

// notePanic keeps the first panic message of the run, cut short: it is shown in the report.
func (r *Runner) notePanic(scenario string, v any) {
	msg := fmt.Sprint(v)
	if len(msg) > 200 {
		msg = msg[:200] + "..."
	}

	note := "scenario " + strconv.Quote(scenario) + " panicked: " + msg
	r.firstPanic.CompareAndSwap(nil, &note)
}

// workFn does the work of one arrival or loop turn; seq numbers them from 0, and scheduled is zero in the closed model.
type workFn func(seq uint64, scheduled time.Time)

type job struct {
	seq       uint64
	scheduled time.Time
}

// Start validates cfg and the target of the first request, then starts the run. The run ends when Duration elapses,
// Requests were sent, ctx is cancelled or Stop is called. Cancelling ctx aborts the requests in flight and drops
// their results; the end of Duration or Stop lets them finish. The Targeter is called once for seq 0 here and the
// answer is reused for the first request.
func Start(ctx context.Context, cfg Config, t Targeter) (*Runner, error) {
	if t == nil {
		return nil, errors.New("loadtest: no targeter")
	}

	if err := checkConfig(&cfg); err != nil {
		return nil, err
	}

	cfg = cfg.withDefaults()

	first := t(0)

	r, err := newRunner(ctx, cfg, first)
	if err != nil {
		return nil, err
	}

	r.t = t
	r.newWork = func(int) workFn { return r.exec }
	r.launch()

	return r, nil
}

// newRunner validates the target and builds a run that is not started. cfg is checked and has its defaults.
func newRunner(ctx context.Context, cfg Config, first Request) (*Runner, error) {
	// A run with only a request count still has an end in time.
	if cfg.Duration == 0 {
		cfg.Duration = maxDuration
	}

	if _, err := parseTarget(first.URL, cfg.AllowRemote); err != nil {
		return nil, err
	}

	reqCtx, reqCancel := context.WithCancel(ctx)
	genCtx, genCancel := context.WithCancel(reqCtx)

	return &Runner{
		cfg: cfg, open: cfg.Rate.Freq > 0, target: first, c: newClient(&cfg, cfg.UserAgent), now: cfg.Now,
		parent: ctx, reqCtx: reqCtx, genCtx: genCtx, reqCancel: reqCancel, genCancel: genCancel,
		results: make(chan Result, resultBuffer), done: make(chan struct{}), rateCh: make(chan struct{}, 1),
		abandon: make(chan struct{}),
	}, nil
}

// launch starts the workers and the goroutine that closes Results when they are done.
func (r *Runner) launch() {
	r.begin = r.now()

	context.AfterFunc(r.genCtx, r.markGenerationEnd)

	if r.cfg.Duration > 0 {
		r.timer = time.AfterFunc(r.cfg.Duration, r.genCancel)
	}

	switch {
	case r.open:
		if r.rate.Load() == nil {
			rate := r.cfg.Rate
			r.rate.Store(&rate)
		}

		r.startOpen()
	case len(r.stages) > 0:
		r.startClosedStages()
	default:
		r.startClosed()
	}

	go func() {
		r.wg.Wait()

		if r.timer != nil {
			r.timer.Stop()
		}

		r.end = r.now()
		r.markGenerationEnd()

		if r.c.own {
			r.c.http.CloseIdleConnections()
		}

		r.genCancel()
		r.reqCancel()
		close(r.results)
		close(r.done)
	}()
}

// Results delivers every Result and is closed when the run ends.
func (r *Runner) Results() <-chan Result { return r.results }

// Dropped returns the open-model arrivals that found MaxWorkers requests already in flight.
func (r *Runner) Dropped() uint64 { return r.dropped.Load() }

// InFlight returns the requests being sent right now.
func (r *Runner) InFlight() int { return int(r.c.active.Load()) }

// Target returns the load being asked for now: arrivals per second in the open model, virtual users in the closed
// one, interpolated when stages drive it.
func (r *Runner) Target() int {
	switch {
	case len(r.stages) > 0:
		return int(targetAt(r.stages, r.now().Sub(r.begin)) + 0.5)
	case r.open:
		if rate := r.rate.Load(); rate != nil && rate.Freq > 0 {
			return int(rate.perSecond() + 0.5)
		}

		return 0
	}

	return r.cfg.Concurrency
}

// Elapsed returns the time since Start, frozen when the run ends.
func (r *Runner) Elapsed() time.Duration {
	select {
	case <-r.done:
		return r.end.Sub(r.begin)
	default:
		return r.now().Sub(r.begin)
	}
}

// markGenerationEnd records the instant the run stopped sending, once.
func (r *Runner) markGenerationEnd() {
	t := r.now()
	r.genEnd.CompareAndSwap(nil, &t)
}

// generationWindow is the time the run spent sending, without the drain that waits for the requests in flight.
func (r *Runner) generationWindow() time.Duration {
	if t := r.genEnd.Load(); t != nil {
		return t.Sub(r.begin)
	}

	return r.Elapsed()
}

// Stop ends the run early and waits for the requests in flight, which a Timeout bounds. Results that a consumer that
// is not reading leaves undelivered are discarded, so Stop never hangs on a full channel.
func (r *Runner) Stop() {
	r.genCancel()
	r.abandonOnce.Do(func() { close(r.abandon) })
	<-r.done
}

// SetRate changes the arrival rate of an open-model run while it runs.
func (r *Runner) SetRate(rate Rate) error {
	if !r.open {
		return errors.New("loadtest: only an open-model run has a rate")
	}

	if err := checkRate(rate); err != nil {
		return err
	}

	r.setRate(rate)

	return nil
}

// setRate applies a rate without validation; a zero Freq pauses the arrivals (stages use it).
func (r *Runner) setRate(rate Rate) {
	r.rate.Store(&rate)

	select {
	case r.rateCh <- struct{}{}:
	default:
	}
}

func (r *Runner) exec(seq uint64, scheduled time.Time) {
	req := r.target // the Targeter already answered for seq 0 in Start

	if seq != 0 {
		req = r.t(seq)
	}

	res := r.c.do(r.reqCtx, req, seq, scheduled, r.now)

	r.publish(res)
}

// publish delivers a result. A cancelled run aborts its requests, and reporting each as a failure would bury the
// real numbers, so those are dropped.
func (r *Runner) publish(res Result) {
	if res.Err == "canceled" && r.reqCtx.Err() != nil {
		return
	}

	select {
	case r.results <- res:
		return
	default:
	}

	select {
	case r.results <- res:
	case <-r.abandon:
	}
}

func (r *Runner) startClosed() {
	var next atomic.Uint64

	for i := range r.cfg.Concurrency {
		r.wg.Go(func() { r.userLoop(i, &next, nil) })
	}
}

// userLoop is one virtual user: it takes the next sequence number, works, and repeats until the run stops, the
// request count is reached or stop is closed.
func (r *Runner) userLoop(worker int, next *atomic.Uint64, stop <-chan struct{}) {
	work := r.newWork(worker)

	for r.genCtx.Err() == nil {
		select {
		case <-stop:
			return
		default:
		}

		seq := next.Add(1) - 1
		if r.cfg.Requests > 0 && seq >= r.cfg.Requests {
			return
		}

		work(seq, time.Time{})
	}
}

func (r *Runner) startOpen() {
	// Queued plus running jobs never exceed MaxWorkers, so the send in dispatch never blocks.
	jobs := make(chan job, r.cfg.MaxWorkers)

	r.wg.Add(1)

	go r.dispatch(jobs)

	if len(r.stages) > 0 {
		r.wg.Go(r.rampRate)
	}
}

// dueTime returns the instant of the n-th arrival after start at the given rate. It is computed from start, never
// by adding sleeps, so timer error cannot accumulate; the 128-bit product keeps a slow rate over a long run from
// overflowing.
func dueTime(start time.Time, rate Rate, n uint64) time.Time {
	// Rate was validated: Per and Freq are positive, and the quotient is at most Per.
	hi, lo := bits.Mul64(n, uint64(rate.Per))     //nolint:gosec // G115: Per > 0
	q, _ := bits.Div64(hi, lo, uint64(rate.Freq)) //nolint:gosec // G115: Freq > 0

	return start.Add(time.Duration(q)) //nolint:gosec // G115: q <= n*Per/Freq fits for any run under the caps
}

// dispatch is the pacer of the open model: it hands each arrival, at its scheduled instant, to a worker, and when
// it is behind the clock it fires without sleeping until it catches up. An arrival that finds MaxWorkers requests in
// flight is counted as dropped, not queued: a queue would hide saturation.
func (r *Runner) dispatch(jobs chan job) {
	genCtx := r.genCtx

	defer r.wg.Done()
	defer close(jobs)

	rate := *r.rate.Load()
	start := r.now()

	var seq, n uint64

	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	workers := 0

	for genCtx.Err() == nil && (r.cfg.Requests == 0 || seq < r.cfg.Requests) {
		// A rate of zero pauses the arrivals until the next change.
		if rate.Freq == 0 {
			select {
			case <-r.rateCh:
				rate, start, n = r.changeRate(rate, start, n)

				continue
			case <-genCtx.Done():
				return
			}
		}

		due := dueTime(start, rate, n)

		if wait := due.Sub(r.now()); wait > 0 {
			timer.Reset(wait)

			select {
			case <-timer.C:
			case <-r.rateCh:
				timer.Stop()

				rate, start, n = r.changeRate(rate, start, n)

				continue
			case <-genCtx.Done():
				return
			}
		}

		inflight := r.inflight.Add(1)
		if inflight > int64(r.cfg.MaxWorkers) {
			r.inflight.Add(-1)
			r.dropped.Add(1)
		} else {
			jobs <- job{seq: seq, scheduled: due}

			if int64(workers) < inflight {
				workers++

				r.wg.Add(1)

				go r.worker(jobs, workers-1)
			}
		}

		seq++
		n++
	}
}

// changeRate applies a SetRate: the new rate counts from the last scheduled arrival, so the schedule stays
// continuous.
func (r *Runner) changeRate(old Rate, start time.Time, n uint64) (Rate, time.Time, uint64) {
	next := *r.rate.Load()

	// From a pause, or before the first arrival, the schedule starts now.
	if old.Freq == 0 {
		return next, r.now(), 0
	}

	if n == 0 {
		return next, start, 0
	}

	return next, dueTime(start, old, n-1), 1
}

func (r *Runner) worker(jobs <-chan job, index int) {
	defer r.wg.Done()

	work := r.newWork(index)

	for j := range jobs {
		work(j.seq, j.scheduled)
		r.inflight.Add(-1)
	}
}
