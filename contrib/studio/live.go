package studio

import (
	"context"
	"crypto/sha256"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/sse"
)

const (
	maxLiveConns   = 8
	minRefresh     = 500 * time.Millisecond
	defaultRefresh = 2 * time.Second
	liveHeartbeat  = 15 * time.Second
)

// ticker is a source of refresh ticks; tests replace it.
type ticker func(d time.Duration) (<-chan time.Time, func())

func realTicker(d time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(d)
	return t.C, t.Stop
}

// refreshInterval normalizes Config.Refresh: 0 means the default (a negative
// value turns live updates off), and anything shorter than 500 ms is raised.
func refreshInterval(d time.Duration) time.Duration {
	switch {
	case d < 0:
		return 0
	case d == 0:
		return defaultRefresh
	default:
		return max(d, minRefresh)
	}
}

// events streams "refresh" events for one view: the server runs the view's
// own GET handler every interval, hashes the answer, and tells the page only
// when it changed. The view must be a /s/... path that one of this server's
// read routes serves; anything else is 400. At most eight streams run at once.
func (s *Studio) events(w http.ResponseWriter, r *http.Request) {
	view := r.URL.Query().Get("view")

	if !s.validView(view) {
		http.Error(w, "bad view", http.StatusBadRequest)
		return
	}

	every := refreshInterval(s.cfg.Refresh)
	if every == 0 {
		http.Error(w, "live updates are off", http.StatusNotFound)
		return
	}

	if s.liveConns.Add(1) > maxLiveConns {
		s.liveConns.Add(-1)
		http.Error(w, "too many live views", http.StatusTooManyRequests)

		return
	}

	defer s.liveConns.Add(-1)

	_ = sse.Serve(w, r, func(st *sse.Stream) error {
		return s.watch(st, view, every)
	}, sse.Heartbeat(liveHeartbeat))
}

// validView accepts only a same-origin path under /s/ that a registered read
// route answers: an absolute URL, a //host, /assets or the write routes
// never match.
func (s *Studio) validView(view string) bool {
	if !strings.HasPrefix(view, "/s/") || strings.HasPrefix(view, "//") || strings.Contains(view, `\`) {
		return false
	}

	u, err := url.Parse(view)
	if err != nil || u.Scheme != "" || u.Host != "" {
		return false
	}

	// The request is served in this process and never sent anywhere.
	req, err := http.NewRequestWithContext( //nolint:gosec // G704: served in process, never sent
		context.Background(),
		http.MethodGet,
		view,
		http.NoBody,
	)
	if err != nil {
		return false
	}

	_, pattern := s.mux.Handler(req)

	return s.livePatterns[pattern]
}

// snapshot runs the view's handler and returns a hash of what it answered.
func (s *Studio) snapshot(ctx context.Context, view string) [32]byte {
	req, err := http.NewRequestWithContext( //nolint:gosec // G704: served in process, never sent
		ctx,
		http.MethodGet,
		view,
		http.NoBody,
	)
	if err != nil {
		return [32]byte{}
	}

	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	return sha256.Sum256(append([]byte(strconv.Itoa(rec.Code)+" "), rec.Body.Bytes()...))
}

// watch sends one event per change until the client goes away.
func (s *Studio) watch(st *sse.Stream, view string, every time.Duration) error {
	ctx := st.Context()
	ticks, stop := s.newTicker(every)

	defer stop()

	last := s.snapshot(ctx, view)

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticks:
			if now := s.snapshot(ctx, view); now != last {
				last = now

				if err := st.SendJSON("refresh", map[string]string{"view": view}); err != nil {
					return nil
				}
			}
		}
	}
}
