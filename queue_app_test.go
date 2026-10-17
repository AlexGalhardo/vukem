package vukem

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/queue"
)

type pingJob struct {
	N int `json:"n"`
}

func (pingJob) Kind() string { return "ping" }

func TestQueueFromConfig(t *testing.T) {
	t.Parallel()

	var got []int

	handle := queue.Handle(func(_ context.Context, j pingJob) error { got = append(got, j.N); return nil })

	app := newTestApp(t, WithJobs(handle))

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	if err := q.Dispatch(t.Context(), pingJob{N: 1}); err != nil || len(got) != 1 {
		t.Fatalf("sync dispatch = %v got=%v", err, got)
	}

	if _, err := newTestApp(
		t,
		WithSource(testSource(map[string]string{"QUEUE_CONNECTION": "kafka"})),
	).Queue(); err == nil {
		t.Error("unknown connection accepted")
	}

	if _, err := newTestApp(t, WithJobs(handle, handle)).Queue(); err == nil {
		t.Error("duplicate job kind accepted")
	}

	if names := (QueueConfig{Queues: "high, default,,"}).QueueNames(); len(names) != 2 || names[0] != "high" {
		t.Errorf("QueueNames = %v", names)
	}
}

func TestMemoryQueueRunner(t *testing.T) {
	t.Parallel()

	synctest.Test(t, func(t *testing.T) {
		done := make(chan int, 1)
		app := newTestApp(t, WithSource(testSource(map[string]string{"QUEUE_CONNECTION": "memory"})),
			WithJobs(queue.Handle(func(_ context.Context, j pingJob) error { done <- j.N; return nil })))

		q, err := app.Queue()
		if err != nil {
			t.Fatal(err)
		}

		_ = q.Dispatch(t.Context(), pingJob{N: 7})

		ctx, cancel := context.WithCancel(t.Context())
		stopped := make(chan error)

		go func() { stopped <- app.queueRunner(QueueConfig{Queues: "default", Concurrency: 1}).Run(ctx) }()

		if n := <-done; n != 7 {
			t.Fatalf("job = %d", n)
		}

		time.Sleep(time.Second)
		cancel()

		if err := <-stopped; err != nil {
			t.Fatal(err)
		}
	})
}

type flakyJob struct {
	Fail bool `json:"fail"`
}

func (flakyJob) Kind() string { return "flaky" }

func TestQueueCommandsOnDatabase(t *testing.T) {
	t.Parallel()

	dbURL := "file:" + filepath.Join(testutil.TempDir(t), "queue.db")

	// Each run is a new app, like separate processes. With the wall clock a
	// job stamped by one run is not yet due in the next one if the clock
	// stepped back in between (time sync), and -stop-when-empty then exits
	// leaving it queued. One fixed app clock makes every queued job due in
	// every later run.
	fixed := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	clock := WithClock(func() time.Time { return fixed })

	var ran atomic.Int32

	jobs := WithJobs(queue.Handle(func(_ context.Context, j flakyJob) error {
		ran.Add(1)
		if j.Fail {
			return errors.New("card declined")
		}

		return nil
	}, queue.Retries(0)))

	run := func(args ...string) (string, error) {
		var out bytes.Buffer

		getenv := func(k string) (string, bool) {
			v, ok := map[string]string{
				"APP_KEY": testKey, "APP_ENV": "test", "DATABASE_URL": dbURL,
				"QUEUE_CONNECTION": "database",
			}[k]
			return v, ok
		}

		build := func(opts ...Option) (*App, error) {
			app, err := New(append(opts, WithLogger(discardLogger()), WithStdout(&out), jobs, clock)...)
			if err != nil {
				return nil, err
			}

			app.AddCommand(
				CommandFunc{CommandName: "test:dispatch", Fn: func(ctx context.Context, a *App, _ []string) error {
					q, err := a.Queue()
					if err != nil {
						return err
					}

					if err := q.Dispatch(ctx, flakyJob{Fail: true}); err != nil {
						return err
					}

					return q.Dispatch(ctx, flakyJob{})
				}},
			)

			return app, nil
		}

		err := RunMain(t.Context(), append([]string{"app"}, args...), getenv, build)

		return out.String(), err
	}

	if _, err := run("test:dispatch"); err != nil {
		t.Fatal(err)
	}

	if _, err := run("queue:work", "-once"); err != nil || ran.Load() != 1 {
		t.Fatalf("work -once = %v ran=%d", err, ran.Load())
	}

	if _, err := run("queue:work", "-stop-when-empty", "-queue", "default", "-concurrency", "2"); err != nil ||
		ran.Load() != 2 {
		t.Fatalf("work = %v ran=%d", err, ran.Load())
	}

	out, err := run("queue:failed")
	if err != nil || !strings.Contains(out, "card declined") || !strings.Contains(out, "flaky") {
		t.Fatalf("queue:failed = %q %v", out, err)
	}

	if out, err := run("queue:failed", "-json"); err != nil || !strings.Contains(out, `"last_error": "card declined"`) {
		t.Fatalf("queue:failed -json = %q %v", out, err)
	}

	if out, err := run("queue:retry", "all"); err != nil || !strings.Contains(out, "1 failed job(s) re-queued") {
		t.Fatalf("queue:retry = %q %v", out, err)
	}

	_, _ = run("queue:work", "-stop-when-empty")

	if out, err := run("queue:forget", "all"); err != nil || !strings.Contains(out, "1 failed job(s) deleted") {
		t.Fatalf("queue:forget = %q %v", out, err)
	}

	if _, err := run("queue:retry"); err == nil {
		t.Error("queue:retry without IDs accepted")
	}
}

