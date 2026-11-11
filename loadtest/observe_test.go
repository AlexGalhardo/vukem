package loadtest

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIsLoopbackHost(t *testing.T) {
	t.Parallel()

	for _, h := range []string{"localhost", "localhost:3000", "app.localhost", "127.0.0.1", "127.0.0.1:80", "[::1]:80", "::1"} {
		if !IsLoopbackHost(h) {
			t.Errorf("IsLoopbackHost(%q) = false", h)
		}
	}

	for _, h := range []string{"example.com", "10.0.0.1:80", "evil.localhost.example.com", "", "0.0.0.0"} {
		if IsLoopbackHost(h) {
			t.Errorf("IsLoopbackHost(%q) = true", h)
		}
	}
}

func TestRunnerTargetAndInFlight(t *testing.T) {
	t.Parallel()

	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}

		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	r, err := Start(t.Context(), Config{Concurrency: 3, Duration: time.Hour}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	if r.Target() != 3 {
		t.Errorf("closed Target = %d, want the 3 workers", r.Target())
	}

	deadline := time.Now().Add(5 * time.Second)
	for r.InFlight() != 3 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}

	if r.InFlight() != 3 {
		t.Errorf("InFlight = %d while 3 requests hang, want 3", r.InFlight())
	}

	close(release)
	r.Stop()

	if r.InFlight() != 0 {
		t.Errorf("InFlight = %d after the run, want 0", r.InFlight())
	}

	open, err := Start(t.Context(), Config{Rate: Rate{50, time.Second}, Requests: 1}, get(srv))
	if err != nil {
		t.Fatal(err)
	}

	if open.Target() != 50 {
		t.Errorf("open Target = %d, want 50", open.Target())
	}

	collect(t, open)
}
