package vukem

import (
	"bytes"
	"context"
	"errors"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/mail"
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

// serve works every persistent queue unless QUEUE_IN_SERVE=false, always
// works the memory queue (no other process sees it) and never sync.
func TestServeQueueWorkerSelection(t *testing.T) {
	t.Parallel()

	for name, tt := range map[string]struct {
		env    map[string]string
		kind   Kind
		custom bool
		want   bool
	}{
		"sync":                    {},
		"sync opted in":           {env: map[string]string{"QUEUE_IN_SERVE": "true"}},
		"memory":                  {env: map[string]string{"QUEUE_CONNECTION": "memory"}, want: true},
		"memory ignores opt-out":  {env: map[string]string{"QUEUE_CONNECTION": "memory", "QUEUE_IN_SERVE": "false"}, want: true},
		"database":                {env: map[string]string{"QUEUE_CONNECTION": "database"}, want: true},
		"database opted out":      {env: map[string]string{"QUEUE_CONNECTION": "database", "QUEUE_IN_SERVE": "false"}},
		"database console":        {env: map[string]string{"QUEUE_CONNECTION": "database"}, kind: KindConsole},
		"custom driver (redis)":   {custom: true, want: true},
		"custom driver opted out": {env: map[string]string{"QUEUE_IN_SERVE": "false"}, custom: true},
		"memory console never":    {env: map[string]string{"QUEUE_CONNECTION": "memory"}, kind: KindConsole},
		"custom driver test kind": {custom: true, kind: KindTest},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			kind := tt.kind
			if kind == "" {
				kind = KindWeb
			}

			opts := []Option{WithSource(testSource(tt.env)), WithKind(kind)}
			if tt.custom {
				opts = append(opts, WithQueueDriver(queue.NewMemory()))
			}

			app := newTestApp(t, opts...)

			got := slices.ContainsFunc(app.runners, func(r namedRunner) bool { return r.name == "queue" })
			if got != tt.want {
				t.Fatalf("queue runner in serve = %v, want %v", got, tt.want)
			}
		})
	}

	if _, err := New(WithSource(testSource(map[string]string{"QUEUE_IN_SERVE": "maybe"}))); err == nil ||
		!strings.Contains(err.Error(), "QUEUE_IN_SERVE") {
		t.Fatalf("New(QUEUE_IN_SERVE=maybe) = %v, want an error", err)
	}
}

// A single serve process runs jobs queued in the database (queued mail on
// a one-service deploy) and stops its worker on shutdown.
func TestRunWorksDatabaseQueueInServe(t *testing.T) {
	t.Parallel()

	done := make(chan int, 1)
	app := dbQueueApp(t, nil, WithoutHTTP(),
		WithJobs(queue.Handle(func(_ context.Context, j pingJob) error { done <- j.N; return nil })))

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	if err := q.Dispatch(t.Context(), pingJob{N: 9}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	stopped := make(chan error, 1)

	go func() { stopped <- app.Run(ctx) }()

	if n := <-done; n != 9 {
		t.Fatalf("job = %d, want 9", n)
	}

	cancel()

	if err := <-stopped; err != nil {
		t.Fatalf("Run() = %v, want nil", err)
	}
}

// A custom driver that runs jobs inline needs no worker: the runner exits
// and serve keeps running.
func TestQueueRunnerExitsForInlineDriver(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithQueueDriver(queue.Sync{}))

	if err := app.queueRunner(QueueConfig{}).Run(t.Context()); err != nil {
		t.Fatalf("queueRunner(sync).Run() = %v, want nil", err)
	}
}

// MailLaterTx queues the mail only if the transaction commits.
func TestMailLaterTx(t *testing.T) {
	t.Parallel()

	rollback := errors.New("rollback")

	for _, conn := range []string{"database", "sync"} {
		t.Run(conn, func(t *testing.T) {
			t.Parallel()

			app := dbQueueApp(t, map[string]string{"QUEUE_CONNECTION": conn, "MAIL_MAILER": "array"})
			ctx := t.Context()

			db, err := app.DB()
			if err != nil {
				t.Fatal(err)
			}

			if err := app.Boot(ctx); err != nil {
				t.Fatal(err)
			}

			for _, fail := range []bool{true, false} {
				err := db.Tx(ctx, func(tx *database.Tx) error {
					if err := app.MailLaterTx(ctx, tx, plainMail{}); err != nil {
						return err
					}

					if fail {
						return rollback
					}

					return nil
				})
				if fail != errors.Is(err, rollback) || !fail && err != nil {
					t.Fatalf("Tx(fail=%v) = %v", fail, err)
				}
			}

			q, err := app.Queue()
			if err != nil {
				t.Fatal(err)
			}

			if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
				t.Fatal(err)
			}

			m, _ := app.Mailer()
			if sent := m.Transport().(*mail.Array).Sent(); len(sent) != 1 || sent[0].Subject != "Shipped" {
				t.Fatalf("sent = %+v, want only the committed mail", sent)
			}
		})
	}
}

// Boot creates the database queue tables, so the first app.Queue() can
// happen inside a transaction: on SQLite that transaction holds the only
// writer connection, and creating the tables there would block and fail.
func TestDatabaseQueueFirstUseInsideTransaction(t *testing.T) {
	t.Parallel()

	app := dbQueueApp(t, nil, WithJobs(queue.Handle(func(context.Context, pingJob) error { return nil })))
	ctx := t.Context()

	if err := app.Boot(ctx); err != nil {
		t.Fatal(err)
	}

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	err = db.Tx(ctx, func(tx *database.Tx) error {
		q, err := app.Queue()
		if err != nil {
			return err
		}

		return q.DispatchTx(ctx, tx, pingJob{N: 1})
	})
	if err != nil {
		t.Fatalf("DispatchTx as the first queue use = %v, want nil", err)
	}

	q, _ := app.Queue()
	if n, err := q.Driver().(*queue.Database).Pending(ctx); err != nil || n != 1 {
		t.Fatalf("Pending() = %d, %v; want the committed job", n, err)
	}
}
