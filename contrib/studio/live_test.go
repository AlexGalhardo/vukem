package studio

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// streamWriter is a flushing ResponseWriter whose body can be read while the
// handler is still running.
type streamWriter struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
	code   int
}

func (w *streamWriter) Header() http.Header { return w.header }

func (w *streamWriter) WriteHeader(code int) { w.code = code }

func (w *streamWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	return w.body.Write(p)
}

func (*streamWriter) Flush() {}

func (w *streamWriter) events() int {
	w.mu.Lock()
	defer w.mu.Unlock()

	return strings.Count(w.body.String(), "event: refresh")
}

// startLive runs /events?view=<view> until the returned cancel is called.
func startLive(t *testing.T, h *harness, view string) (*streamWriter, context.CancelFunc, <-chan struct{}) {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())

	r := request(http.MethodGet, "/events?view="+view).WithContext(ctx)
	r.AddCookie(h.login)

	w := &streamWriter{header: http.Header{}}
	done := make(chan struct{})

	go func() {
		defer close(done)

		h.s.Handler().ServeHTTP(w, r)
	}()

	return w, cancel, done
}

func TestSecurity_A01_StudioLiveRejectsForeignView(t *testing.T) {
	t.Parallel()

	h := newHarness(t, true)

	for _, view := range []string{
		"", "https://evil.example/", "//evil.example/s/x", "/assets/studio.css", "/", "/events", "/s", "/s/mongo/c/users/d/x",
		`/s/%5Cevil`, "/s/mongo\\x", "http://127.0.0.1/s/mongo", "/s/mongo/mongo-console", "s/mongo",
	} {
		r := request(http.MethodGet, "/events?view="+strings.ReplaceAll(view, " ", "%20"))
		r.AddCookie(h.login)

		if rec := serve(h.s, r); rec.Code != http.StatusBadRequest {
			t.Errorf("view %q: status = %d, want 400", view, rec.Code)
		}
	}
}

func TestSecurity_A04_StudioLiveCapsConnections(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	h.s.liveConns.Store(maxLiveConns)

	r := request(http.MethodGet, "/events?view=/s/mongo")
	r.AddCookie(h.login)

	if rec := serve(h.s, r); rec.Code != http.StatusTooManyRequests {
		t.Errorf("the ninth stream = %d, want 429", rec.Code)
	}

	if got := h.s.liveConns.Load(); got != maxLiveConns {
		t.Errorf("a refused stream left the counter at %d", got)
	}
}

func TestLiveSendsRefreshOnlyOnChange(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)

	tick := make(chan time.Time) // unbuffered: sending a tick waits for the loop
	h.s.newTicker = func(time.Duration) (<-chan time.Time, func()) { return tick, func() {} }

	w, cancel, done := startLive(t, h, "/s/mongo/c/users")

	defer func() { cancel(); <-done }()

	step := func() { tick <- time.Time{} }

	step()
	step() // barrier: the first tick is fully processed once the second is taken

	if n := w.events(); n != 0 {
		t.Fatalf("%d events after two ticks with unchanged data, want 0", n)
	}

	h.fm.addDoc(rawDoc(t, int32(3), "grace"))

	step()
	step()

	if n := w.events(); n != 1 {
		t.Errorf("%d events after the data changed, want 1", n)
	}

	step()
	step()

	if n := w.events(); n != 1 {
		t.Errorf("%d events after another quiet tick, want still 1", n)
	}

	if !strings.Contains(w.body.String(), `"view":"/s/mongo/c/users"`) {
		t.Errorf("event payload = %q", w.body.String())
	}
}

func TestLiveStopsOnClientDisconnect(t *testing.T) {
	t.Parallel()

	h := newHarness(t, false)
	h.s.newTicker = func(time.Duration) (<-chan time.Time, func()) { return make(chan time.Time), func() {} }

	_, cancel, done := startLive(t, h, "/s/mongo")

	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the stream goroutine kept running after the client went away")
	}

	if got := h.s.liveConns.Load(); got != 0 {
		t.Errorf("liveConns = %d after the disconnect, want 0", got)
	}
}

func TestRefreshInterval(t *testing.T) {
	t.Parallel()

	for in, want := range map[time.Duration]time.Duration{
		0: defaultRefresh, -1: 0, time.Millisecond: minRefresh, 3 * time.Second: 3 * time.Second,
	} {
		if got := refreshInterval(in); got != want {
			t.Errorf("refreshInterval(%v) = %v, want %v", in, got, want)
		}
	}
}
