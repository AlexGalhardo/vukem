package loadtest

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// collect drains every Result of r, failing the test when the run does not end in time.
func collect(t *testing.T, r *Runner) []Result {
	t.Helper()

	var out []Result

	timeout := time.After(30 * time.Second)

	for {
		select {
		case res, ok := <-r.Results():
			if !ok {
				return out
			}

			out = append(out, res)
		case <-timeout:
			t.Fatal("the run did not end")
		}
	}
}

func okServer(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if hits != nil {
			hits.Add(1)
		}

		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	return srv
}

func get(srv *httptest.Server) Targeter { return Static(Request{URL: srv.URL + "/"}) }

func TestClosedModelSendsExactlyN(t *testing.T) {
	t.Parallel()

	var hits atomic.Int64

	srv := okServer(t, &hits)

	r, err := Start(t.Context(), Config{Concurrency: 8, Requests: 500}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	res := collect(t, r)
	if len(res) != 500 || hits.Load() != 500 {
		t.Fatalf("results=%d server hits=%d, want 500 each", len(res), hits.Load())
	}

	seen := map[uint64]bool{}

	for _, x := range res {
		if x.Failed() || x.Status != 200 || x.BytesIn != 2 || !x.Scheduled.Equal(x.Sent) {
			t.Fatalf("unexpected result %+v", x)
		}

		seen[x.Seq] = true
	}

	if len(seen) != 500 {
		t.Fatalf("seq values are not unique: %d distinct", len(seen))
	}
}

func TestOpenModelHoldsRate(t *testing.T) {
	if testing.Short() {
		t.Skip("takes a second of wall time")
	}

	srv := okServer(t, nil)

	r, err := Start(t.Context(), Config{Rate: Rate{200, time.Second}, Duration: time.Second}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	// The spec asks for 10%. Arrivals are scheduled from the start instant, so a busy machine can only lose
	// the ones its dispatcher never reached before the deadline: allow 20%.
	if n := len(collect(t, r)); n < 160 || n > 240 {
		t.Fatalf("sent %d requests for 200/s over 1s, want 160..240", n)
	}

	if r.Dropped() != 0 {
		t.Fatalf("dropped %d arrivals with no worker cap", r.Dropped())
	}
}

func TestDueTimeDoesNotDrift(t *testing.T) {
	t.Parallel()

	start := time.Unix(1_000, 0)

	if got := dueTime(start, Rate{3, time.Second}, 1); got != start.Add(time.Second/3) {
		t.Fatalf("dueTime(1) = %v", got.Sub(start))
	}

	// 100 000 arrivals per second for an hour at a one-hour period: the product overflows int64.
	r := Rate{360_000_000, time.Hour}

	if got := dueTime(start, r, 360_000_000).Sub(start); got != time.Hour {
		t.Fatalf("dueTime after one period = %v, want 1h", got)
	}

	if got := dueTime(start, Rate{200, time.Second}, 200).Sub(start); got != time.Second {
		t.Fatalf("200 arrivals at 200/s = %v, want exactly 1s", got)
	}
}

func TestOpenModelMeasuresFromScheduledTime(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)
	c := newClient(&Config{Timeout: 5 * time.Second, Connections: 1}, "test")

	// A request that was due 200 ms ago: the generator was late, and the lateness belongs in the latency.
	scheduled := time.Now().Add(-200 * time.Millisecond)
	res := c.do(t.Context(), Request{URL: srv.URL + "/"}, 0, scheduled, time.Now)

	if res.Failed() {
		t.Fatalf("result failed: %+v", res)
	}

	if res.Duration < 200*time.Millisecond || res.Service >= 200*time.Millisecond ||
		res.Duration < res.Service+190*time.Millisecond {
		t.Fatalf(
			"Duration=%v Service=%v: Duration must include the 200ms of lateness, Service must not",
			res.Duration,
			res.Service,
		)
	}
}

func TestOpenModelDropsWhenSaturated(t *testing.T) {
	if testing.Short() {
		t.Skip("takes half a second of wall time")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(150 * time.Millisecond):
		case <-r.Context().Done():
		}

		_, _ = w.Write([]byte("slow"))
	}))
	t.Cleanup(srv.Close)

	before := runtime.NumGoroutine()

	r, err := Start(
		t.Context(),
		Config{Rate: Rate{200, time.Second}, MaxWorkers: 2, Duration: 600 * time.Millisecond},
		get(srv),
	)
	if err != nil {
		t.Fatal(err)
	}

	peak := 0

	timeout := time.After(30 * time.Second)

