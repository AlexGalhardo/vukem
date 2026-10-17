package vukem

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/mail"
	"github.com/AlexGalhardo/vukem/queue"
)

// WithJobs registers job handlers on the application queue. Handlers that
// need the app (database, clock, mailer) use WithJobsFunc.
func WithJobs(regs ...queue.Registration) Option {
	return func(o *options) error { o.jobs = append(o.jobs, regs...); return nil }
}

// WithJobsFunc registers job handlers built from the app, so they reach
// its database, clock, mailer and other services without globals
// (generated apps pass jobs.All). fn runs once, inside the first
// app.Queue call; a panic in fn becomes that call's error. fn may get
// app.DB, app.Mailer, app.Crypt and app.Now; it must not dispatch: no
// app.Queue, app.Events.Dispatch (queued listeners), Mailer.SendLater /
// app.MailLater or running scheduled jobs, which wait for the queue being
// built and deadlock. Handlers may do all of that when they run.
func WithJobsFunc(fn func(app *App) []queue.Registration) Option {
	return func(o *options) error { o.jobFuncs = append(o.jobFuncs, fn); return nil }
}

// WithQueueDriver replaces the driver chosen by QUEUE_CONNECTION (tests
// pass a queue.Fake). The last of WithQueueDriver and WithQueueDriverFunc
// wins.
func WithQueueDriver(d queue.Driver) Option {
	return func(o *options) error { o.queueDriver, o.queueFunc = d, nil; return nil }
}

// WithQueueDriverFunc replaces the driver chosen by QUEUE_CONNECTION with
// one built by fn on the first app.Queue() call, after configuration
// loaded, so fn can read app.Source. An error from fn, or a nil driver,
// is returned by app.Queue(). fn must not call app.Queue() (or dispatch a
// job): it runs inside Queue's sync.Once and that call would deadlock.
func WithQueueDriverFunc(fn func(app *App) (queue.Driver, error)) Option {
	return func(o *options) error { o.queueFunc, o.queueDriver = fn, nil; return nil }
}

// MailJobKind is the queue job that sends queued mail (App.MailLater).
const MailJobKind = "vukem.mail"

// QueueConfig is read from QUEUE_* variables.
type QueueConfig struct {
	// Connection is "sync" (default: jobs run during Dispatch), "memory"
	// (in process, run by a worker inside serve) or "database".
	Connection string `env:"QUEUE_CONNECTION" default:"sync"`
	// Queues lists the queues workers poll, in priority order.
	Queues string `env:"QUEUE_NAMES" default:"default"`
	// Concurrency is the number of parallel workers.
	Concurrency int `env:"QUEUE_CONCURRENCY" default:"4"`
	// Table and FailedTable are the database driver's tables.
	Table       string `env:"QUEUE_TABLE"        default:"jobs"`
	FailedTable string `env:"QUEUE_FAILED_TABLE" default:"failed_jobs"`
	// InServe runs a queue worker inside serve for the database driver
	// and custom drivers (Redis). Set it to false when dedicated
	// queue:work processes run the jobs instead; leaving it on next to
	// them is safe (reservations are atomic), it only adds workers. The
	// memory queue is always worked inside serve: no other process sees
	// its jobs.
	InServe bool `env:"QUEUE_IN_SERVE" default:"true"`
	// AllowLegacyMail sends clear-text mail jobs queued before v0.24.0
	// (an upgrade window: drain the queue before upgrading, or set it
	// until the old jobs are gone). Off, they fail.
	AllowLegacyMail bool `env:"QUEUE_ALLOW_LEGACY_MAIL"`
}

// QueueNames returns Queues as a list.
func (c QueueConfig) QueueNames() []string {
	var out []string

	for name := range strings.SplitSeq(c.Queues, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}

	return out
}

