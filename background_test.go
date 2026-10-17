package vukem

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/internal/testutil"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/schedule"
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

// With QUEUE_CONNECTION=database, Boot prepares the queue tables; an
// unreachable database fails the boot with an error naming the queue.
func TestBootFailsClearlyWhenQueueDatabaseIsUnreachable(t *testing.T) {
	t.Parallel()

	app := newTestApp(t, WithSource(testSource(map[string]string{
		"QUEUE_CONNECTION": "database",
		"DATABASE_URL":     "file:" + filepath.Join(t.TempDir(), "missing", "app.db"),
	})))

	err := app.Boot(t.Context())
	if err == nil || !strings.Contains(err.Error(), "QUEUE_CONNECTION=database") ||
		!strings.Contains(err.Error(), "database unreachable") {
		t.Fatalf("Boot() = %v, want an error naming the database queue", err)
	}
}

type orderShipped struct {
	OrderID int64 `json:"order_id"`
}

type plainMail struct{}

func (plainMail) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{"ana@example.com"}, Subject: "Shipped"}
}
func (plainMail) Content() mail.Content { return mail.Text("Your order is on its way.") }

func TestQueuedListenersMailAndFake(t *testing.T) {
	t.Parallel()

	var notified []int64

	fake := queue.NewFake()
	app := newTestApp(t,
		WithSource(testSource(map[string]string{"MAIL_MAILER": "array"})),
		WithQueueDriver(fake),
		WithJobs(queue.Handle(func(context.Context, pingJob) error { return nil })),
		WithListeners(events.OnQueued("notify_customer", func(_ context.Context, e orderShipped) error {
			notified = append(notified, e.OrderID)
			return nil
		})),
	)

	ctx := t.Context()

	if err := app.Events.Dispatch(ctx, orderShipped{OrderID: 42}); err != nil {
		t.Fatal(err)
	}

	if err := app.MailLater(ctx, plainMail{}); err != nil {
		t.Fatal(err)
	}

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	_ = q.Dispatch(ctx, pingJob{N: 3})

	// Nothing ran yet: the fake only recorded the jobs.
	if len(notified) != 0 || len(fake.Messages()) != 3 {
		t.Fatalf("notified=%v messages=%d", notified, len(fake.Messages()))
	}

	if got := queue.Dispatched[pingJob](fake); len(got) != 1 || got[0].N != 3 {
		t.Fatalf("Dispatched[pingJob] = %v", got)
	}

	if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
		t.Fatal(err)
	}

	m, _ := app.Mailer()
	sent := m.Transport().(*mail.Array).Sent()

	if len(notified) != 1 || notified[0] != 42 || len(sent) != 1 || sent[0].Subject != "Shipped" {
		t.Fatalf("after work: notified=%v sent=%+v", notified, sent)
	}
}

func TestQueuedListenerWithoutQueue(t *testing.T) {
	t.Parallel()

	bus := events.New(discardLogger(), 0)
	bus.Register(events.OnQueued("x", func(context.Context, orderShipped) error { return nil }))

	if err := bus.Dispatch(t.Context(), orderShipped{}); !errors.Is(err, events.ErrNoQueue) {
		t.Fatalf("dispatch without queue = %v", err)
	}
}

type resetMail struct{}

func (resetMail) Envelope() mail.Envelope {
	return mail.Envelope{To: []string{"ana@example.com"}, Subject: "Reset your password"}
}

func (resetMail) Content() mail.Content {
	return mail.Text("https://app.test/reset-password/s3cr3t-t0ken")
}