func TestFailedCommandsNeedAStore(t *testing.T) {
	t.Parallel()

	app := newTestApp(t)

	if err := queueFailedCommand().Run(t.Context(), app, nil); !errors.Is(err, ErrNoFailedStore) {
		t.Fatalf("queue:failed on sync = %v", err)
	}
}

// WithJobsFunc builds handlers from the app, so they reach its database,
// clock and other services without globals.
func TestWithJobsFuncReachesTheApp(t *testing.T) {
	t.Parallel()

	fixed := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

	var (
		got   time.Time
		built int
	)

	handlers := func(app *App) []queue.Registration {
		built++

		return []queue.Registration{
			queue.Handle(func(context.Context, pingJob) error { got = app.Now(); return nil }),
		}
	}

	app := newTestApp(t, WithClock(func() time.Time { return fixed }), WithJobsFunc(handlers))

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	if _, err := app.Queue(); err != nil {
		t.Fatal(err)
	}

	if err := q.Dispatch(t.Context(), pingJob{N: 1}); err != nil {
		t.Fatal(err)
	}

	if !got.Equal(fixed) || built != 1 {
		t.Fatalf("handler saw now = %v (built %d times), want %v (once)", got, built, fixed)
	}

	dup := newTestApp(t,
		WithJobs(queue.Handle(func(context.Context, pingJob) error { return nil })),
		WithJobsFunc(handlers),
	)
	if _, err := dup.Queue(); !errors.Is(err, queue.ErrDuplicateKind) {
		t.Errorf("Queue() with a kind from WithJobs and WithJobsFunc = %v, want ErrDuplicateKind", err)
	}
}

func TestWithJobsFuncPanicIsAnError(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithJobsFunc(func(*App) []queue.Registration { panic("boom") }))

	for range 2 {
		q, err := app.Queue()
		if q != nil || err == nil || !strings.Contains(err.Error(), "WithJobsFunc panicked: boom") {
			t.Fatalf("Queue() = %v, %v; want nil and the panic as an error", q, err)
		}
	}
}

// dbQueueApp returns an app on a fresh SQLite database with
// QUEUE_CONNECTION=database (plus env), shut down after the test.
func dbQueueApp(t *testing.T, env map[string]string, opts ...Option) *App {
	t.Helper()

	src := map[string]string{
		"DATABASE_URL":     "file:" + filepath.Join(testutil.TempDir(t), "app.db"),
		"QUEUE_CONNECTION": "database",
	}
	maps.Copy(src, env)

	app := newTestApp(t, append([]Option{WithSource(testSource(src))}, opts...)...)
	t.Cleanup(func() { _ = app.Shutdown(context.WithoutCancel(t.Context())) })

	return app
}

// queue.Delay and retries follow the app clock (WithClock), on the
// in-process and the database driver alike.
func TestQueueFollowsAppClock(t *testing.T) {
	t.Parallel()

	for _, conn := range []string{"memory", "database"} {
		t.Run(conn, func(t *testing.T) {
			t.Parallel()

			var (
				mu    sync.Mutex
				now   = time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
				clock = func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
				runs  atomic.Int32
			)

			app := dbQueueApp(t, map[string]string{"QUEUE_CONNECTION": conn}, WithClock(clock),
				WithJobs(queue.Handle(func(context.Context, pingJob) error { runs.Add(1); return nil })))

			q, err := app.Queue()
			if err != nil {
				t.Fatal(err)
			}

			if err := q.Dispatch(t.Context(), pingJob{N: 1}, queue.Delay(10*time.Minute)); err != nil {
				t.Fatal(err)
			}

			for _, step := range []struct {
				advance time.Duration
				want    int32
			}{{9 * time.Minute, 0}, {time.Minute, 1}} {
				mu.Lock()
				now = now.Add(step.advance)
				mu.Unlock()

				if err := q.Work(t.Context(), queue.WorkOptions{StopWhenEmpty: true}); err != nil {
					t.Fatal(err)
				}

				if got := runs.Load(); got != step.want {
					t.Fatalf("after %v: runs = %d, want %d", step.advance, got, step.want)
				}
			}
		})
	}
}

