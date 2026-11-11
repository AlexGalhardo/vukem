package liveui

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/loadtest"
)

// Options attach a dashboard and a report to one run: the one place vukem bench and bench:run share.
type Options struct {
	// UI serves the live dashboard on 127.0.0.1 and keeps it up after the run until the user quits.
	UI bool
	// Port is the dashboard port; 0 picks a free one.
	Port int
	// NoOpen skips opening the browser.
	NoOpen bool
	// Report is a file to write the self-contained HTML report to when the run ends; empty writes none.
	Report string
	// Title and Model head the pages.
	Title, Model string
	// Thresholds are shown as pass or fail in the dashboard and the report.
	Thresholds []loadtest.Threshold
	// Active, Target and Dropped are the live gauges of the run (loadtest.Runner.InFlight, Target, Dropped); nil is 0.
	Active, Target func() int
	Dropped        func() uint64
	// Version is the vukem version shown in the report.
	Version string
	// Out receives the "dashboard" line with the start URL.
	Out io.Writer
}

// Watch is the dashboard and the series of a run in progress.
type Watch struct {
	opts Options
	srv  *Server

	stopHTTP   func()
	stopTicker context.CancelFunc
	tickerDone chan struct{}
	closeOnce  sync.Once
}

// Begin starts watching a run. With UI it listens on 127.0.0.1, prints the start URL (it carries the session token)
// and opens the browser; without UI it only records the per-second series that Report needs.
func Begin(ctx context.Context, o Options) (*Watch, error) {
	srv, err := New(
		Config{Title: o.Title, Model: o.Model, Thresholds: o.Thresholds, Active: o.Active, Target: o.Target},
	)
	if err != nil {
		return nil, err
	}

	w := &Watch{opts: o, srv: srv, stopHTTP: func() {}, tickerDone: make(chan struct{})}

	if o.UI {
		url, stop, err := srv.Listen(ctx, o.Port)
		if err != nil {
			return nil, err
		}

		w.stopHTTP = stop

		if o.Out != nil {
			fmt.Fprintf(o.Out, "dashboard  %s\n", url)
		}

		if !o.NoOpen {
			OpenBrowser(ctx, url)
		}
	}

	tickCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	w.stopTicker = cancel

	go w.tick(tickCtx)

	return w, nil
}

// tick closes a second of the series every second.
func (w *Watch) tick(ctx context.Context) {
	defer close(w.tickerDone)

	t := time.NewTicker(time.Second)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var dropped uint64
			if w.opts.Dropped != nil {
				dropped = w.opts.Dropped()
			}

			w.srv.Tick(dropped)
		}
	}
}

// Observe records one finished request. Call it from the one goroutine that reads the Results of the run.
func (w *Watch) Observe(r loadtest.Result) { w.srv.Observe(r) }

// Finish ends the run on the dashboard and writes the report file, if one was asked for.
func (w *Watch) Finish(sum loadtest.Summary) error {
	w.stopTicker()
	<-w.tickerDone

	w.srv.Finish(sum)

	if w.opts.Report == "" {
		return nil
	}

	// 0600: a report names the target and the scenarios, and is the operator's to share.
	f, err := os.OpenFile(w.opts.Report, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("report: %w", err)
	}

	werr := WriteHTML(f, w.opts.Title, sum, w.srv.Series(),
		WithThresholds(loadtest.Evaluate(sum, w.opts.Thresholds)), WithVersion(w.opts.Version))

	if cerr := f.Close(); werr == nil {
		werr = cerr
	}

	if werr != nil {
		return fmt.Errorf("report: %w", werr)
	}

	return nil
}

// Hold keeps the dashboard up until ctx is cancelled (Ctrl+C), so the result can be read, then closes everything.
// Without a dashboard it returns at once.
func (w *Watch) Hold(ctx context.Context) {
	if w.opts.UI && w.opts.Out != nil {
		fmt.Fprintln(w.opts.Out, "the run is over; the dashboard stays up: press Ctrl+C to quit")
	}

	if w.opts.UI {
		<-ctx.Done()
	}

	w.Close()
}

// Close stops the server and the ticker; it is safe to call more than once.
func (w *Watch) Close() {
	w.closeOnce.Do(func() {
		w.stopTicker()
		<-w.tickerDone
		w.stopHTTP()
	})
}
