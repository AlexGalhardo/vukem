package schedule_test

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/schedule"
)

type prune struct{}

func (prune) Kind() string { return "prune" }

func newSchedule(o schedule.Options) *schedule.Schedule {
	o.Log = slog.New(slog.DiscardHandler)
	return schedule.New(o)
}

func TestHelpersBuildExpressions(t *testing.T) {
	t.Parallel()

	s := newSchedule(schedule.Options{})
	noop := func(context.Context) error { return nil }

	got := []string{
		s.Func("a", noop).Expression(),
		s.Func("b", noop).Every(15 * time.Minute).Expression(),
		s.Func("c", noop).Every(2 * time.Hour).Expression(),
		s.Func("d", noop).Hourly().Expression(),
		s.Func("e", noop).HourlyAt(7).Expression(),
		s.Func("f", noop).Daily().Expression(),
		s.Func("g", noop).DailyAt("08:05").Weekdays().Expression(),
		s.Func("h", noop).WeeklyOn(time.Monday, "9:00").Expression(),
		s.Func("i", noop).MonthlyOn(1, "00:00").Expression(),
		s.Func("j", noop).EveryMinute().EveryFiveMinutes().Expression(),
		s.Func("k", noop).Cron("@daily").Weekdays().Expression(),
	}
	want := []string{
		"* * * * *", "*/15 * * * *", "0 */2 * * *", "0 * * * *", "7 * * * *", "0 0 * * *", "5 8 * * 1-5",
		"0 9 * * 1", "0 0 1 * *", "*/5 * * * *", "0 0 * * 1-5",
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("expressions (-want +got):\n%s", diff)
	}

	for name, fn := range map[string]func(){
		"Every 7m":    func() { s.Func("x", noop).Every(7 * time.Minute) },
		"Every 30s":   func() { s.Func("x", noop).Every(30 * time.Second) },
		"DailyAt bad": func() { s.Func("x", noop).DailyAt("25:00") },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s did not panic", name)
				}
			}()

			fn()
		}()
	}
}

func TestRunDueCommandsJobsAndZones(t *testing.T) {
	t.Parallel()

	var (
		mu    sync.Mutex
		calls []string
	)

	record := func(s string) {
		mu.Lock()
		calls = append(calls, s)
		mu.Unlock()
	}

	fake := queue.NewFake()
	q := queue.New(fake, slog.New(slog.DiscardHandler))
	_ = q.Register(queue.Handle(func(context.Context, prune) error { return nil }))

	saoPaulo := time.FixedZone("BRT", -3*3600)
	s := newSchedule(schedule.Options{
		Location: saoPaulo,
		Commands: func(_ context.Context, name string, args []string) error {
			record(name + " " + strings.Join(args, ","))
			return nil
		},
		Queue: func() (*queue.Queue, error) { return q, nil },
	})

	s.Command("reports:send", "--daily").DailyAt("08:00")
	s.Job(prune{}).DailyAt("08:00")
	s.Func("utc", func(context.Context) error { record("utc"); return nil }).DailyAt("08:00").In(time.UTC)
	s.Func("boom", func(context.Context) error { return errors.New("boom") }).DailyAt("08:00")

	// 11:00 UTC is 08:00 in São Paulo.
	err := s.RunDue(t.Context(), at("2026-10-16 11:00"))
	if err == nil || !strings.Contains(err.Error(), "boom: boom") {
		t.Fatalf("RunDue error = %v", err)
	}

	if diff := cmp.Diff(
		[]string{"reports:send --daily"},
		calls,
	); diff != "" ||
		len(queue.Dispatched[prune](fake)) != 1 {
		t.Errorf("calls (-want +got):\n%s jobs=%d", diff, len(queue.Dispatched[prune](fake)))
	}

	calls = nil
	_ = s.RunDue(t.Context(), at("2026-10-16 08:00"))

	if diff := cmp.Diff([]string{"utc"}, calls); diff != "" {
		t.Errorf("utc task (-want +got):\n%s", diff)
	}

	next := s.Next(s.Tasks()[0], at("2026-10-16 11:00"))
	if want := time.Date(2026, 10, 17, 8, 0, 0, 0, saoPaulo); !next.Equal(want) {
		t.Errorf("Next = %s, want %s", next, want)
	}

	if s.Tasks()[1].Name() != "job:prune" {
		t.Errorf("job task name = %q", s.Tasks()[1].Name())
	}

	bare := newSchedule(schedule.Options{})
	bare.Command("x").EveryMinute()
	bare.Job(prune{}).EveryMinute()
	bare.Func("panics", func(context.Context) error { panic("oops") })

	if err := bare.RunDue(t.Context(), at("2026-01-01 00:00")); err == nil ||
		!strings.Contains(err.Error(), "no command runner") || !strings.Contains(err.Error(), "no queue") ||
		!strings.Contains(err.Error(), "panic: oops") {
		t.Errorf("unconfigured tasks = %v", err)
	}
}

func TestWorkRunsEveryMinuteWithoutOverlapping(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		var starts, overlapped atomic.Int32

		s := newSchedule(schedule.Options{})
		s.Func("slow", func(context.Context) error {
			starts.Add(1)
			time.Sleep(150 * time.Second) // spans the next two ticks

			return nil
		}).EveryMinute().WithoutOverlapping()
		s.Func("quick", func(context.Context) error { overlapped.Add(1); return nil }).EveryMinute()

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error)

		go func() { done <- s.Work(ctx, nil) }()

		time.Sleep(5*time.Minute + 30*time.Second)
		cancel()

		if err := <-done; err != nil {
			t.Fatal(err)
		}

		// Ticks at minutes 1-5: "slow" starts at 1 and 4 (2, 3 and 5 are skipped
		// while it runs); "quick" runs on every tick.
		if starts.Load() != 2 || overlapped.Load() != 5 {
			t.Fatalf("slow starts = %d, quick runs = %d; want 2 and 5", starts.Load(), overlapped.Load())
		}
	})
}