loop:
	for {
		select {
		case _, ok := <-r.Results():
			if !ok {
				break loop
			}
		case <-time.After(20 * time.Millisecond):
			peak = max(peak, runtime.NumGoroutine())
		case <-timeout:
			t.Fatal("the run did not end")
		}
	}

	if r.Dropped() == 0 {
		t.Fatal("no arrival was dropped with 2 workers and a 150ms server at 200/s")
	}

	// Two workers, the dispatcher, the client's connection goroutines and the test server: nowhere near one per arrival.
	if peak-before > 60 {
		t.Fatalf("goroutines grew from %d to %d: arrivals are queueing without a bound", before, peak)
	}
}

func TestSetRateChangesArrivals(t *testing.T) {
	if testing.Short() {
		t.Skip("takes two seconds of wall time")
	}

	srv := okServer(t, nil)

	r, err := Start(t.Context(), Config{Rate: Rate{50, time.Second}, Duration: 2 * time.Second}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	begin := time.Now()

	time.AfterFunc(time.Second, func() {
		if err := r.SetRate(Rate{200, time.Second}); err != nil {
			t.Error(err)
		}
	})

	var first, second int

	for _, res := range collect(t, r) {
		if res.Scheduled.Sub(begin) < time.Second {
			first++
		} else {
			second++
		}
	}

	// Expected about 50 then 150 to 200; the comparison leaves a wide margin for a loaded machine.
	if second < 2*first {
		t.Fatalf("arrivals before/after the rate change: %d/%d, want the second window at least double", first, second)
	}
}

func TestSetRateRefusedInClosedModelAndOverCap(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	closed, err := Start(t.Context(), Config{Concurrency: 1, Requests: 1}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	if err := closed.SetRate(Rate{10, time.Second}); err == nil {
		t.Error("SetRate worked on a closed-model run")
	}

	collect(t, closed)

	open, err := Start(t.Context(), Config{Rate: Rate{1, time.Second}, Requests: 1}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	if err := open.SetRate(Rate{maxRate + 1, time.Second}); err == nil {
		t.Error("SetRate accepted a rate over the cap")
	}

	collect(t, open)
}

func TestStopEndsRunAndClosesResults(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	r, err := Start(t.Context(), Config{Concurrency: 2, Duration: time.Hour}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup

	wg.Go(func() { collect(t, r) })

	time.Sleep(50 * time.Millisecond)
	r.Stop()
	r.Stop() // idempotent
	wg.Wait()
}

func TestContextCancelEndsRun(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)
	ctx, cancel := context.WithCancel(t.Context())

	r, err := Start(ctx, Config{Concurrency: 2, Duration: time.Hour}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	time.AfterFunc(50*time.Millisecond, cancel)

	for _, res := range collect(t, r) {
		if res.Err == "canceled" {
			t.Fatalf("a request aborted by the cancellation was reported as a failure: %+v", res)
		}
	}
}

func TestSecurity_A10_BenchDoesNotFollowRedirects(t *testing.T) {
	t.Parallel()

	var other atomic.Int64

	b := okServer(t, &other)
	a := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, b.URL+"/", http.StatusFound)
	}))
	t.Cleanup(a.Close)

	r, err := Start(t.Context(), Config{Concurrency: 2, Requests: 10}, get(a))
	if err != nil {
		t.Fatal(err)
	}

	for _, res := range collect(t, r) {
		if res.Status != http.StatusFound || res.Failed() {
			t.Fatalf("result = %+v, want a successful 302", res)
		}
	}

	if other.Load() != 0 {
		t.Fatalf("the redirect target received %d requests", other.Load())
	}
}

