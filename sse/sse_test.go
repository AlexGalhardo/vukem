package sse_test

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/sse"
)

// flushRecorder is a ResponseWriter whose body can be read while the
// handler is still streaming.
type flushRecorder struct {
	mu     sync.Mutex
	header http.Header
	status int
	body   strings.Builder
	flushN int
}

func newRecorder() *flushRecorder { return &flushRecorder{header: http.Header{}} }

func (f *flushRecorder) Header() http.Header { return f.header }
func (f *flushRecorder) WriteHeader(code int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.status == 0 {
		f.status = code
	}
}

func (f *flushRecorder) Write(b []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.status == 0 {
		f.status = http.StatusOK
	}

	return f.body.Write(b)
}

func (f *flushRecorder) Flush() { f.mu.Lock(); f.flushN++; f.mu.Unlock() }

func (f *flushRecorder) String() string {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.body.String()
}

func TestServeWritesEvents(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/events", http.NoBody)
	r.Header.Set("Last-Event-ID", "41")

	err := sse.Serve(rec, r, func(s *sse.Stream) error {
		if s.LastEventID() != "41" {
			t.Errorf("LastEventID() = %q", s.LastEventID())
		}

		if err := s.Send("greeting", "hello\nworld"); err != nil {
			return err
		}

		if err := s.SendJSON("count", map[string]int{"n": 1}); err != nil {
			return err
		}

		return s.SendEvent(sse.Event{ID: "42", Data: "plain", Retry: 3 * time.Second})
	})
	if err != nil {
		t.Fatal(err)
	}

	want := "event: greeting\ndata: hello\ndata: world\n\n" +
		"event: count\ndata: {\"n\":1}\n\n" +
		"id: 42\nretry: 3000\ndata: plain\n\n"

	if diff := cmp.Diff(want, rec.String()); diff != "" {
		t.Errorf("stream mismatch (-want +got):\n%s", diff)
	}

	h := rec.Header()
	if h.Get("Content-Type") != "text/event-stream" || h.Get("Cache-Control") != "no-cache" ||
		h.Get("X-Accel-Buffering") != "no" || rec.flushN < 3 {
		t.Errorf("headers = %v, flushes = %d", h, rec.flushN)
	}
}

func TestSecurity_A05_SSEFieldInjection(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	r := httptest.NewRequestWithContext(t.Context(), http.MethodGet, "/", http.NoBody)

	_ = sse.Serve(rec, r, func(s *sse.Stream) error {
		return s.SendEvent(sse.Event{ID: "1\ndata: forged", Event: "x\r\nevent: admin", Data: "ok\r\nevent: evil"})
	})

	for line := range strings.Lines(rec.String()) {
		if strings.HasPrefix(line, "event: admin") || strings.HasPrefix(line, "event: evil") ||
			strings.HasPrefix(line, "data: forged") {
			t.Errorf("injected field line %q in:\n%s", line, rec.String())
		}
	}
}

func TestHeartbeatAndDisconnect(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		rec := newRecorder()
		r := httptest.NewRequestWithContext(ctx, http.MethodGet, "/", http.NoBody)

		done := make(chan error, 1)
		go func() {
			done <- sse.Serve(rec, r, func(s *sse.Stream) error {
				<-s.Context().Done()
				return nil
			}, sse.Heartbeat(10*time.Second))
		}()

		time.Sleep(25 * time.Second)
		synctest.Wait()

		if n := strings.Count(rec.String(), ": ping\n\n"); n != 2 {
			t.Errorf("heartbeats after 25s = %d, want 2 (%q)", n, rec.String())
		}

		cancel()

		if err := <-done; err != nil {
			t.Errorf("Serve after client disconnect = %v", err)
		}
	})
}

