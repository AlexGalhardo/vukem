package logging_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/logging"
)

// recorder is a slog.Handler keeping "level msg key=value..." lines.
type recorder struct {
	mu    *sync.Mutex
	lines *[]string
	attrs []slog.Attr
}

func newRecorder() *recorder {
	return &recorder{mu: &sync.Mutex{}, lines: &[]string{}}
}

func (r *recorder) Enabled(context.Context, slog.Level) bool { return true }

func (r *recorder) Handle(_ context.Context, rec slog.Record) error {
	parts := []string{rec.Level.String(), rec.Message}

	for _, a := range r.attrs {
		parts = append(parts, a.String())
	}

	rec.Attrs(func(a slog.Attr) bool {
		parts = append(parts, a.String())
		return true
	})

	r.mu.Lock()
	*r.lines = append(*r.lines, strings.Join(parts, " "))
	r.mu.Unlock()

	return nil
}

func (r *recorder) WithAttrs(attrs []slog.Attr) slog.Handler {
	c := *r
	c.attrs = append(append([]slog.Attr(nil), r.attrs...), attrs...)

	return &c
}

func (r *recorder) WithGroup(string) slog.Handler { return r }

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]string(nil), *r.lines...)
}

func TestDedup(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")

	tests := []struct {
		name string
		log  func(log *slog.Logger)
		want []string
	}{
		{
			name: "repeats inside the window become one summary",
			log: func(log *slog.Logger) {
				for range 5 {
					log.Error("db down", "error", boom)
				}

				time.Sleep(time.Minute)

				synctest.Wait() // let the window timer flush before the next record
			},
			want: []string{
				"ERROR db down error=boom",
				"ERROR repeated message=db down count=4 window=1m0s error=boom",
			},
		},
		{
			name: "a single record has no summary",
			log: func(log *slog.Logger) {
				log.Error("db down", "error", boom)
				time.Sleep(time.Minute)
				synctest.Wait() // let the window timer flush before the next record
			},
			want: []string{"ERROR db down error=boom"},
		},
		{
			name: "a repeat after the window starts a new one",
			log: func(log *slog.Logger) {
				log.Error("db down", "error", boom)
				log.Error("db down", "error", boom)
				time.Sleep(time.Minute)
				synctest.Wait() // let the window timer flush before the next record
				log.Error("db down", "error", boom)
				time.Sleep(time.Minute)
				synctest.Wait() // let the window timer flush before the next record
			},
			want: []string{
				"ERROR db down error=boom",
				"ERROR repeated message=db down count=1 window=1m0s error=boom",
				"ERROR db down error=boom",
			},
		},
		{
			name: "different event, error or message are distinct",
			log: func(log *slog.Logger) {
				log.Error("failed", "event", "a", "error", boom)
				log.Error("failed", "event", "b", "error", boom)
				log.Error("failed", "event", "a", "error", errors.New("other"))
				log.Error("other", "event", "a", "error", boom)
				log.Error("failed", "event", "a", "error", boom, "user", 7)
				time.Sleep(time.Minute)
				synctest.Wait() // let the window timer flush before the next record
			},
			want: []string{
				"ERROR failed event=a error=boom",
				"ERROR failed event=b error=boom",
				"ERROR failed event=a error=other",
				"ERROR other event=a error=boom",
				"ERROR repeated message=failed count=1 window=1m0s event=a error=boom",
			},
		},
		{
			name: "records below ERROR pass through",
			log: func(log *slog.Logger) {
				log.Warn("slow")
				log.Warn("slow")
			},
			want: []string{"WARN slow", "WARN slow"},
		},
		{
			name: "handler attributes are part of the key",
			log: func(log *slog.Logger) {
				log.With("job", "a").Error("failed")
				log.With("job", "b").Error("failed")
				log.With("job", "a").Error("failed")
				time.Sleep(time.Minute)
				synctest.Wait() // let the window timer flush before the next record
			},
			want: []string{
				"ERROR failed job=a",
				"ERROR failed job=b",
				"ERROR repeated job=a message=failed count=1 window=1m0s",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				rec := newRecorder()
				d := logging.NewDedup(rec, logging.DedupOptions{})

				tt.log(slog.New(d))
				synctest.Wait()

				if diff := cmp.Diff(tt.want, rec.got()); diff != "" {
					t.Errorf("records (-want +got):\n%s", diff)
				}

				if err := d.Close(t.Context()); err != nil {
					t.Errorf("Close() = %v, want nil", err)
				}
			})
		})
	}
}