// Queue returns the application queue, built from QUEUE_* on first use.
func (a *App) Queue() (*queue.Queue, error) {
	a.queueOnce.Do(func() {
		cfg, err := configLoad[QueueConfig](a)
		if err != nil {
			a.queueErr = err
			return
		}

		d := a.queueDriver

		switch {
		case d != nil:
		case a.queueDriverFunc != nil:
			d, err = a.queueDriverFunc(a)
			if err == nil && d == nil {
				err = errors.New("WithQueueDriverFunc returned a nil driver")
			}

			if err != nil {
				a.queueErr = fmt.Errorf("vukem: queue driver: %w", err)
				return
			}
		case cfg.Connection == "sync":
			d = queue.Sync{}
		case cfg.Connection == "memory":
			d = queue.NewMemory()
		case cfg.Connection == "database":
			if d, err = a.databaseQueueDriver(cfg); err != nil {
				a.queueErr = err
				return
			}
		default:
			a.queueErr = fmt.Errorf("vukem: unknown QUEUE_CONNECTION %q (sync, memory, database)", cfg.Connection)
			return
		}

		// Delays and retries follow the app clock (WithClock, vukemtest fake
		// clocks) on every driver that reads one, custom drivers included.
		if c, ok := d.(queue.Clock); ok {
			c.SetClock(a.now)
		}

		q := queue.New(d, a.Log)
		q.SetClock(a.now)

		if a.Devtools != nil {
			q.SetObserver(a.Devtools)
		}

		regs, err := a.jobRegistrations()
		if err != nil {
			a.queueErr = err
			return
		}

		regs = append(regs, a.Events.Jobs()...)
		regs = append(regs, queue.HandleKind(MailJobKind, a.sendQueuedMail))

		if err := q.Register(regs...); err != nil {
			a.queueErr = err
			return
		}

		a.queue = q
	})

	return a.queue, a.queueErr
}

// jobRegistrations runs the WithJobsFunc functions; a panic in one becomes
// the Queue error instead of leaving a nil queue behind the sync.Once.
func (a *App) jobRegistrations() (regs []queue.Registration, err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("vukem: WithJobsFunc panicked: %v", p)
		}
	}()

	regs = slices.Clone(a.jobs)
	for _, fn := range a.jobFuncs {
		regs = append(regs, fn(a)...)
	}

	return regs, nil
}

// prepareDatabaseQueue creates the database queue tables at Boot, so the
// first app.Queue() never runs DDL: called inside a transaction on SQLite,
// whose single writer connection the transaction holds, it would block
// until the timeout and fail.
func (a *App) prepareDatabaseQueue(ctx context.Context) error {
	if a.queueDriver != nil || a.queueDriverFunc != nil {
		return nil
	}

	cfg, err := configLoad[QueueConfig](a)
	if err != nil || cfg.Connection != "database" {
		return err
	}

	d, err := a.databaseQueue(ctx, cfg)
	if err != nil {
		return fmt.Errorf("vukem: queue: %w", err)
	}

	a.mu.Lock()
	a.queueDB = d
	a.mu.Unlock()

	return nil
}

// databaseQueueDriver returns the driver Boot prepared, or builds it when
// the app was not booted.
func (a *App) databaseQueueDriver(cfg QueueConfig) (*queue.Database, error) {
	a.mu.Lock()
	d := a.queueDB
	a.mu.Unlock()

	if d != nil {
		return d, nil
	}

	return a.databaseQueue(context.Background(), cfg)
}

func (a *App) databaseQueue(ctx context.Context, cfg QueueConfig) (*queue.Database, error) {
	db, err := a.DB()
	if err != nil {
		return nil, err
	}

	d, err := queue.NewDatabase(db, queue.DatabaseOptions{Table: cfg.Table, FailedTable: cfg.FailedTable})
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	if err := d.CreateTables(ctx); err != nil {
		return nil, err
	}

	return d, nil
}

// queuedMail is the payload of a MailJobKind job: the rendered message,
// encrypted with app.Crypt so bodies (reset links, tokens) and addresses
// never sit in the jobs tables, queue:failed output or logs in clear text.
// Message is the clear-text payload of jobs queued before v0.24.0, refused
// unless QUEUE_ALLOW_LEGACY_MAIL=true. The additional data is fixed, so
// someone with write access to the jobs table can replay a sealed mail;
// that access already allows far worse.
type queuedMail struct {
	Sealed string `json:"sealed,omitempty"`

	mail.Message
}

// ErrLegacyMail fails a clear-text mail job when QUEUE_ALLOW_LEGACY_MAIL
// is off.
var ErrLegacyMail = errors.New("vukem: clear-text mail job refused (QUEUE_ALLOW_LEGACY_MAIL is off)")

// mailJobAD binds sealed mail payloads to the mail job.
const mailJobAD = "queue:" + MailJobKind

func (a *App) enqueueMail(ctx context.Context, msg *mail.Message, opts ...queue.Option) error {
	q, payload, err := a.mailJob(msg)
	if err != nil {
		return err
	}

	return q.DispatchKind(ctx, MailJobKind, payload, opts...)
}

