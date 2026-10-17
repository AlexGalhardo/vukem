// Package schedule runs recurring tasks: functions, app commands and
// queue jobs, on cron expressions or helpers such as DailyAt.
//
//	s.Command("reports:send").DailyAt("08:00").WithoutOverlapping()
//	s.Job(jobs.PruneSessions{}).Hourly()
//	s.Func("ping", func(ctx context.Context) error { ... }).EveryFiveMinutes()
//
// Run it in process with Work (vukem schedule:work) or from one cron
// entry calling RunDue every minute (vukem schedule:run). [Schedule]
// holds the [Task] list, [Cron] parses five-field expressions, and
// WithoutOverlapping takes a lock in the Options.Locks cache.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#scheduler.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/queue"
)

// Options configure a Schedule.
type Options struct {
	// Location is the default time zone of expressions (default UTC).
	Location *time.Location
	// Log receives task outcomes (default slog.Default()).
	Log *slog.Logger
	// Locks backs WithoutOverlapping (default an in-memory store, which
	// only protects one process; use the database or Redis cache store
	// when several servers run the schedule).
	Locks *cache.Cache
	// Commands runs app commands for Command tasks.
	Commands func(ctx context.Context, name string, args []string) error
	// Queue dispatches Job tasks.
	Queue func() (*queue.Queue, error)
}

// Schedule holds the tasks.
type Schedule struct {
	o     Options
	tasks []*Task
}

// New returns an empty schedule.
func New(o Options) *Schedule {
	if o.Location == nil {
		o.Location = time.UTC
	}

	if o.Log == nil {
		o.Log = slog.Default()
	}

	if o.Locks == nil {
		o.Locks = cache.New(cache.NewMemory(1000), "")
	}

	return &Schedule{o: o}
}

// Task is a scheduled unit of work; configure it with the chained methods.
type Task struct {
	name    string
	run     func(ctx context.Context) error
	cron    Cron
	loc     *time.Location
	overlap bool
	lockTTL time.Duration
}

// Func schedules fn under name.
func (s *Schedule) Func(name string, fn func(ctx context.Context) error) *Task {
	t := &Task{name: name, run: fn, cron: MustParseCron("* * * * *")}
	s.tasks = append(s.tasks, t)

	return t
}

// Command schedules an app command with args.
func (s *Schedule) Command(name string, args ...string) *Task {
	label := strings.TrimSpace(name + " " + strings.Join(args, " "))

	return s.Func(label, func(ctx context.Context) error {
		if s.o.Commands == nil {
			return errors.New("schedule: no command runner configured")
		}

		return s.o.Commands(ctx, name, args)
	})
}

// Job schedules dispatching j onto the queue.
func (s *Schedule) Job(j queue.Job, opts ...queue.Option) *Task {
	return s.Func("job:"+j.Kind(), func(ctx context.Context) error {
		if s.o.Queue == nil {
			return errors.New("schedule: no queue configured")
		}

		q, err := s.o.Queue()
		if err != nil {
			return err
		}

		return q.Dispatch(ctx, j, opts...)
	})
}

// Tasks returns the tasks in registration order.
func (s *Schedule) Tasks() []*Task { return s.tasks }

// Name returns the task's label.
func (t *Task) Name() string { return t.name }

// Expression returns the cron expression.
func (t *Task) Expression() string { return t.cron.String() }

// Cron sets a cron expression; it panics on a bad expression, so mistakes
// surface at boot.
func (t *Task) Cron(expr string) *Task {
	t.cron = MustParseCron(expr)
	return t
}

// Every runs the task every d, aligned to the clock (d must be a whole
// number of minutes dividing an hour or a day, e.g. 5m, 15m, 2h).
func (t *Task) Every(d time.Duration) *Task {
	m := int(d / time.Minute)

	switch {
	case d%time.Minute != 0 || m < 1:
		panic(fmt.Sprintf("schedule: Every(%s) needs whole minutes", d))
	case m < 60 && 60%m == 0:
		return t.Cron("*/" + strconv.Itoa(m) + " * * * *")
	case m%60 == 0 && 24%(m/60) == 0:
		return t.Cron("0 */" + strconv.Itoa(m/60) + " * * *")
	default:
		panic(fmt.Sprintf("schedule: Every(%s) must divide an hour or a day; use Cron", d))
	}
}

// EveryMinute runs the task every minute.
func (t *Task) EveryMinute() *Task { return t.Cron("* * * * *") }

// EveryFiveMinutes runs the task every five minutes.
func (t *Task) EveryFiveMinutes() *Task { return t.Cron("*/5 * * * *") }

// Hourly runs the task at the start of every hour.
func (t *Task) Hourly() *Task { return t.Cron("0 * * * *") }

// HourlyAt runs the task every hour at minute.
func (t *Task) HourlyAt(minute int) *Task { return t.Cron(strconv.Itoa(minute) + " * * * *") }

