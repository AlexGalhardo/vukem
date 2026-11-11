package liveui

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/AlexGalhardo/vukem/loadtest"
	"github.com/AlexGalhardo/vukem/sse"
)

//go:embed templates assets
var assetFS embed.FS

// maxStreams is how many event streams may be open at once: a page, a reload and a few extra tabs.
const maxStreams = 4

// pollInterval is how often an open stream looks for a second that closed.
const pollInterval = 250 * time.Millisecond

// Config configures the dashboard of one run.
type Config struct {
	// Title is shown in the header: the method and target, or the scenario names.
	Title string
	// Model is "open" or "closed".
	Model string
	// Thresholds are evaluated against every summary and shown as pass or fail.
	Thresholds []loadtest.Threshold
	// Active returns the requests in flight; nil means 0.
	Active func() int
	// Target returns the arrivals per second asked for (open) or the virtual users (closed); nil means 0.
	Target func() int
	// Now is the clock (tests); nil means time.Now.
	Now func() time.Time
}

// Server is the dashboard of one run: the per-second series, the cumulative summary and the HTTP handler.
type Server struct {
	cfg    Config
	token  string
	now    func() time.Time
	series *series

	streams   atomic.Int32
	tokenUsed atomic.Bool
	sessMu    sync.Mutex
	sessions  []sessionEntry

	mu       sync.Mutex
	dropped  uint64
	final    *loadtest.Summary
	finished chan struct{}
	finish   sync.Once
}

// New returns a Server with a fresh session token. The run starts now: second 0 begins at Config.Now.
func New(cfg Config) (*Server, error) {
	tok, err := newToken()
	if err != nil {
		return nil, fmt.Errorf("liveui: session token: %w", err)
	}

	now := cfg.Now
	if now == nil {
		now = time.Now
	}

	// The title reaches the page, the stream and the report: it never carries credentials, a query or a fragment.
	cfg.Title = cleanTitle(cfg.Title)

	return &Server{cfg: cfg, token: tok, now: now, series: newSeries(now()), finished: make(chan struct{})}, nil
}

// Token returns the session token that the start URL carries.
func (s *Server) Token() string { return s.token }

// Observe records one finished request.
func (s *Server) Observe(r loadtest.Result) { s.series.observe(r) }

// Tick closes the seconds that have passed and notes the gauges; dropped is the cumulative dropped_iterations. Call it
// about once a second.
func (s *Server) Tick(dropped uint64) {
	s.mu.Lock()
	s.dropped = dropped
	s.mu.Unlock()

	s.series.tick(s.now(), dropped, call(s.cfg.Active), call(s.cfg.Target))
}

func call(f func() int) int {
	if f == nil {
		return 0
	}

	return f()
}

// Feed observes results until the channel closes and ticks every second: call it in a goroutine.
func (s *Server) Feed(results <-chan loadtest.Result, dropped func() uint64) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()

	for {
		select {
		case r, ok := <-results:
			if !ok {
				return
			}

			s.Observe(r)
		case <-tick.C:
			s.Tick(dropped())
		}
	}
}

// Finish records the final summary, closes the partial last second and tells the open streams the run is over.
func (s *Server) Finish(sum loadtest.Summary) {
	s.series.finish(s.now(), sum.DroppedIterations)

	s.mu.Lock()
	s.final = &sum
	s.mu.Unlock()

	s.finish.Do(func() { close(s.finished) })
}

// Series returns the seconds recorded so far, for WriteHTML.
func (s *Server) Series() []Snapshot { return s.series.snapshots() }

// Summary returns the final summary once the run finished, and the cumulative one until then.
func (s *Server) Summary() loadtest.Summary {
	s.mu.Lock()
	final, dropped := s.final, s.dropped
	s.mu.Unlock()

	if final != nil {
		return *final
	}

	sum := s.series.summary(s.now().Sub(s.series.start), dropped)
	sum.Model, sum.Target = s.cfg.Model, s.cfg.Title

	return sum
}