func (a *App) enqueueMailTx(ctx context.Context, ex database.Executor, msg *mail.Message, opts ...queue.Option) error {
	q, payload, err := a.mailJob(msg)
	if err != nil {
		return err
	}

	return q.DispatchKindTx(ctx, ex, MailJobKind, payload, opts...)
}

// mailJob seals msg into a MailJobKind payload.
func (a *App) mailJob(msg *mail.Message) (*queue.Queue, map[string]string, error) {
	plain, err := json.Marshal(msg) //nolint:musttag // Go field names, as mail jobs always used
	if err != nil {
		return nil, nil, fmt.Errorf("vukem: encode queued mail: %w", err)
	}

	q, err := a.Queue()
	if err != nil {
		return nil, nil, err
	}

	return q, map[string]string{"sealed": a.Crypt.Seal(plain, []byte(mailJobAD))}, nil
}

func (a *App) sendQueuedMail(ctx context.Context, p queuedMail) error {
	m, err := a.Mailer()
	if err != nil {
		return err
	}

	msg := p.Message
	if p.Sealed == "" {
		cfg, err := configLoad[QueueConfig](a)
		if err != nil {
			return err
		}

		if !cfg.AllowLegacyMail {
			a.Log.WarnContext(
				ctx,
				"clear-text mail job refused (set QUEUE_ALLOW_LEGACY_MAIL=true to drain jobs queued before v0.24.0)",
			)
			return ErrLegacyMail
		}

		a.Log.WarnContext(
			ctx,
			"clear-text mail job sent (QUEUE_ALLOW_LEGACY_MAIL=true; unset it once old jobs are drained)",
		)
	} else {
		plain, err := a.Crypt.Open(p.Sealed, []byte(mailJobAD))
		if err != nil {
			return fmt.Errorf("vukem: open queued mail: %w", err)
		}

		msg = mail.Message{}
		if err := json.Unmarshal(plain, &msg); err != nil { //nolint:musttag // see mailJob
			return fmt.Errorf("vukem: decode queued mail: %w", err)
		}
	}

	return m.Transport().Send(ctx, &msg)
}

// MailLater renders mailable now and sends it from a queue job: see
// mail.Mailer.SendLater (the app's mailer encrypts the job payload).
func (a *App) MailLater(ctx context.Context, mailable mail.Mailable, opts ...queue.Option) error {
	m, err := a.Mailer()
	if err != nil {
		return err
	}

	return m.SendLater(ctx, mailable, opts...)
}

// MailLaterTx is MailLater inside the transaction of ex: the mail is
// queued only if the transaction commits (see mail.Mailer.SendLaterTx and
// queue.Queue.DispatchTx).
func (a *App) MailLaterTx(ctx context.Context, ex database.Executor, mailable mail.Mailable,
	opts ...queue.Option,
) error {
	m, err := a.Mailer()
	if err != nil {
		return err
	}

	return m.SendLaterTx(ctx, ex, mailable, opts...)
}

// inServe reports whether serve runs a queue worker: always for memory,
// otherwise unless QUEUE_IN_SERVE=false, never for sync (jobs run inside
// Dispatch). custom is a driver set with WithQueueDriver(Func).
func (c QueueConfig) inServe(custom bool) bool {
	switch {
	case custom:
		return c.InServe
	case c.Connection == "memory":
		return true
	case c.Connection == "sync":
		return false
	default:
		return c.InServe
	}
}

// queueRunner works the queue inside the web process, so a
// single-process deploy still runs queued mail and jobs. It exits at once
// when the driver runs jobs inline (a custom sync driver). On shutdown,
// running jobs get SHUTDOWN_TIMEOUT before their context is cancelled.
func (a *App) queueRunner(cfg QueueConfig) Runner {
	return RunnerFunc(func(ctx context.Context) error {
		q, err := a.Queue()
		if err != nil {
			return err
		}

		if in, ok := q.Driver().(queue.Inline); ok && in.Inline() {
			return nil
		}

		if cfg.Connection != "memory" {
			a.Log.InfoContext(ctx,
				"queue worker running inside serve; set QUEUE_IN_SERVE=false if only queue:work should run jobs",
				"queues", cfg.QueueNames(), "concurrency", cfg.Concurrency)
		}

		return q.Work(ctx, queue.WorkOptions{
			Queues:      cfg.QueueNames(),
			Concurrency: cfg.Concurrency,
			StopTimeout: a.Config.ShutdownTimeout,
		})
	})
}

