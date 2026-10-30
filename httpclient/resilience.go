package httpclient

import (
	"context"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// ErrCircuitOpen is returned by a [Breaker] transport while the host's
// circuit is open.
var ErrCircuitOpen = errors.New("httpclient: circuit open for host")

// RoundTripperFunc adapts a function to http.RoundTripper.
type RoundTripperFunc func(*http.Request) (*http.Response, error)

// RoundTrip calls f(req).
func (f RoundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

// Limit bounds the requests a [Throttle] transport sends to each host.
type Limit struct {
	// PerSecond is the sustained request rate per host; 0 means no rate
	// limit.
	PerSecond float64
	// Burst is how many requests may go at once after an idle period
	// (default 1).
	Burst int
	// InFlight bounds the requests per host whose response body is still
	// open; 0 means no bound.
	InFlight int
}

// Throttle returns a transport wrapper that limits each host (the URL's
// host:port) to l: requests wait for their turn, or fail with the
// request context's error when it ends first. Use one client per API with
// its own limit (Options.WrapTransport).
//
//	httpclient.Safe(httpclient.Options{
//		WrapTransport: httpclient.Throttle(httpclient.Limit{PerSecond: 4, InFlight: 8}),
//	})
func Throttle(l Limit) func(http.RoundTripper) http.RoundTripper {
	return func(next http.RoundTripper) http.RoundTripper {
		t := &throttle{next: next, limit: l, hosts: map[string]*hostLimit{}}
		return RoundTripperFunc(t.roundTrip)
	}
}

type throttle struct {
	next  http.RoundTripper
	limit Limit

	mu sync.Mutex
	// shortcut: entries are never evicted, fine for a fixed set of API
	// hosts; add an LRU before throttling arbitrary user-supplied hosts.
	hosts map[string]*hostLimit
}

type hostLimit struct {
	tat time.Time     // theoretical arrival time of the next request (GCRA)
	sem chan struct{} // nil without an InFlight bound
}

func (t *throttle) host(name string) *hostLimit {
	h := t.hosts[name]
	if h == nil {
		h = &hostLimit{}
		if t.limit.InFlight > 0 {
			h.sem = make(chan struct{}, t.limit.InFlight)
		}

		t.hosts[name] = h
	}

	return h
}

// reserve books the next slot for host and returns how long to wait.
func (t *throttle) reserve(h *hostLimit) time.Duration {
	if t.limit.PerSecond <= 0 {
		return 0
	}

	interval := time.Duration(float64(time.Second) / t.limit.PerSecond)
	now := time.Now()

	if h.tat.Before(now) {
		h.tat = now
	}

	allowAt := h.tat.Add(-time.Duration(max(t.limit.Burst, 1)-1) * interval)
	h.tat = h.tat.Add(interval)

	return allowAt.Sub(now)
}

func (t *throttle) roundTrip(req *http.Request) (*http.Response, error) {
	t.mu.Lock()
	h := t.host(req.URL.Host)
	wait := t.reserve(h)
	t.mu.Unlock()

	if err := sleep(req.Context(), wait); err != nil {
		return nil, err
	}

	if h.sem == nil {
		return t.next.RoundTrip(req)
	}

	select {
	case h.sem <- struct{}{}:
	case <-req.Context().Done():
		return nil, req.Context().Err()
	}

	release := sync.OnceFunc(func() { <-h.sem })

	resp, err := t.next.RoundTrip(req)
	if err != nil {
		release()
		return nil, err
	}

	resp.Body = &releaseBody{ReadCloser: resp.Body, release: release}

	return resp, nil
}

// releaseBody frees the in-flight slot when the body is closed.
type releaseBody struct {
	io.ReadCloser

	release func()
}

func (b *releaseBody) Close() error {
	err := b.ReadCloser.Close()
	b.release()

	return err
}

// RetryPolicy configures a [Retry] transport.
type RetryPolicy struct {
	// Attempts is the total number of tries, the first included
	// (default 3).
	Attempts int
	// Base is the first backoff delay, doubled on every retry with full
	// jitter (default 200ms).
	Base time.Duration
	// Max caps every delay (default 30s). A Retry-After longer than Max
	// is not waited for: the response is returned as is.
	Max time.Duration
}

// Retry returns a transport wrapper that retries a request after a
// transport error or a 429, 502, 503 or 504 response, waiting for the
// server's Retry-After (seconds or an HTTP date) or else an exponential
// backoff. Only idempotent methods (GET, HEAD, OPTIONS, TRACE, PUT, DELETE)
// and requests carrying an Idempotency-Key header are retried, and only
// when their body can be replayed (no body, or Request.GetBody, which
// http.NewRequest sets for in-memory bodies). Context errors,
// ErrBlockedAddress and ErrCircuitOpen are never retried. Client.Timeout
// covers all attempts together.
func Retry(p RetryPolicy) func(http.RoundTripper) http.RoundTripper {
	if p.Attempts <= 0 {
		p.Attempts = 3
	}

	if p.Base <= 0 {
		p.Base = 200 * time.Millisecond
	}

	if p.Max <= 0 {
		p.Max = 30 * time.Second
	}

	return func(next http.RoundTripper) http.RoundTripper {
		return RoundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return retry(next, p, req)
		})
	}
}