// Handler returns the dashboard: the protection chain around the routes. Every route is a GET.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /{$}", s.page)
	mux.HandleFunc("GET /events", s.events)
	mux.HandleFunc("GET /summary.json", s.summaryJSON)
	mux.HandleFunc("GET /report.html", s.report)
	mux.HandleFunc("GET /assets/{file}", s.asset)

	return s.protect(mux)
}

func (s *Server) page(w http.ResponseWriter, _ *http.Request) {
	b, err := assetFS.ReadFile("templates/live.html")
	if err != nil {
		http.Error(w, "page missing", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(b)
}

func (s *Server) asset(w http.ResponseWriter, r *http.Request) {
	types := map[string]string{"live.css": "text/css; charset=utf-8", "live.js": "text/javascript; charset=utf-8"}

	name := r.PathValue("file")

	ct, ok := types[name]
	if !ok {
		http.NotFound(w, r)

		return
	}

	b, err := assetFS.ReadFile("assets/" + name)
	if err != nil {
		http.NotFound(w, r)

		return
	}

	w.Header().Set("Content-Type", ct)
	_, _ = w.Write(b) //nolint:gosec // G705: an embedded file, not user input
}

func (s *Server) summaryJSON(w http.ResponseWriter, _ *http.Request) {
	var b bytes.Buffer
	if err := loadtest.WriteJSON(&b, s.Summary()); err != nil {
		http.Error(w, "summary failed", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b.Bytes())
}

func (s *Server) report(w http.ResponseWriter, _ *http.Request) {
	sum := s.Summary()

	var b bytes.Buffer
	if err := WriteHTML(
		&b,
		s.cfg.Title,
		sum,
		s.Series(),
		WithThresholds(loadtest.Evaluate(sum, s.cfg.Thresholds)),
	); err != nil {
		http.Error(w, "report failed", http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="bench-report.html"`)
	_, _ = w.Write(b.Bytes())
}

// thresholdJSON is a threshold verdict as the page reads it.
type thresholdJSON struct {
	Threshold string `json:"threshold"`
	Got       string `json:"got"`
	OK        bool   `json:"ok"`
}

func (s *Server) verdicts(sum loadtest.Summary) []thresholdJSON {
	results := loadtest.Evaluate(sum, s.cfg.Thresholds)
	out := make([]thresholdJSON, len(results))

	for i, r := range results {
		out[i] = thresholdJSON{Threshold: r.Threshold, Got: r.Got, OK: r.OK}
	}

	return out
}

// events streams the run: the seconds so far, then one snapshot per second that closes, a summary every two seconds
// and, once, the final summary.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if s.streams.Add(1) > maxStreams {
		s.streams.Add(-1)
		http.Error(w, "too many open dashboards", http.StatusTooManyRequests)

		return
	}

	defer s.streams.Add(-1)

	// A stream outlives the server's write timeout by design, so each event carries its own deadline: a client that
	// stops reading is dropped instead of holding a goroutine for good.
	w = &deadlineWriter{ResponseWriter: w, rc: http.NewResponseController(w), timeout: streamWriteTimeout}

	_ = sse.Serve(w, r, s.stream, sse.Heartbeat(15*time.Second))
}

// streamWriteTimeout is how long one event may take to reach the client.
const streamWriteTimeout = 10 * time.Second

// deadlineWriter sets a write deadline before every write.
type deadlineWriter struct {
	http.ResponseWriter

	rc      *http.ResponseController
	timeout time.Duration
}

func (d *deadlineWriter) Write(p []byte) (int, error) {
	_ = d.rc.SetWriteDeadline(time.Now().Add(d.timeout))

	return d.ResponseWriter.Write(p)
}

// Unwrap lets http.ResponseController reach Flush and the deadlines of the real writer.
func (d *deadlineWriter) Unwrap() http.ResponseWriter { return d.ResponseWriter }

// cleanTitle shows every URL in a title as host and path: no scheme, no credentials, no query, no fragment.
func cleanTitle(title string) string {
	words := strings.Split(title, " ")

	for i, w := range words {
		if strings.HasPrefix(w, "http://") || strings.HasPrefix(w, "https://") {
			words[i] = displayTarget(w)
		}
	}

	return strings.Join(words, " ")
}

func (s *Server) stream(st *sse.Stream) error {
	snaps := s.series.snapshots()

	titles := struct {
		Snapshots  []Snapshot `json:"snapshots"`
		Title      string     `json:"title"`
		Model      string     `json:"model"`
		Thresholds []string   `json:"thresholds"`
	}{Snapshots: snaps, Title: s.cfg.Title, Model: s.cfg.Model}

	for _, v := range s.verdicts(loadtest.Summary{}) {
		titles.Thresholds = append(titles.Thresholds, v.Threshold)
	}

	if err := st.SendJSON("history", titles); err != nil {
		return err
	}

	last := -1
	if len(snaps) > 0 {
		last = snaps[len(snaps)-1].Second
	}

	poll := time.NewTicker(pollInterval)
	defer poll.Stop()

	nextSummary := time.Time{}

	for {
		select {
		case <-st.Context().Done():
			return nil
		case <-s.finished:
			return s.sendFinal(st, last)
		case <-poll.C:
			var err error
			if last, err = s.sendSnapshots(st, last); err != nil {
				return err
			}

			if now := time.Now(); now.After(nextSummary) {
				nextSummary = now.Add(2 * time.Second)

				if err := s.sendSummary(st); err != nil {
					return err
				}
			}
		}
	}
}

func (s *Server) sendSnapshots(st *sse.Stream, last int) (int, error) {
	for _, sn := range s.series.since(last) {
		if err := st.SendJSON("snapshot", sn); err != nil {
			return last, err
		}

		last = sn.Second
	}

	return last, nil
}

func (s *Server) sendSummary(st *sse.Stream) error {
	sum := s.Summary()

	return st.SendJSON("summary", map[string]any{"summary": sum, "thresholds": s.verdicts(sum)})
}

func (s *Server) sendFinal(st *sse.Stream, last int) error {
	if _, err := s.sendSnapshots(st, last); err != nil {
		return err
	}

	if err := s.sendSummary(st); err != nil {
		return err
	}

	return st.SendJSON("finished", s.Summary())
}

// Listen serves the dashboard on 127.0.0.1 (port 0 picks a free one) and returns the start URL, which carries the
// token, and a function that shuts the server down. There is no way to listen on another address.
func (s *Server) Listen(ctx context.Context, port int) (string, func(), error) {
	if port < 0 || port > 65535 {
		return "", nil, errors.New("liveui: invalid port")
	}

	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return "", nil, err
	}

	srv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		MaxHeaderBytes:    16 << 10,
	}

	go func() { _ = srv.Serve(ln) }()

	stop := func() {
		shutdown, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()

		if srv.Shutdown(shutdown) != nil {
			_ = srv.Close()
		}
	}

	return "http://" + ln.Addr().String() + "/?token=" + s.token, stop, nil
}

// OpenBrowser asks the system to open url. A failure is ignored: the URL is printed for the user anyway. The URL carries
// the session token and goes on the command line of the launcher (start, open or xdg-open), where another local user
// can read it from the process list for the moment the launcher runs. The token works once and the first browser tab
// to use it wins, so the window is short; pass --no-open to keep the URL in the terminal only.
func OpenBrowser(ctx context.Context, url string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		// The empty title keeps start from reading the URL as one.
		cmd = exec.CommandContext(ctx, "cmd", "/c", "start", "", url)
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", url)
	default:
		cmd = exec.CommandContext(ctx, "xdg-open", url)
	}

	if cmd.Start() == nil {
		go func() { _ = cmd.Wait() }()
	}
}