func TestServeStopsOnShutdown(t *testing.T) {
	t.Parallel()

	stop := make(chan struct{})
	r := httptest.NewRequestWithContext(sse.WithShutdown(t.Context(), stop), http.MethodGet, "/", http.NoBody)

	done := make(chan struct{})
	go func() {
		_ = sse.Serve(newRecorder(), r, func(s *sse.Stream) error {
			<-s.Context().Done()
			return nil
		})
		close(done)
	}()

	close(stop)

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("stream kept running after shutdown")
	}
}

func TestShutdownSignal(t *testing.T) {
	t.Parallel()

	if got := sse.ShutdownSignal(t.Context()); got != nil {
		t.Errorf("ShutdownSignal(no signal) = %v, want nil", got)
	}

	stop := make(chan struct{})
	got := sse.ShutdownSignal(sse.WithShutdown(t.Context(), stop))

	if got == nil {
		t.Fatal("ShutdownSignal(WithShutdown) = nil, want the stop channel")
	}

	close(stop)

	select {
	case <-got:
	default:
		t.Error("ShutdownSignal channel still open after stop was closed")
	}
}

type memBroker struct {
	mu   sync.Mutex
	subs map[string][]chan []byte
}

func (b *memBroker) Publish(_ context.Context, channel string, payload []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, ch := range b.subs[channel] {
		ch <- payload
	}

	return nil
}

func (b *memBroker) Subscribe(_ context.Context, channels ...string) (<-chan []byte, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.subs == nil {
		b.subs = map[string][]chan []byte{}
	}

	ch := make(chan []byte, 8)
	for _, c := range channels {
		b.subs[c] = append(b.subs[c], ch)
	}

	return ch, nil
}

func TestHub(t *testing.T) {
	t.Parallel()

	for name, broker := range map[string]sse.Broker{"memory": nil, "broker": &memBroker{}} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			hub := sse.NewHub(broker)
			t.Cleanup(hub.Close)

			a, stopA := hub.Subscribe(t.Context(), "posts")
			b, stopB := hub.Subscribe(t.Context(), "posts", "users")
			defer stopB()

			if err := hub.Publish(t.Context(), "posts", sse.Event{Event: "created", Data: "1"}); err != nil {
				t.Fatal(err)
			}

			for _, ch := range []<-chan sse.Event{a, b} {
				select {
				case ev := <-ch:
					if ev.Event != "created" || ev.Data != "1" {
						t.Errorf("event = %+v", ev)
					}
				case <-time.After(5 * time.Second):
					t.Fatal("event not delivered")
				}
			}

			stopA()

			if err := hub.Publish(t.Context(), "users", sse.Event{Data: "2"}); err != nil {
				t.Fatal(err)
			}

			select {
			case ev := <-b:
				if ev.Data != "2" {
					t.Errorf("users event = %+v", ev)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("users event not delivered")
			}

			if _, open := <-a; open {
				t.Error("unsubscribed channel still open")
			}
		})
	}
}

func TestHubStreamEndsOnClose(t *testing.T) {
	t.Parallel()

	hub := sse.NewHub(nil)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = sse.Serve(w, r, func(s *sse.Stream) error { return hub.Stream(s, "posts") })
	}))
	t.Cleanup(srv.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}

	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = res.Body.Close() })

	lines := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}

		close(lines)
	}()

	deadline := time.After(5 * time.Second)
	for hub.Subscribers("posts") == 0 {
		select {
		case <-deadline:
			t.Fatal("stream never subscribed")
		default:
			time.Sleep(time.Millisecond) //nolint:forbidigo // waits on a real HTTP server goroutine
		}
	}

	if err := hub.Publish(t.Context(), "posts", sse.Event{Event: "created", Data: "7"}); err != nil {
		t.Fatal(err)
	}

	got := []string{<-lines, <-lines}
	if diff := cmp.Diff([]string{"event: created", "data: 7"}, got); diff != "" {
		t.Errorf("stream lines (-want +got):\n%s", diff)
	}

	hub.Close()

	for range lines { // drain until the server ends the response
	}

	if _, err := io.ReadAll(res.Body); err != nil && !errors.Is(err, io.EOF) {
		t.Errorf("read after close = %v", err)
	}
}
