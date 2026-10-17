package vukem

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"maps"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/schedule"
)

func TestScheduleCommands(t *testing.T) {
	t.Parallel()

	var (
		out   bytes.Buffer
		calls []string
	)

	clock := func() time.Time { return time.Date(2026, 10, 16, 11, 0, 20, 0, time.UTC) } // 08:00 in São Paulo

	build := func(opts ...Option) (*App, error) {
		app, err := New(append(opts, WithLogger(discardLogger()), WithStdout(&out), WithClock(clock),
			WithSchedule(func(s *schedule.Schedule, _ *App) {
				s.Command("reports:send", "-v").DailyAt("08:00").WithoutOverlapping()
				s.Func("never", func(context.Context) error { return nil }).Cron("0 0 31 2 *")
			}))...)
		if err != nil {
			return nil, err
		}

		app.AddCommand(CommandFunc{
			CommandName: "reports:send",
			DefineFlags: func(fs *flag.FlagSet) { fs.Bool("v", false, "verbose") },
			Fn: func(context.Context, *App, []string) error {
				calls = append(calls, "reports:send")
				return nil
			},
		})

		return app, nil
	}

	run := func(args ...string) (string, error) {
		out.Reset()

		getenv := func(k string) (string, bool) {
			v, ok := map[string]string{"APP_KEY": testKey, "APP_ENV": "test", "SCHEDULE_TIMEZONE": "America/Sao_Paulo"}[k]
			return v, ok
		}

		err := RunMain(t.Context(), append([]string{"app"}, args...), getenv, build)

		return out.String(), err
	}

	if _, err := run("schedule:run"); err != nil || len(calls) != 1 {
		t.Fatalf("schedule:run = %v calls=%v", err, calls)
	}

	listing, err := run("schedule:list")
	if err != nil || !strings.Contains(listing, "reports:send -v") ||
		!strings.Contains(listing, "2026-10-17T08:00:00-03:00") ||
		!strings.Contains(listing, "never") {
		t.Fatalf("schedule:list = %q %v", listing, err)
	}
}

func TestScheduleErrorsAndCall(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	if err := app.Call(t.Context(), "nope", nil); err == nil {
		t.Error("unknown command accepted")
	}

	if err := app.Call(t.Context(), "route:list", []string{"-bogus"}); err == nil {
		t.Error("bad flag accepted")
	}

	if err := scheduleCommands()[2].Run(t.Context(), app, nil); err == nil {
		t.Error("schedule:list without tasks should explain how to add them")
	}

	bad := newTestApp(t, WithSource(testSource(map[string]string{"SCHEDULE_TIMEZONE": "Mars/Olympus"})))
	if _, err := bad.Schedule(); err == nil {
		t.Error("bad timezone accepted")
	}
}

type prunable struct{ cache.Store }

func (prunable) Prune(context.Context) (int64, error) { return 0, nil }

type wrapped struct{ cache.Store }

func (w wrapped) Unwrap() cache.Store { return w.Store }

// selfWrapped unwraps to itself; storePruner must not loop on it.
type selfWrapped struct{ cache.Store }

func (s *selfWrapped) Unwrap() cache.Store { return s }

// A store wrapped for metrics or tracing still gets pruned when it
// exposes the store underneath with Unwrap.
func TestStorePrunerUnwraps(t *testing.T) {
	t.Parallel()

	db := prunable{cache.NewMemory(0)}

	for name, tt := range map[string]struct {
		store cache.Store
		want  bool
	}{
		"database":       {store: db, want: true},
		"wrapped":        {store: wrapped{db}, want: true},
		"wrapped twice":  {store: wrapped{wrapped{db}}, want: true},
		"memory":         {store: cache.NewMemory(0), want: false},
		"wrapped memory": {store: wrapped{cache.NewMemory(0)}, want: false},
		"self reference": {store: &selfWrapped{Store: cache.NewMemory(0)}, want: false},
	} {
		if _, ok := storePruner(tt.store); ok != tt.want {
			t.Errorf("%s: storePruner() = %v, want %v", name, ok, tt.want)
		}
	}
}

// serve runs the schedule in process unless SCHEDULE_IN_SERVE=false, and
// the scheduler stops with the app: synctest fails if a goroutine leaks.
func TestRunStartsSchedulerInServe(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		env  map[string]string
		kind Kind
		want bool
	}{
		"web default":     {kind: KindWeb, want: true},
		"web opted out":   {env: map[string]string{"SCHEDULE_IN_SERVE": "false"}, kind: KindWeb},
		"web opted in":    {env: map[string]string{"SCHEDULE_IN_SERVE": "true"}, kind: KindWeb, want: true},
		"console never":   {kind: KindConsole},
		"test kind never": {kind: KindTest},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			synctest.Test(t, func(t *testing.T) {
				var runs atomic.Int32

				app, err := New(WithSource(testSource(tt.env)), WithLogger(discardLogger()), WithoutHTTP(),
					WithKind(tt.kind),
					WithSchedule(func(s *schedule.Schedule, _ *App) {
						s.Func("tick", func(context.Context) error { runs.Add(1); return nil }).EveryMinute()
					}))
				if err != nil {
					t.Fatal(err)
				}

				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				go func() { done <- app.Run(ctx) }()

				time.Sleep(90 * time.Second)
				synctest.Wait()
				cancel()

				if err := <-done; err != nil {
					t.Fatalf("Run() = %v, want nil on clean shutdown", err)
				}

				if got := runs.Load() > 0; got != tt.want {
					t.Fatalf("task ran = %v (%d runs), want %v", got, runs.Load(), tt.want)
				}
			})
		})
	}
}