// Queued mail is stored encrypted: reset links and other tokens in the
// body never sit in the jobs tables (or queue:failed output) in clear text.
func TestSecurity_A04_QueuedMailPayloadIsEncrypted(t *testing.T) {
	t.Parallel()

	fake := queue.NewFake()
	app := newTestApp(t, WithSource(testSource(map[string]string{"MAIL_MAILER": "array"})), WithQueueDriver(fake))
	ctx := t.Context()

	m, err := app.Mailer()
	if err != nil {
		t.Fatal(err)
	}

	if err := m.SendLater(ctx, resetMail{}); err != nil {
		t.Fatal(err)
	}

	msgs := fake.Messages()
	if len(msgs) != 1 || msgs[0].Kind != MailJobKind {
		t.Fatalf("queued = %+v, want one %s job", msgs, MailJobKind)
	}

	for _, secret := range []string{"s3cr3t-t0ken", "ana@example.com", "Reset your password"} {
		if strings.Contains(string(msgs[0].Payload), secret) {
			t.Errorf("job payload holds %q in clear text: %s", secret, msgs[0].Payload)
		}
	}

	q, err := app.Queue()
	if err != nil {
		t.Fatal(err)
	}

	if err := q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true}); err != nil {
		t.Fatal(err)
	}

	sent := m.Transport().(*mail.Array).Sent()
	if len(sent) != 1 || !strings.Contains(sent[0].Text, "s3cr3t-t0ken") {
		t.Fatalf("sent = %+v, want the decrypted reset mail", sent)
	}

	if len(fake.Failed()) != 0 {
		t.Fatalf("failed jobs = %+v", fake.Failed())
	}

	// A tampered payload fails instead of sending.
	tampered := queue.NewFake()
	other := newTestApp(t, WithSource(testSource(map[string]string{"MAIL_MAILER": "array"})), WithQueueDriver(tampered))
	oq, _ := other.Queue()

	if err := oq.DispatchKind(ctx, MailJobKind, map[string]string{"sealed": "v1.bogus"}, queue.Retries(0)); err != nil {
		t.Fatal(err)
	}

	_ = oq.Work(ctx, queue.WorkOptions{StopWhenEmpty: true})

	if len(tampered.Failed()) != 1 {
		t.Errorf("tampered mail job failed %d times, want 1", len(tampered.Failed()))
	}
}

// A clear-text mail job (queued before v0.24.0, or written by someone with
// access to the jobs table) is refused unless QUEUE_ALLOW_LEGACY_MAIL opens
// an upgrade window; each use is logged.
func TestSecurity_A04_PlaintextMailJobRefusedByDefault(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		allow    string
		sent     int
		failed   int
		wantWarn string
	}{
		{"", 0, 1, "clear-text mail job refused"},
		{"true", 1, 0, "clear-text mail job sent"},
	} {
		t.Run("allow="+tt.allow, func(t *testing.T) {
			t.Parallel()

			var logs bytes.Buffer

			fake := queue.NewFake()
			app := newTestApp(t,
				WithSource(testSource(map[string]string{"MAIL_MAILER": "array", "QUEUE_ALLOW_LEGACY_MAIL": tt.allow})),
				WithLogger(slog.New(slog.NewTextHandler(&logs, nil))),
				WithQueueDriver(fake),
			)
			ctx := t.Context()

			q, err := app.Queue()
			if err != nil {
				t.Fatal(err)
			}

			legacy := mail.Message{
				ID:      "legacy-1",
				From:    "a@b.co",
				To:      []string{"ana@example.com"},
				Subject: "Old",
				Text:    "x",
			}
			if err := q.DispatchKind(ctx, MailJobKind, legacy, queue.Retries(0)); err != nil {
				t.Fatal(err)
			}

			_ = q.Work(ctx, queue.WorkOptions{StopWhenEmpty: true})

			m, _ := app.Mailer()
			if sent := m.Transport().(*mail.Array).Sent(); len(sent) != tt.sent {
				t.Errorf("sent %d clear-text mails, want %d", len(sent), tt.sent)
			}

			if n := len(fake.Failed()); n != tt.failed {
				t.Errorf("failed jobs = %d, want %d", n, tt.failed)
			}

			if !strings.Contains(logs.String(), "level=WARN") || !strings.Contains(logs.String(), tt.wantWarn) {
				t.Errorf("log = %q, want a warning %q", logs.String(), tt.wantWarn)
			}
		})
	}
}

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
