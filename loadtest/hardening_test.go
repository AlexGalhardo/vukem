package loadtest

import (
	"bytes"
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestNewClientTransportDefaults(t *testing.T) {
	t.Parallel()

	own := newClient(&Config{Timeout: time.Second}, "ua")
	if !own.own {
		t.Fatal("a generated client must be marked as owned, so the run closes its idle connections")
	}

	tr, ok := own.http.Transport.(*http.Transport)
	if !ok || tr.IdleConnTimeout != 90*time.Second || !tr.DisableCompression || tr.Proxy != nil {
		t.Fatalf("transport = %+v, want a 90s idle timeout, no compression and no proxy", tr)
	}

	if given := newClient(&Config{Client: &http.Client{}}, "ua"); given.own {
		t.Fatal("a Config.Client is the caller's: the run must not close its connections")
	}
}

func TestIdleConnectionsAreClosedAtTheEndOfARun(t *testing.T) {
	t.Parallel()

	var closed atomic.Int64

	srv := httptest.NewUnstartedServer(
		http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }),
	)
	srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateClosed {
			closed.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)

	if _, err := Run(t.Context(), Config{Concurrency: 1, Requests: 5}, get(srv)); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for closed.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if closed.Load() != 1 {
		t.Fatalf("the server saw %d closed connections, want the one kept alive to be closed by the run", closed.Load())
	}
}

func TestConnectionsDefaultFollowsTheModel(t *testing.T) {
	t.Parallel()

	if got := (Config{Rate: Rate{10, time.Second}, MaxWorkers: 5000}).withDefaults().Connections; got != 5000 {
		t.Errorf("open model with 5000 workers keeps %d idle connections, want 5000", got)
	}

	if got := (Config{Rate: Rate{10, time.Second}}).withDefaults().Connections; got != defaultMaxWorkers {
		t.Errorf("open model default = %d, want MaxWorkers (%d)", got, defaultMaxWorkers)
	}

	if got := (Config{Concurrency: 300}).withDefaults().Connections; got != 300 {
		t.Errorf("closed model with 300 workers keeps %d, want 300", got)
	}

	if got := (Config{Concurrency: 3}).withDefaults().Connections; got != defaultConnections {
		t.Errorf("closed model default = %d, want %d", got, defaultConnections)
	}
}

func TestSecurity_A10_BenchPinsTheDialToLoopback(t *testing.T) {
	t.Parallel()

	for _, addr := range []string{"127.0.0.1:80", "[::1]:80", "127.9.9.9:1", "[::ffff:127.0.0.1]:80"} {
		if err := loopbackOnly("tcp", addr, nil); err != nil {
			t.Errorf("loopbackOnly(%q) = %v, want accepted", addr, err)
		}
	}

	// What a name such as x.localhost resolving to an internal address, or a rebinding answer, would produce.
	for _, addr := range []string{"10.0.0.5:80", "169.254.169.254:80", "192.168.1.1:443", "[2001:db8::1]:80", "[::ffff:10.0.0.5]:80", "garbage"} {
		if err := loopbackOnly("tcp", addr, nil); err == nil {
			t.Errorf("loopbackOnly(%q) accepted a non-loopback address", addr)
		}
	}

	dial := newDialContext([]string{"allowed.example"}, time.Second)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	// An IP literal skips DNS, so this needs no network: the control hook refuses before any connection.
	conn, err := dial(ctx, "tcp", "10.255.255.1:9")
	if err == nil {
		_ = conn.Close()

		t.Fatal("the dial of a host that is not allow-listed reached a non-loopback address")
	}

	if !strings.Contains(err.Error(), "non-loopback") {
		t.Fatalf("err = %v, want the loopback pin to refuse", err)
	}

	if !hostAllowed([]string{"Allowed.Example"}, "allowed.example:443") ||
		hostAllowed([]string{"allowed.example"}, "evil.example:443") {
		t.Fatal("hostAllowed must match the allow-list exactly, ignoring case and port")
	}
}

func TestMetricsArrivalsAndDropWarning(t *testing.T) {
	t.Parallel()

	var m Metrics

	for range 7 {
		m.Add(Result{Name: "GET /", Status: 200, Duration: time.Millisecond})
	}

	clean := m.Summary(time.Second, 0)
	if clean.Arrivals != 7 || len(clean.Warnings) != 0 {
		t.Fatalf("no drops: arrivals=%d warnings=%v", clean.Arrivals, clean.Warnings)
	}

	s := m.Summary(time.Second, 3)
	if s.Arrivals != 10 || s.DroppedIterations != 3 || len(s.Warnings) != 1 ||
		!strings.Contains(s.Warnings[0], "understated") {
		t.Fatalf("with drops: arrivals=%d dropped=%d warnings=%v", s.Arrivals, s.DroppedIterations, s.Warnings)
	}

	var text, js bytes.Buffer
	if err := WriteText(&text, s); err != nil {
		t.Fatal(err)
	}

	if err := WriteJSON(&js, s); err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(text.String(), "warning") || !strings.Contains(text.String(), "understated") ||
		!strings.Contains(text.String(), "3 dropped") {
		t.Errorf("the text report must show the drops and the warning:\n%s", text.String())
	}

	if !strings.Contains(js.String(), `"warnings"`) || !strings.Contains(js.String(), `"arrivals": 10`) {
		t.Errorf("the JSON must carry arrivals and warnings:\n%s", js.String())
	}

	var quiet bytes.Buffer
	if err := WriteText(&quiet, clean); err != nil {
		t.Fatal(err)
	}

	if strings.Contains(quiet.String(), "warning") {
		t.Errorf("a run without drops must not warn:\n%s", quiet.String())
	}
}