func queueCommands() []Command {
	return []Command{
		queueWorkCommand(),
		queueFailedCommand(),
		queueRetryCommand("queue:retry"),
		queueRetryCommand("queue:forget"),
	}
}

func queueWorkCommand() Command {
	var (
		queues      *string
		concurrency *int
		once, empty *bool
		sleep       *time.Duration
	)

	return CommandFunc{
		CommandName: "queue:work",
		Desc:        "Process queued jobs until stopped (Ctrl+C drains running jobs within SHUTDOWN_TIMEOUT)",
		DefineFlags: func(fs *flag.FlagSet) {
			queues = fs.String("queue", "", "queues in priority order (default QUEUE_NAMES)")
			concurrency = fs.Int("concurrency", 0, "parallel workers (default QUEUE_CONCURRENCY)")
			once = fs.Bool("once", false, "process a single job")
			empty = fs.Bool("stop-when-empty", false, "exit when no job is available")
			sleep = fs.Duration("sleep", time.Second, "wait when the queue is empty")
		},
		Fn: func(ctx context.Context, app *App, _ []string) error {
			cfg, err := configLoad[QueueConfig](app)
			if err != nil {
				return err
			}

			if *queues != "" {
				cfg.Queues = *queues
			}

			if *concurrency > 0 {
				cfg.Concurrency = *concurrency
			}

			q, err := app.Queue()
			if err != nil {
				return err
			}

			o := queue.WorkOptions{
				Queues:        cfg.QueueNames(),
				Concurrency:   cfg.Concurrency,
				Sleep:         *sleep,
				StopWhenEmpty: *empty,
				StopTimeout:   app.Config.ShutdownTimeout,
			}
			if *once {
				o.MaxJobs, o.Concurrency, o.StopWhenEmpty = 1, 1, true
			}

			app.Log.InfoContext(ctx, "queue worker started", "queues", o.Queues, "concurrency", o.Concurrency)

			return q.Work(ctx, o)
		},
	}
}

// ErrNoFailedStore is returned by the failed-job commands for drivers that
// do not keep failed jobs.
var ErrNoFailedStore = errors.New("vukem: the queue driver does not keep failed jobs (use QUEUE_CONNECTION=database)")

func failedStore(app *App) (queue.FailedStore, error) {
	q, err := app.Queue()
	if err != nil {
		return nil, err
	}

	fs, ok := q.Driver().(queue.FailedStore)
	if !ok {
		return nil, ErrNoFailedStore
	}

	return fs, nil
}

func queueFailedCommand() Command {
	var asJSON *bool

	return CommandFunc{
		CommandName: "queue:failed",
		Desc:        "List failed jobs",
		DefineFlags: func(fs *flag.FlagSet) { asJSON = fs.Bool("json", false, "print JSON") },
		Fn: func(ctx context.Context, app *App, _ []string) error {
			fs, err := failedStore(app)
			if err != nil {
				return err
			}

			jobs, err := fs.Failed(ctx)
			if err != nil {
				return err
			}

			if *asJSON {
				enc := json.NewEncoder(app.stdout)
				enc.SetIndent("", "  ")

				return enc.Encode(jobs)
			}

			tw := tabwriter.NewWriter(app.stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "ID\tQUEUE\tKIND\tATTEMPTS\tFAILED AT\tERROR")

			for i := range jobs {
				j := &jobs[i]
				msg, _, _ := strings.Cut(j.LastError, "\n")
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", j.ID, j.Queue, j.Kind, j.Attempts,
					j.FailedAt.Format(time.RFC3339), msg)
			}

			return tw.Flush()
		},
	}
}

func queueRetryCommand(name string) Command {
	desc := "Re-queue failed jobs: IDs or all"
	if name == "queue:forget" {
		desc = "Delete failed jobs: IDs or all"
	}

	return CommandFunc{
		CommandName: name,
		Desc:        desc,
		Fn: func(ctx context.Context, app *App, args []string) error {
			if len(args) == 0 {
				return fmt.Errorf("%s needs job IDs or \"all\"", name)
			}

			if len(args) == 1 && args[0] == "all" {
				args = nil
			}

			fs, err := failedStore(app)
			if err != nil {
				return err
			}

			act, verb := fs.Retry, "re-queued"
			if name == "queue:forget" {
				act, verb = fs.Forget, "deleted"
			}

			n, err := act(ctx, args...)
			if err != nil {
				return err
			}

			fmt.Fprintf(app.stdout, "%d failed job(s) %s\n", n, verb)

			return nil
		},
	}
}