// Without tasks the scheduler runner exits at once and serve keeps running.
func TestRunWithoutScheduledTasks(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		app, err := New(WithSource(testSource(nil)), WithLogger(discardLogger()), WithoutHTTP())
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- app.Run(ctx) }()

		synctest.Wait()

		select {
		case err := <-done:
			t.Fatalf("Run() returned early: %v", err)
		default:
		}

		cancel()

		if err := <-done; err != nil {
			t.Fatalf("Run() = %v, want nil", err)
		}
	})
}

// A broken schedule configuration stops serve instead of silently never
// running the tasks.
func TestRunFailsOnBadScheduleConfig(t *testing.T) {
	t.Parallel()

	if _, err := New(WithSource(testSource(map[string]string{"SCHEDULE_IN_SERVE": "maybe"}))); err == nil ||
		!strings.Contains(err.Error(), "SCHEDULE_IN_SERVE") {
		t.Fatalf("New(SCHEDULE_IN_SERVE=maybe) = %v, want an error", err)
	}

	synctest.Test(t, func(t *testing.T) {
		app, err := New(WithSource(testSource(map[string]string{"SCHEDULE_TIMEZONE": "Mars/Olympus"})),
			WithLogger(discardLogger()), WithoutHTTP(),
			WithSchedule(func(s *schedule.Schedule, _ *App) {
				s.Func("tick", func(context.Context) error { return nil }).EveryMinute()
			}))
		if err != nil {
			t.Fatal(err)
		}

		if err := app.Run(t.Context()); err == nil || !strings.Contains(err.Error(), "runner schedule") {
			t.Fatalf("Run() = %v, want a scheduler error", err)
		}
	})
}

// A task still running at shutdown gets SHUTDOWN_TIMEOUT to finish, then its
// context is cancelled and Run returns.
func TestRunBoundsSchedulerShutdown(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		app, err := New(WithSource(testSource(map[string]string{"SHUTDOWN_TIMEOUT": "5s"})),
			WithLogger(discardLogger()), WithoutHTTP(),
			WithSchedule(func(s *schedule.Schedule, _ *App) {
				s.Func("stuck", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }).EveryMinute()
			}))
		if err != nil {
			t.Fatal(err)
		}

		ctx, cancel := context.WithCancel(t.Context())
		done := make(chan error, 1)
		go func() { done <- app.Run(ctx) }()

		time.Sleep(time.Minute)
		synctest.Wait()
		cancel()

		start := time.Now()
		if err := <-done; err != nil {
			t.Fatalf("Run() = %v", err)
		}

		if waited := time.Since(start); waited != 5*time.Second {
			t.Fatalf("Run waited %v after cancel, want SHUTDOWN_TIMEOUT (5s)", waited)
		}
	})
}

// OnOneServer needs the lock store the servers share: with the in-process
// cache the schedule does not build (serve and schedule:work fail at boot).
func TestScheduleOnOneServerNeedsSharedCache(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		env  map[string]string
		opt  Option
		fail bool
	}{
		"memory cache":          {fail: true},
		"database cache":        {env: map[string]string{"CACHE_STORE": "database"}},
		"opaque wrapped memory": {fail: true, opt: WithCacheStore(opaqueStore{cache.NewMemory(0)})},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := map[string]string{"DATABASE_URL": "file:" + filepath.Join(testutil.TempDir(t), "app.db")}
			maps.Copy(env, tt.env)

			opts := []Option{WithSource(testSource(env)), WithSchedule(func(s *schedule.Schedule, _ *App) {
				s.Func("draw", func(context.Context) error { return nil }).Daily().OnOneServer()
			})}
			if tt.opt != nil {
				opts = append(opts, tt.opt)
			}

			app := newTestApp(t, opts...)
			t.Cleanup(func() { _ = app.Shutdown(context.WithoutCancel(t.Context())) })

			_, err := app.Schedule()
			if got := errors.Is(err, schedule.ErrLocksNotShared); got != tt.fail {
				t.Fatalf("Schedule() = %v, want ErrLocksNotShared: %v", err, tt.fail)
			}
		})
	}
}

// sharedCache stands for a store several apps share (Redis, a database).
type sharedCache struct{ cache.Store }

func (sharedCache) Shared() bool { return true }

// Schedule locks are namespaced by APP_NAME, so two apps sharing one
// cache store each run their own task of the same name.
func TestScheduleLocksAreNamespacedByAppName(t *testing.T) {
	t.Parallel()

	store := sharedCache{cache.NewMemory(0)}

	var runs atomic.Int32

	for _, name := range []string{"Shop", "Blog", "Shop"} {
		app := newTestApp(t, WithSource(testSource(map[string]string{"APP_NAME": name})), WithCacheStore(store),
			WithSchedule(func(s *schedule.Schedule, _ *App) {
				s.Func("draw", func(context.Context) error { runs.Add(1); return nil }).EveryMinute().OnOneServer()
			}))

		s, err := app.Schedule()
		if err != nil {
			t.Fatal(err)
		}

		if err := s.RunDue(t.Context(), time.Date(2026, 10, 16, 11, 0, 0, 0, time.UTC)); err != nil {
			t.Fatal(err)
		}
	}

	if got := runs.Load(); got != 2 {
		t.Fatalf("draw runs = %d, want 2 (once for Shop, once for Blog)", got)
	}

	for in, want := range map[string]string{"My Shop!": "my-shop", "Vukem": "vukem", "  ": "app", "café_2": "caf-2"} {
		if got := lockNamespace(in); got != want {
			t.Errorf("lockNamespace(%q) = %q, want %q", in, got, want)
		}
	}
}
