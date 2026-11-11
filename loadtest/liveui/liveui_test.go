package liveui

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/loadtest"
)

// clock is a settable clock for the dashboard.
type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.t
}

func (c *clock) set(t time.Time) {
	c.mu.Lock()
	c.t = t
	c.mu.Unlock()
}

func newServer(t *testing.T, cfg Config) (*Server, *clock) {
	t.Helper()

	clk := &clock{t: epoch}
	cfg.Now = clk.now

	if cfg.Title == "" {
		cfg.Title = "GET 127.0.0.1:3000/"
	}

	s, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}

	return s, clk
}

// client returns an HTTP client of the test server that has already exchanged the token for the session cookie.
func client(t *testing.T, s *Server) (*http.Client, *httptest.Server) {
	t.Helper()

	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	c := &http.Client{Jar: jar}

	resp, err := c.Get(srv.URL + "/?token=" + s.Token())
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the start URL answered %d", resp.StatusCode)
	}

	return c, srv
}

func TestHandlerServesPagesAndAssets(t *testing.T) {
	t.Parallel()

	s, clk := newServer(t, Config{Model: "closed"})
	s.Observe(result(300*time.Millisecond, 5*time.Millisecond, 200))
	clk.set(epoch.Add(2 * time.Second))
	s.Tick(0)

	c, srv := client(t, s)

	for path, want := range map[string]string{
		"/":                 "text/html",
		"/assets/live.css":  "text/css",
		"/assets/live.js":   "javascript",
		"/summary.json":     "application/json",
		"/report.html":      "text/html",
		"/assets/missing":   "",
		"/assets/../go.mod": "",
	} {
		resp, err := c.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}

		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		if want == "" {
			if resp.StatusCode == http.StatusOK {
				t.Errorf("%s answered 200", path)
			}

			continue
		}

		if resp.StatusCode != http.StatusOK || !strings.Contains(resp.Header.Get("Content-Type"), want) ||
			len(body) == 0 {
			t.Errorf(
				"%s = %d %q (%d bytes), want %s",
				path,
				resp.StatusCode,
				resp.Header.Get("Content-Type"),
				len(body),
				want,
			)
		}
	}

	resp, err := c.Get(srv.URL + "/summary.json")
	if err != nil {
		t.Fatal(err)
	}

	defer resp.Body.Close()

	var sum loadtest.Summary
	if err := json.NewDecoder(resp.Body).Decode(&sum); err != nil || sum.SchemaVersion != 1 || sum.HTTPReqs != 1 {
		t.Fatalf("summary.json = %+v, %v", sum, err)
	}
}

// sseLines reads an event stream line by line into a channel.
func sseLines(t *testing.T, c *http.Client, url string) (<-chan string, func()) {
	t.Helper()

	resp, err := c.Get(url) //nolint:bodyclose // the returned function closes it
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		t.Fatalf("%s answered %d", url, resp.StatusCode)
	}

	lines := make(chan string, 256)

	go func() {
		defer close(lines)

		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
	}()

	return lines, func() { _ = resp.Body.Close() }
}

// waitFor returns the data line that follows "event: name", failing after a timeout.
func waitFor(t *testing.T, lines <-chan string, name string) string {
	t.Helper()

	timeout := time.After(10 * time.Second)

	for {
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("the stream ended before event %q", name)
			}

			if l == "event: "+name {
				data := <-lines

				return strings.TrimPrefix(data, "data: ")
			}
		case <-timeout:
			t.Fatalf("no %q event", name)
		}
	}
}

func TestEventsSendHistoryThenSnapshots(t *testing.T) {
	t.Parallel()

	s, clk := newServer(t, Config{Model: "open"})
	s.Observe(result(200*time.Millisecond, time.Millisecond, 200))
	clk.set(epoch.Add(2 * time.Second))
	s.Tick(0)

	c, srv := client(t, s)

	// A client that connects late gets the seconds so far at once...
	lines, closeStream := sseLines(t, c, srv.URL+"/events")
	defer closeStream()

	var history struct {
		Snapshots []Snapshot `json:"snapshots"`
		Title     string     `json:"title"`
		Model     string     `json:"model"`
	}

	if err := json.Unmarshal([]byte(waitFor(t, lines, "history")), &history); err != nil {
		t.Fatal(err)
	}

	if len(history.Snapshots) != 2 || history.Model != "open" || history.Title == "" {
		t.Fatalf("history = %+v", history)
	}

	// ...then one snapshot for every second that closes afterwards.
	s.Observe(result(2500*time.Millisecond, time.Millisecond, 200))
	clk.set(epoch.Add(3 * time.Second))
	s.Tick(0)

	var snap Snapshot
	if err := json.Unmarshal(
		[]byte(waitFor(t, lines, "snapshot")),
		&snap,
	); err != nil || snap.Second != 2 ||
		snap.Reqs != 1 {
		t.Fatalf("snapshot = %+v, %v", snap, err)
	}
}

