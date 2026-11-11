package liveui

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/loadtest"
)

// lockedBuffer is a Writer that a test can read while the code under test still writes to it.
type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.b.String()
}

func startURL(t *testing.T, out *lockedBuffer) string {
	t.Helper()

	deadline := time.Now().Add(5 * time.Second)

	for time.Now().Before(deadline) {
		for line := range strings.SplitSeq(out.String(), "\n") {
			if rest, ok := strings.CutPrefix(line, "dashboard  "); ok {
				return strings.TrimSpace(rest)
			}
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("no dashboard line in %q", out.String())

	return ""
}

func TestWatchServesTheDashboardAndHolds(t *testing.T) {
	t.Parallel()

	out := &lockedBuffer{}
	ctx, cancel := context.WithCancel(t.Context())

	defer cancel()

	w, err := Begin(
		ctx,
		Options{
			UI:      true,
			NoOpen:  true,
			Title:   "GET 127.0.0.1/",
			Model:   "closed",
			Out:     out,
			Dropped: func() uint64 { return 0 },
		},
	)
	if err != nil {
		t.Fatal(err)
	}

	url := startURL(t, out)
	if !strings.HasPrefix(url, "http://127.0.0.1:") || !strings.Contains(url, "/?token=") {
		t.Fatalf("start URL = %q, want a loopback URL carrying the token", url)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}

	c := &http.Client{Jar: jar}

	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}

	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("the start URL answered %d", resp.StatusCode)
	}

	w.Observe(
		loadtest.Result{
			Sent:     time.Now(),
			Service:  time.Millisecond,
			Duration: time.Millisecond,
			Status:   200,
			Name:     "GET /",
		},
	)

	if err := w.Finish(loadtest.Summary{SchemaVersion: 1, HTTPReqs: 1}); err != nil {
		t.Fatal(err)
	}

	held := make(chan struct{})

	go func() {
		w.Hold(ctx)
		close(held)
	}()

	select {
	case <-held:
		t.Fatal("Hold returned before the user quit: the dashboard must stay up to be read")
	case <-time.After(200 * time.Millisecond):
	}

	resp, err = c.Get(strings.SplitN(url, "/?", 2)[0] + "/summary.json")
	if err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"http_reqs": 1`) {
		t.Fatalf("summary.json while held = %d %s", resp.StatusCode, body)
	}

	cancel()

	select {
	case <-held:
	case <-time.After(10 * time.Second):
		t.Fatal("Hold did not return after the user quit")
	}

	if resp, err := (&http.Client{Timeout: time.Second}).Get(url); err == nil {
		_ = resp.Body.Close()

		t.Fatal("the dashboard still answers after Hold returned")
	}
}

func TestWatchWritesTheReportWithoutTheDashboard(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "out.html")
	out := &lockedBuffer{}

	w, err := Begin(t.Context(), Options{Report: path, Title: "t", Model: "closed", Out: out, Version: "v1.2.3"})
	if err != nil {
		t.Fatal(err)
	}

	w.Observe(
		loadtest.Result{
			Sent:     time.Now(),
			Service:  time.Millisecond,
			Duration: time.Millisecond,
			Status:   200,
			Name:     "GET /",
		},
	)

	if err := w.Finish(loadtest.Summary{SchemaVersion: 1, HTTPReqs: 1, Model: "closed", Concurrency: 1}); err != nil {
		t.Fatal(err)
	}

	// Without --ui there is nothing to wait for.
	done := make(chan struct{})

	go func() {
		w.Hold(t.Context())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Hold waited without a dashboard")
	}

	if strings.Contains(out.String(), "dashboard") {
		t.Fatalf("no dashboard was asked for: %q", out.String())
	}

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	if !strings.Contains(string(b), "<svg") || !strings.Contains(string(b), "v1.2.3") {
		t.Fatalf("report = %s", b)
	}

	if info, err := os.Stat(path); err != nil || info.Mode().Perm()&0o077 != 0 && runtime.GOOS != "windows" {
		t.Fatalf("the report is readable by others: %v %v", info, err)
	}
}

func TestWatchReportFailureIsAnError(t *testing.T) {
	t.Parallel()

	w, err := Begin(
		t.Context(),
		Options{Report: filepath.Join(t.TempDir(), "missing", "dir", "out.html"), Model: "closed"},
	)
	if err != nil {
		t.Fatal(err)
	}

	if err := w.Finish(loadtest.Summary{SchemaVersion: 1}); err == nil {
		t.Fatal("an unwritable report was not reported")
	}

	w.Close()
}

func TestBeginRefusesABadPort(t *testing.T) {
	t.Parallel()

	if _, err := Begin(t.Context(), Options{UI: true, Port: 70000, NoOpen: true, Out: io.Discard}); err == nil {
		t.Fatal("port 70000 was accepted")
	}
}