func retry(next http.RoundTripper, p RetryPolicy, req *http.Request) (*http.Response, error) {
	if !replayable(req) {
		return next.RoundTrip(req)
	}

	for attempt := 1; ; attempt++ {
		resp, err := next.RoundTrip(req)

		delay, again := retryDelay(p, attempt, resp, err)
		if !again || attempt >= p.Attempts {
			return resp, err
		}

		if resp != nil {
			_, _ = io.CopyN(io.Discard, resp.Body, 64<<10) // lets the connection be reused
			_ = resp.Body.Close()
		}

		if err := sleep(req.Context(), delay); err != nil {
			return nil, err
		}

		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}

			req = req.Clone(req.Context())
			req.Body = body
		}
	}
}

func replayable(req *http.Request) bool {
	switch req.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, http.MethodTrace, http.MethodPut, http.MethodDelete:
	default:
		if req.Header.Get("Idempotency-Key") == "" {
			return false
		}
	}

	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// retryDelay reports whether the outcome of attempt deserves a retry, and
// after how long.
func retryDelay(p RetryPolicy, attempt int, resp *http.Response, err error) (time.Duration, bool) {
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, ErrBlockedAddress) || errors.Is(err, ErrCircuitOpen) {
			return 0, false
		}

		return backoff(p, attempt), true
	}

	switch resp.StatusCode {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
	default:
		return 0, false
	}

	if d, ok := retryAfter(resp.Header.Get("Retry-After")); ok {
		return d, d <= p.Max
	}

	return backoff(p, attempt), true
}

func backoff(p RetryPolicy, attempt int) time.Duration {
	d := min(p.Base<<min(attempt-1, 30), p.Max)

	return rand.N(d) + 1 //nolint:gosec // jitter, not a secret
}

func retryAfter(v string) (time.Duration, bool) {
	if v == "" {
		return 0, false
	}

	if s, err := strconv.Atoi(v); err == nil && s >= 0 {
		return time.Duration(s) * time.Second, true
	}

	if t, err := http.ParseTime(v); err == nil {
		return max(time.Until(t), 0), true
	}

	return 0, false
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// BreakerOptions configures a [Breaker] transport.
type BreakerOptions struct {
	// Failures is the number of consecutive failures (transport errors and
	// 5xx responses) that opens a host's circuit (default 5).
	Failures int
	// Cooldown is how long the circuit stays open before one trial
	// request may go through (default 30s).
	Cooldown time.Duration
}

// Breaker returns a transport wrapper with a circuit breaker per host:
// after o.Failures consecutive failures, requests to that host fail at
// once with ErrCircuitOpen for o.Cooldown; then a single trial request
// goes through, and its success closes the circuit while a failure opens
// it again. A request ended by its own context counts as neither.
func Breaker(o BreakerOptions) func(http.RoundTripper) http.RoundTripper {
	if o.Failures <= 0 {
		o.Failures = 5
	}

	if o.Cooldown <= 0 {
		o.Cooldown = 30 * time.Second
	}

	return func(next http.RoundTripper) http.RoundTripper {
		b := &breaker{next: next, opts: o, hosts: map[string]*circuit{}}
		return RoundTripperFunc(b.roundTrip)
	}
}

type breaker struct {
	next http.RoundTripper
	opts BreakerOptions

	mu    sync.Mutex
	hosts map[string]*circuit // shortcut: never evicted, like throttle.hosts
}

type circuit struct {
	failures  int
	openUntil time.Time
	trial     bool // a trial request is in flight
}

func (b *breaker) roundTrip(req *http.Request) (*http.Response, error) {
	host := req.URL.Host

	b.mu.Lock()

	c := b.hosts[host]
	if c == nil {
		c = &circuit{}
		b.hosts[host] = c
	}

	if c.failures >= b.opts.Failures {
		if c.trial || time.Now().Before(c.openUntil) {
			b.mu.Unlock()
			return nil, ErrCircuitOpen
		}

		c.trial = true
	}

	b.mu.Unlock()

	resp, err := b.next.RoundTrip(req)

	b.mu.Lock()
	defer b.mu.Unlock()

	switch {
	case err != nil && req.Context().Err() != nil:
		c.trial = false
	case err != nil || resp.StatusCode >= http.StatusInternalServerError:
		c.failures++
		if c.failures >= b.opts.Failures {
			c.openUntil = time.Now().Add(b.opts.Cooldown)
		}

		c.trial = false
	default:
		c.failures, c.trial = 0, false
	}

	return resp, err
}