func TestDedupCloseFlushesPending(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		d := logging.NewDedup(rec, logging.DedupOptions{Window: time.Hour, MinLevel: slog.LevelWarn})
		log := slog.New(d)

		log.Warn("retrying", "event", "mail.retry")
		log.Warn("retrying", "event", "mail.retry")
		log.Warn("retrying", "event", "mail.retry")

		if err := d.Close(t.Context()); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}

		log.Warn("retrying", "event", "mail.retry")

		if err := d.Close(t.Context()); err != nil {
			t.Fatalf("second Close() = %v, want nil", err)
		}

		// No timer is left behind: the bubble would report it otherwise.
		synctest.Wait()

		want := []string{
			"WARN retrying event=mail.retry",
			"WARN repeated message=retrying count=2 window=1h0m0s event=mail.retry",
			"WARN retrying event=mail.retry",
		}
		if diff := cmp.Diff(want, rec.got()); diff != "" {
			t.Errorf("records (-want +got):\n%s", diff)
		}
	})
}

func TestDedupCloseHonoursContext(t *testing.T) {
	t.Parallel()

	rec := newRecorder()
	d := logging.NewDedup(rec, logging.DedupOptions{})
	log := slog.New(d)

	log.Error("x")
	log.Error("x")

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := d.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Errorf("Close(canceled) = %v, want context.Canceled", err)
	}
}

func TestDedupConcurrent(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		d := logging.NewDedup(rec, logging.DedupOptions{})
		log := slog.New(d)

		var wg sync.WaitGroup

		for range 50 {
			wg.Go(func() {
				for range 20 {
					log.Error("db down", "error", "timeout")
				}
			})
		}

		wg.Wait()
		time.Sleep(time.Minute)
		synctest.Wait() // let the window timer flush before the next record
		synctest.Wait()

		want := []string{
			"ERROR db down error=timeout",
			"ERROR repeated message=db down count=999 window=1m0s error=timeout",
		}
		if diff := cmp.Diff(want, rec.got()); diff != "" {
			t.Errorf("records (-want +got):\n%s", diff)
		}
	})
}

func TestDedupKeyLimitPassesThrough(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		rec := newRecorder()
		d := logging.NewDedup(rec, logging.DedupOptions{})
		log := slog.New(d)

		for i := range 1100 {
			log.Error("failed", "event", i)
		}

		log.Error("failed", "event", 1050)

		if got := len(rec.got()); got != 1101 {
			t.Errorf("records = %d, want 1101 (untracked keys pass through)", got)
		}

		if err := d.Close(t.Context()); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})
}

func TestDedupEnabledDelegates(t *testing.T) {
	t.Parallel()

	next := slog.NewTextHandler(nil, &slog.HandlerOptions{Level: slog.LevelWarn})
	d := logging.NewDedup(next, logging.DedupOptions{})

	if d.Enabled(t.Context(), slog.LevelInfo) {
		t.Error("Enabled(INFO) = true, want false")
	}

	if d.WithGroup("") != d || d.WithAttrs(nil) != d {
		t.Error("WithGroup(\"\") / WithAttrs(nil) returned a new handler, want d")
	}

	if _, ok := d.WithGroup("g").(*logging.Dedup); !ok {
		t.Error("WithGroup(g) is not a *Dedup")
	}
}