func TestEventsSendSummaryWithThresholds(t *testing.T) {
	t.Parallel()

	th, err := loadtest.ParseThreshold("p95<1h")
	if err != nil {
		t.Fatal(err)
	}

	s, clk := newServer(t, Config{Model: "closed", Thresholds: []loadtest.Threshold{th}})
	s.Observe(result(200*time.Millisecond, time.Millisecond, 200))
	clk.set(epoch.Add(time.Second))
	s.Tick(0)

	c, srv := client(t, s)
	lines, closeStream := sseLines(t, c, srv.URL+"/events")
	defer closeStream()

	var ev struct {
		Summary    loadtest.Summary `json:"summary"`
		Thresholds []struct {
			Threshold string `json:"threshold"`
			Got       string `json:"got"`
			OK        bool   `json:"ok"`
		} `json:"thresholds"`
	}

	if err := json.Unmarshal([]byte(waitFor(t, lines, "summary")), &ev); err != nil {
		t.Fatal(err)
	}

	if ev.Summary.HTTPReqs != 1 || len(ev.Thresholds) != 1 || ev.Thresholds[0].Threshold != "p95<1h" ||
		!ev.Thresholds[0].OK {
		t.Fatalf("summary event = %+v", ev)
	}
}

func TestFinishSendsTheFinalSummaryAndEndsTheStream(t *testing.T) {
	t.Parallel()

	s, clk := newServer(t, Config{Model: "closed"})
	c, srv := client(t, s)

	lines, closeStream := sseLines(t, c, srv.URL+"/events")
	defer closeStream()

	waitFor(t, lines, "history")

	clk.set(epoch.Add(time.Second))
	s.Finish(loadtest.Summary{SchemaVersion: 1, Target: "http://127.0.0.1:3000/", HTTPReqs: 42})

	var sum loadtest.Summary
	if err := json.Unmarshal([]byte(waitFor(t, lines, "finished")), &sum); err != nil || sum.HTTPReqs != 42 {
		t.Fatalf("finished = %+v, %v", sum, err)
	}

	deadline := time.After(10 * time.Second)

	for {
		select {
		case _, ok := <-lines:
			if !ok {
				return
			}
		case <-deadline:
			t.Fatal("the stream stayed open after the finished event")
		}
	}
}

func TestSecurity_A04_BenchUICapsSSEConnections(t *testing.T) {
	t.Parallel()

	s, _ := newServer(t, Config{Model: "closed"})
	c, srv := client(t, s)

	for i := range maxStreams {
		lines, closeStream := sseLines(t, c, srv.URL+"/events")
		t.Cleanup(closeStream)

		waitFor(t, lines, "history")

		_ = i
	}

	resp, err := c.Get(srv.URL + "/events")
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("connection %d answered %d, want 429", maxStreams+1, resp.StatusCode)
	}
}

func TestSecurity_A02_BenchUITitleNeverCarriesCredentials(t *testing.T) {
	t.Parallel()

	s, clk := newServer(
		t,
		Config{Title: "GET http://user:hunter2@127.0.0.1:3000/posts?token=abc#frag", Model: "closed"},
	)
	s.Observe(result(200*time.Millisecond, time.Millisecond, 200))
	clk.set(epoch.Add(time.Second))
	s.Tick(0)

	c, srv := client(t, s)

	lines, closeStream := sseLines(t, c, srv.URL+"/events")
	defer closeStream()

	history := waitFor(t, lines, "history")

	resp, err := c.Get(srv.URL + "/report.html")
	if err != nil {
		t.Fatal(err)
	}

	report, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	for _, out := range []string{history, string(report)} {
		for _, secret := range []string{"hunter2", "user:", "@127.0.0.1", "token=abc", "frag"} {
			if strings.Contains(out, secret) {
				t.Errorf("the title leaks %q: %.400s", secret, out)
			}
		}

		if !strings.Contains(out, "127.0.0.1:3000/posts") {
			t.Errorf("the title lost the target:\n%.400s", out)
		}
	}
}

func TestCleanTitle(t *testing.T) {
	t.Parallel()

	for in, want := range map[string]string{
		"GET http://u:p@h:1/x?y=1#z": "GET h:1/x",
		"browse, list_posts":         "browse, list_posts",
		"GET https://a.example/p":    "GET a.example/p",
		"":                           "",
	} {
		if got := cleanTitle(in); got != want {
			t.Errorf("cleanTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSecurity_A04_BenchUIStreamWritesHaveADeadline(t *testing.T) {
	t.Parallel()

	rec := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
	w := &deadlineWriter{ResponseWriter: rec, rc: http.NewResponseController(rec), timeout: 7 * time.Second}

	before := time.Now()

	if _, err := w.Write([]byte("event: x\n\n")); err != nil {
		t.Fatal(err)
	}

	if len(rec.deadlines) != 1 || rec.deadlines[0].Before(before.Add(6*time.Second)) ||
		rec.deadlines[0].After(before.Add(8*time.Second)) {
		t.Fatalf("deadlines = %v, want one about 7s ahead for the write", rec.deadlines)
	}
}

// deadlineRecorder is a ResponseWriter that records the write deadlines set on it.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlines []time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadlines = append(d.deadlines, t)

	return nil
}

func TestSummaryJSONIsTheCurrentSummary(t *testing.T) {
	t.Parallel()

	s, clk := newServer(t, Config{Model: "closed"})
	s.Observe(result(100*time.Millisecond, time.Millisecond, 500))
	clk.set(epoch.Add(time.Second))
	s.Tick(3)

	sum := s.Summary()
	if sum.HTTPReqs != 1 || sum.HTTPReqFailed != 1 || sum.DroppedIterations != 3 || sum.Model != "closed" {
		t.Fatalf("summary = %+v", sum)
	}
}