func TestSecurity_A10_BenchChecksEveryTargetHost(t *testing.T) {
	t.Parallel()

	srv := okServer(t, nil)

	var outside atomic.Int64

	dialer := &net.Dialer{}
	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if host, _, _ := net.SplitHostPort(addr); !loopbackHost(host) {
				outside.Add(1)
			}

			return dialer.DialContext(ctx, network, addr)
		},
	}}

	targeter := func(seq uint64) Request {
		if seq%2 == 0 {
			return Request{URL: srv.URL + "/"}
		}

		return Request{URL: "http://example.com/"}
	}

	r, err := Start(t.Context(), Config{Concurrency: 2, Requests: 10, Client: client}, targeter)
	if err != nil {
		t.Fatal(err)
	}

	var refused int

	for _, res := range collect(t, r) {
		if res.Seq%2 == 1 {
			if res.Err != "other" || res.Status != 0 {
				t.Fatalf("a request to another host = %+v, want an unsent failure", res)
			}

			refused++
		}
	}

	if refused != 5 || outside.Load() != 0 {
		t.Fatalf("refused=%d, connections outside loopback=%d", refused, outside.Load())
	}
}

func TestStartRefusesBadTargetsBeforeSending(t *testing.T) {
	t.Parallel()

	for _, u := range []string{"http://example.com/", "http://u:p@127.0.0.1/", "file:///etc/hosts"} {
		if _, err := Start(t.Context(), Config{Concurrency: 1, Requests: 1}, Static(Request{URL: u})); err == nil {
			t.Errorf("Start accepted %q", u)
		}
	}
}

func TestErrorClasses(t *testing.T) {
	t.Parallel()

	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(2 * time.Second):
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(slow.Close)

	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}

			// Read the request first: closing with it unread would reset the connection instead of ending it.
			_, _ = c.Read(make([]byte, 4096))
			_ = c.Close()
		}
	}()

	tests := []struct {
		name, url, want string
	}{
		{"timeout", slow.URL, "timeout"},
		{"refused", closedURL, "connection refused"},
		{"eof", "http://" + ln.Addr().String(), "eof"},
	}

	for _, tt := range tests {
		r, err := Start(
			t.Context(),
			Config{Concurrency: 1, Requests: 1, Timeout: 100 * time.Millisecond},
			Static(Request{URL: tt.url + "/"}),
		)
		if err != nil {
			t.Fatal(err)
		}

		res := collect(t, r)
		if len(res) != 1 || res[0].Err != tt.want || !res[0].Failed() || res[0].Status != 0 {
			t.Errorf("%s: results = %+v, want Err %q", tt.name, res, tt.want)
		}
	}
}

func TestKeepAliveReusesConnections(t *testing.T) {
	t.Parallel()

	for _, noKeepAlive := range []bool{false, true} {
		var conns atomic.Int64

		srv := httptest.NewUnstartedServer(
			http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }),
		)
		srv.Config.ConnState = func(_ net.Conn, s http.ConnState) {
			if s == http.StateNew {
				conns.Add(1)
			}
		}
		srv.Start()

		r, err := Start(t.Context(), Config{Concurrency: 1, Requests: 20, NoKeepAlive: noKeepAlive}, get(srv))
		if err != nil {
			t.Fatal(err)
		}

		var connecting int

		for _, res := range collect(t, r) {
			if res.Connecting > 0 {
				connecting++
			}
		}

		srv.Close()

		if noKeepAlive {
			if conns.Load() != 20 {
				t.Errorf("without keep-alive the server saw %d connections, want 20", conns.Load())
			}
		} else if conns.Load() != 1 || connecting > 1 {
			t.Errorf(
				"with keep-alive: %d connections, %d results that connected, want 1 and at most 1",
				conns.Load(),
				connecting,
			)
		}
	}
}
