// Package sse streams server-sent events (text/event-stream).
//
//	return sse.Serve(w, r, func(s *sse.Stream) error {
//		return s.Send("message", "<li>new post</li>")
//	})
//
// Serve flushes every event, sends heartbeat comments, lifts the server's
// write timeout for the stream and ends when the client disconnects, the
// callback returns or the server shuts down. A Hub fans events out to many
// streams, in memory or through a Broker (drivers/redis) across instances.
package sse

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultHeartbeat is the interval of keep-alive comments.
const DefaultHeartbeat = 15 * time.Second

// Event is one server-sent event.
type Event struct {
	// ID becomes the client's Last-Event-ID on reconnect.
	ID string `json:"id,omitempty"`
	// Event is the event type ("message" when empty).
	Event string `json:"event,omitempty"`
	// Data is the payload; newlines become several data lines.
	Data string `json:"data"`
	// Retry tells the client how long to wait before reconnecting.
	Retry time.Duration `json:"retry,omitempty"`
}

// Stream writes events to one client; it is safe for concurrent use.
type Stream struct {
	ctx  context.Context
	last string

	mu sync.Mutex
	w  io.Writer
	rc *http.ResponseController
}

// Option configures Serve.
type Option func(*options)

type options struct{ heartbeat time.Duration }

// Heartbeat sets the keep-alive interval (zero or negative disables it).
func Heartbeat(d time.Duration) Option { return func(o *options) { o.heartbeat = d } }

type shutdownKey struct{}

// WithShutdown makes streams served with ctx end when stop is closed; the
// app's HTTP server does this so shutdown does not wait for open streams.
func WithShutdown(ctx context.Context, stop <-chan struct{}) context.Context {
	return context.WithValue(ctx, shutdownKey{}, stop)
}

// Serve streams events to the client of r with fn until fn returns, the
// client goes away or the server shuts down (Stream.Context is then done).
func Serve(w http.ResponseWriter, r *http.Request, fn func(*Stream) error, opts ...Option) error {
	o := options{heartbeat: DefaultHeartbeat}
	for _, opt := range opts {
		opt(&o)
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	var wg sync.WaitGroup
	defer wg.Wait() // no write may happen after the handler returns

	if stop, ok := r.Context().Value(shutdownKey{}).(<-chan struct{}); ok {
		wg.Go(func() {
			select {
			case <-stop:
				cancel()
			case <-ctx.Done():
			}
		})
	}

	rc := http.NewResponseController(w)
	// A stream outlives the server's WriteTimeout by design.
	_ = rc.SetWriteDeadline(time.Time{})

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	s := &Stream{ctx: ctx, last: r.Header.Get("Last-Event-ID"), w: w, rc: rc}
	if err := s.write(""); err != nil {
		return nil // the client went away before the first byte
	}

	if o.heartbeat > 0 {
		wg.Go(func() {
			t := time.NewTicker(o.heartbeat)
			defer t.Stop()

			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if s.write(": ping\n\n") != nil {
						cancel()
						return
					}
				}
			}
		})
	}

	err := fn(s)
	cancel()

	return err
}

// Context is done when the client disconnects or the server shuts down.
func (s *Stream) Context() context.Context { return s.ctx }

// LastEventID is the Last-Event-ID header of a reconnecting client.
func (s *Stream) LastEventID() string { return s.last }

// Send writes an event with a type and data.
func (s *Stream) Send(event, data string) error { return s.SendEvent(Event{Event: event, Data: data}) }

// SendJSON writes an event whose data is v encoded as JSON.
func (s *Stream) SendJSON(event string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("sse: encode %s: %w", event, err)
	}

	return s.Send(event, string(b))
}

// SendEvent writes ev and flushes it.
func (s *Stream) SendEvent(ev Event) error {
	if err := s.ctx.Err(); err != nil {
		return err
	}

	return s.write(encode(ev))
}

func (s *Stream) write(payload string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if payload != "" {
		if _, err := io.WriteString(s.w, payload); err != nil {
			return err
		}
	}

	return s.rc.Flush()
}

// encode renders ev in the wire format. Field values lose CR/LF so no
// input can start a new field.
func encode(ev Event) string {
	var b strings.Builder

	if ev.ID != "" {
		b.WriteString("id: " + oneLine(ev.ID) + "\n")
	}

	if ev.Event != "" {
		b.WriteString("event: " + oneLine(ev.Event) + "\n")
	}

	if ev.Retry > 0 {
		b.WriteString("retry: " + strconv.FormatInt(ev.Retry.Milliseconds(), 10) + "\n")
	}

	data := strings.ReplaceAll(strings.ReplaceAll(ev.Data, "\r\n", "\n"), "\r", "\n")
	for line := range strings.SplitSeq(data, "\n") {
		b.WriteString("data: " + line + "\n")
	}

	b.WriteString("\n")

	return b.String()
}

func oneLine(s string) string { return strings.NewReplacer("\r", "", "\n", "").Replace(s) }