// Daily runs the task at midnight.
func (t *Task) Daily() *Task { return t.Cron("0 0 * * *") }

// DailyAt runs the task every day at "HH:MM".
func (t *Task) DailyAt(hhmm string) *Task {
	h, m := clock(hhmm)
	return t.Cron(m + " " + h + " * * *")
}

// Weekdays restricts the task to Monday-Friday (keeps its time).
func (t *Task) Weekdays() *Task {
	f := strings.Fields(t.cronFields())
	return t.Cron(f[0] + " " + f[1] + " " + f[2] + " " + f[3] + " 1-5")
}

// WeeklyOn runs the task on weekday (0 = Sunday) at "HH:MM".
func (t *Task) WeeklyOn(weekday time.Weekday, hhmm string) *Task {
	h, m := clock(hhmm)
	return t.Cron(m + " " + h + " * * " + strconv.Itoa(int(weekday)))
}

// MonthlyOn runs the task on day of the month at "HH:MM".
func (t *Task) MonthlyOn(day int, hhmm string) *Task {
	h, m := clock(hhmm)
	return t.Cron(m + " " + h + " " + strconv.Itoa(day) + " * *")
}

// In sets the time zone of the task's expression.
func (t *Task) In(loc *time.Location) *Task {
	t.loc = loc
	return t
}

// WithoutOverlapping skips a run while the previous one still holds the
// lock (released when it ends, or after ttl; default 24h).
func (t *Task) WithoutOverlapping(ttl ...time.Duration) *Task {
	t.overlap, t.lockTTL = true, 24*time.Hour
	if len(ttl) > 0 {
		t.lockTTL = ttl[0]
	}

	return t
}

func (t *Task) cronFields() string {
	if m, ok := macros[strings.ToLower(t.cron.expr)]; ok {
		return m
	}

	return t.cron.expr
}

func clock(hhmm string) (h, m string) {
	hs, ms, ok := strings.Cut(hhmm, ":")
	hv, err1 := strconv.Atoi(hs)
	mv, err2 := strconv.Atoi(ms)

	if !ok || err1 != nil || err2 != nil || hv < 0 || hv > 23 || mv < 0 || mv > 59 {
		panic(fmt.Sprintf("schedule: bad time %q, want HH:MM", hhmm))
	}

	return strconv.Itoa(hv), strconv.Itoa(mv)
}

func (s *Schedule) loc(t *Task) *time.Location {
	if t.loc != nil {
		return t.loc
	}

	return s.o.Location
}

// Due returns the tasks due at now (to the minute).
func (s *Schedule) Due(now time.Time) []*Task {
	var out []*Task

	for _, t := range s.tasks {
		if t.cron.Matches(now.In(s.loc(t))) {
			out = append(out, t)
		}
	}

	return out
}

// Next returns when t runs next after now.
func (s *Schedule) Next(t *Task, now time.Time) time.Time { return t.cron.Next(now.In(s.loc(t))) }

// RunDue runs the tasks due at now concurrently and waits for them. Task
// errors are logged and joined into the result.
func (s *Schedule) RunDue(ctx context.Context, now time.Time) error {
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for _, t := range s.Due(now) {
		wg.Go(func() {
			if err := s.runTask(ctx, t, now); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", t.name, err))
				mu.Unlock()
			}
		})
	}

	wg.Wait()

	return errors.Join(errs...)
}

func (s *Schedule) runTask(ctx context.Context, t *Task, now time.Time) (err error) {
	log := s.o.Log.With("task", t.name)

	if t.overlap {
		lock := s.o.Locks.NewLock("schedule:"+t.name, t.lockTTL)

		ok, err := lock.TryAcquire(ctx)
		if err != nil {
			return fmt.Errorf("lock: %w", err)
		}

		if !ok {
			log.InfoContext(ctx, "scheduled task skipped: still running")
			return nil
		}

		defer func() { _ = lock.Release(context.WithoutCancel(ctx)) }()
	}

	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v", p)
		}

		if err != nil {
			log.ErrorContext(ctx, "scheduled task failed", "err", err)
		} else {
			log.InfoContext(ctx, "scheduled task done", "took", time.Since(now).Round(time.Millisecond))
		}
	}()

	return t.run(ctx)
}

// Work runs due tasks at the start of every minute until ctx is cancelled,
// then waits for running tasks. now is the clock (time.Now if nil).
func (s *Schedule) Work(ctx context.Context, now func() time.Time) error {
	if now == nil {
		now = time.Now
	}

	var wg sync.WaitGroup

	defer wg.Wait()

	for {
		next := now().Truncate(time.Minute).Add(time.Minute)
		timer := time.NewTimer(next.Sub(now()))

		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}

		wg.Go(func() { _ = s.RunDue(context.WithoutCancel(ctx), next) })
	}
}