func TestMetricsConnectionSeriesRecordOnlyRealConnects(t *testing.T) {
	t.Parallel()

	var m Metrics

	for range 99 {
		m.Add(Result{Name: "GET /", Status: 200, Duration: time.Millisecond})
	}

	if s := m.Summary(time.Second, 0); s.HTTPReqConnecting.Max != 0 || s.HTTPReqTLS.Max != 0 {
		t.Fatalf("reused connections must not enter the series: %+v %+v", s.HTTPReqConnecting, s.HTTPReqTLS)
	}

	m.Add(
		Result{
			Name:       "GET /",
			Status:     200,
			Duration:   time.Millisecond,
			Connecting: 2 * time.Millisecond,
			TLS:        3 * time.Millisecond,
		},
	)

	s := m.Summary(time.Second, 0)
	if s.HTTPReqConnecting.Min != 2*time.Millisecond || s.HTTPReqConnecting.P50 != 2*time.Millisecond ||
		s.HTTPReqTLS.Max != 3*time.Millisecond {
		t.Fatalf("the series must hold only the one real connect: %+v %+v", s.HTTPReqConnecting, s.HTTPReqTLS)
	}
}

func TestRPSIsMeasuredOverTheGenerationWindow(t *testing.T) {
	t.Parallel()

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(600 * time.Millisecond):
		case <-r.Context().Done():
		}

		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(slow.Close)

	// Two workers send once, generation stops at 300ms, and the drain lasts until about 600ms.
	s, err := Run(t.Context(), Config{Concurrency: 2, Duration: 300 * time.Millisecond}, get(slow))
	if err != nil {
		t.Fatal(err)
	}

	if s.HTTPReqs != 2 || s.Elapsed < 500*time.Millisecond {
		t.Fatalf("reqs=%d elapsed=%v", s.HTTPReqs, s.Elapsed)
	}

	if overElapsed := float64(s.HTTPReqs) / s.Elapsed.Seconds(); s.RPS < 1.4*overElapsed {
		t.Fatalf("RPS=%.2f is not measured over the generation window (elapsed gives %.2f)", s.RPS, overElapsed)
	}
}

func TestSummaryTargetDropsQueryAndFragment(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	s, err := Run(
		t.Context(),
		Config{Concurrency: 1, Requests: 2},
		Static(Request{URL: srv.URL + "/p?token=secret#frag"}),
	)
	if err != nil {
		t.Fatal(err)
	}

	if s.Target != srv.URL+"/p" || strings.Contains(s.Target, "secret") {
		t.Fatalf("Target = %q, want the URL without query and fragment", s.Target)
	}
}

func TestHistogramBucketsWithASubMicrosecondSpan(t *testing.T) {
	t.Parallel()

	var h Histogram

	h.Record(100 * time.Nanosecond)
	h.Record(500 * time.Nanosecond)

	rows := h.Buckets(5)
	if len(rows) != 1 || rows[0].Count != 2 || rows[0].From != 100*time.Nanosecond ||
		rows[0].To != 500*time.Nanosecond {
		t.Fatalf("rows = %+v, want one row holding both samples", rows)
	}
}

func TestCapsForARunWithOnlyARequestCount(t *testing.T) {
	t.Parallel()

	if err := checkConfig(&Config{Requests: maxRequests + 1}); err == nil {
		t.Error("a request count over the cap was accepted")
	}

	if err := checkConfig(&Config{Requests: maxRequests}); err != nil {
		t.Errorf("a request count at the cap was refused: %v", err)
	}

	srv := okServer(t, nil)

	r, err := Start(t.Context(), Config{Concurrency: 1, Requests: 1}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	if r.cfg.Duration != maxDuration {
		t.Errorf("a run with only Requests has no time cap: Duration = %v, want %v", r.cfg.Duration, maxDuration)
	}

	collect(t, r)
}

func TestTargeterIsCalledOnceForSeqZero(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	var (
		mu    sync.Mutex
		calls = map[uint64]int{}
	)

	targeter := func(seq uint64) Request {
		mu.Lock()
		calls[seq]++
		mu.Unlock()

		return Request{URL: srv.URL + "/"}
	}

	if _, err := Run(t.Context(), Config{Concurrency: 2, Requests: 10}, targeter); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()

	if calls[0] != 1 {
		t.Fatalf("the Targeter was called %d times for seq 0, want 1 (Start's check is reused)", calls[0])
	}
}

func TestStopDoesNotHangWhenNobodyReadsResults(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	r, err := Start(t.Context(), Config{Concurrency: 4, Duration: time.Hour}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	// Nobody reads: the buffer fills and the workers block on it.
	deadline := time.Now().Add(10 * time.Second)
	for len(r.Results()) < cap(r.Results()) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	done := make(chan struct{})

	go func() {
		r.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Stop hung on workers blocked sending to a channel nobody reads")
	}
}