// On shutdown Work waits for running tasks, but only StopTimeout: then it
// cancels their context so a stuck task cannot hold the process forever.
func TestWorkStopTimeoutCancelsRunningTasks(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		stop     time.Duration
		task     func(ctx context.Context) error
		wantWait time.Duration
		wantErr  bool
	}{
		"blocking task is cancelled after StopTimeout": {
			stop:     10 * time.Second,
			task:     func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
			wantWait: 10 * time.Second,
			wantErr:  true,
		},
		"short task finishes first": {
			stop: 10 * time.Second,
			task: func(ctx context.Context) error {
				select {
				case <-time.After(2 * time.Second):
					return ctx.Err()
				case <-ctx.Done():
					return ctx.Err()
				}
			},
			wantWait: 2 * time.Second,
		},
		"default StopTimeout is 30s": {
			task:     func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() },
			wantWait: 30 * time.Second,
			wantErr:  true,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var taskErr atomic.Value

				s := newSchedule(schedule.Options{StopTimeout: tt.stop})
				s.Func("job", func(ctx context.Context) error {
					err := tt.task(ctx)
					taskErr.Store(errors.Join(err, errors.New("ran")))

					return err
				}).EveryMinute()

				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { done <- s.Work(ctx, nil) }()

				time.Sleep(time.Minute)
				synctest.Wait()
				cancel()

				start := time.Now()
				if err := <-done; err != nil {
					t.Fatalf("Work() = %v", err)
				}

				if waited := time.Since(start); waited != tt.wantWait {
					t.Errorf("Work waited %v, want %v", waited, tt.wantWait)
				}

				err, _ := taskErr.Load().(error)
				if err == nil {
					t.Fatal("task did not finish before Work returned")
				}

				if got := errors.Is(err, context.Canceled); got != tt.wantErr {
					t.Errorf("task err = %v, want cancelled %v", err, tt.wantErr)
				}
			})
		})
	}
}

// sharedStore stands for a store several servers share (database, Redis):
// any store other than the in-process memory cache.
type sharedStore struct{ cache.Store }

// OnOneServer runs each occurrence once across schedulers sharing the lock
// store, and refuses to run on an in-process store, where every server
// would run it.
func TestOnOneServer(t *testing.T) {
	t.Parallel()

	var runs atomic.Int32

	locks := cache.New(sharedStore{cache.NewMemory(0)}, "")
	servers := make([]*schedule.Schedule, 3)

	for i := range servers {
		servers[i] = newSchedule(schedule.Options{Locks: locks})
		servers[i].Func("draw", func(context.Context) error { runs.Add(1); return nil }).EveryMinute().OnOneServer()

		if err := servers[i].Validate(); err != nil {
			t.Fatalf("Validate() on a shared store = %v", err)
		}
	}

	for i, minute := range []string{"2026-10-16 11:00", "2026-10-16 11:01"} {
		for _, s := range servers {
			if err := s.RunDue(t.Context(), at(minute)); err != nil {
				t.Fatal(err)
			}
		}

		if got, want := runs.Load(), int32(i+1); got != want {
			t.Fatalf("after %s: runs = %d, want %d (once per minute)", minute, got, want)
		}
	}

	local := newSchedule(schedule.Options{})
	local.Func("draw", func(context.Context) error { runs.Add(1); return nil }).EveryMinute().OnOneServer()

	if err := local.Validate(); err == nil || !strings.Contains(err.Error(), "OnOneServer") {
		t.Fatalf("Validate() on the memory store = %v, want an OnOneServer error", err)
	}

	if err := local.RunDue(t.Context(), at("2026-10-16 11:02")); err == nil || runs.Load() != 2 {
		t.Fatalf("RunDue() on the memory store = %v (runs %d), want an error and no run", err, runs.Load())
	}
}

// Apps sharing one lock store (one Redis, one cache table) keep their
// locks apart through Options.Namespace: the same task name in two apps
// runs in both, and once per occurrence within each app.
func TestLockNamespace(t *testing.T) {
	t.Parallel()

	locks := cache.New(sharedStore{cache.NewMemory(0)}, "")

	// The shop's prune is still running elsewhere: only the shop skips it.
	held := locks.NewLock("schedule:shop:prune", time.Hour)
	if ok, err := held.TryAcquire(t.Context()); !ok || err != nil {
		t.Fatalf("TryAcquire() = %v, %v", ok, err)
	}

	runs := map[string]*atomic.Int32{}

	for _, app := range []string{"shop", "blog", "shop"} {
		sched := newSchedule(schedule.Options{Locks: locks, Namespace: app})

		for _, task := range []string{"draw", "prune"} {
			if runs[app+" "+task] == nil {
				runs[app+" "+task] = &atomic.Int32{}
			}

			n := runs[app+" "+task]
			fn := sched.Func(task, func(context.Context) error { n.Add(1); return nil }).EveryMinute()

			if task == "draw" {
				fn.OnOneServer()
			} else {
				fn.WithoutOverlapping()
			}
		}

		if err := sched.RunDue(t.Context(), at("2026-10-16 11:00")); err != nil {
			t.Fatal(err)
		}
	}

	got := map[string]int32{}
	for k, v := range runs {
		got[k] = v.Load()
	}

	want := map[string]int32{"shop draw": 1, "blog draw": 1, "shop prune": 0, "blog prune": 1}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("runs (-want +got):\n%s", diff)
	}
}
