package vukem

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"
	"time"
	_ "time/tzdata" // SCHEDULE_TIMEZONE works on hosts without a zone database (Windows)

	"github.com/AlexGalhardo/vukem/queue"
	"github.com/AlexGalhardo/vukem/schedule"
)

// WithSchedule declares scheduled tasks (generated apps pass
// routes.Schedule).
func WithSchedule(fn func(s *schedule.Schedule, app *App)) Option {
	return func(o *options) error { o.schedules = append(o.schedules, fn); return nil }
}

// ScheduleConfig is read from SCHEDULE_* variables.
type ScheduleConfig struct {
	// Timezone of the expressions (IANA name, default UTC).
	Timezone string `env:"SCHEDULE_TIMEZONE" default:"UTC"`
}

// Schedule returns the application schedule, built on first use. Locks
// for WithoutOverlapping use App.Cache (set CACHE_STORE=database or Redis
// when several servers run the schedule).
func (a *App) Schedule() (*schedule.Schedule, error) {
	a.scheduleOnce.Do(func() {
		cfg, err := configLoad[ScheduleConfig](a)
		if err != nil {
			a.scheduleErr = err
			return
		}

		loc, err := time.LoadLocation(cfg.Timezone)
		if err != nil {
			a.scheduleErr = fmt.Errorf("vukem: SCHEDULE_TIMEZONE: %w", err)
			return
		}

		locks, err := a.Cache()
		if err != nil {
			a.scheduleErr = err
			return
		}

		s := schedule.New(schedule.Options{
			Location: loc,
			Log:      a.Log,
			Locks:    locks,
			Commands: a.Call,
			Queue:    func() (*queue.Queue, error) { return a.Queue() },
		})

		// Database sessions keep expired rows until pruned.
		if sess, err := configLoad[SessionConfig](a); err == nil && sess.Driver == "database" {
			s.Command("session:prune").Hourly().WithoutOverlapping()
		}

		for _, fn := range a.schedules {
			fn(s, a)
		}

		a.schedule = s
	})

	return a.schedule, a.scheduleErr
}

// Call runs the app command name with args inside the current process (the
// app is already booted), e.g. from a scheduled task.
func (a *App) Call(ctx context.Context, name string, args []string) error {
	for _, c := range a.Commands() {
		if c.Name() != name {
			continue
		}

		fs := flag.NewFlagSet(name, flag.ContinueOnError)
		fs.SetOutput(a.stdout)
		c.Flags(fs)

		if err := fs.Parse(args); err != nil {
			return fmt.Errorf("%s: %w", name, err)
		}

		return c.Run(ctx, a, fs.Args())
	}

	return fmt.Errorf("vukem: unknown command %q", name)
}

func scheduleCommands() []Command {
	return []Command{
		CommandFunc{
			CommandName: "schedule:run",
			Desc:        "Run the scheduled tasks due now (call it every minute from cron)",
			Fn: func(ctx context.Context, app *App, _ []string) error {
				s, err := app.Schedule()
				if err != nil {
					return err
				}

				return s.RunDue(ctx, app.now().Truncate(time.Minute))
			},
		},
		CommandFunc{
			CommandName: "schedule:work",
			Desc:        "Run the scheduler in the foreground (every minute, until stopped)",
			Fn: func(ctx context.Context, app *App, _ []string) error {
				s, err := app.Schedule()
				if err != nil {
					return err
				}

				app.Log.InfoContext(ctx, "scheduler started", "tasks", len(s.Tasks()))

				return s.Work(ctx, app.now)
			},
		},
		CommandFunc{
			CommandName: "schedule:list",
			Desc:        "List the scheduled tasks and their next run",
			Fn: func(_ context.Context, app *App, _ []string) error {
				s, err := app.Schedule()
				if err != nil {
					return err
				}

				if len(s.Tasks()) == 0 {
					return errors.New("no scheduled tasks (declare them in routes/schedule.go)")
				}

				tw := tabwriter.NewWriter(app.stdout, 0, 4, 2, ' ', 0)
				fmt.Fprintln(tw, "TASK\tEXPRESSION\tNEXT RUN")

				now := app.now()
				for _, t := range s.Tasks() {
					next := s.Next(t, now)
					fmt.Fprintf(tw, "%s\t%s\t%s\n", t.Name(), t.Expression(),
						strings.Replace(next.Format(time.RFC3339), "0001-01-01T00:00:00Z", "never", 1))
				}

				return tw.Flush()
			},
		},
		CommandFunc{
			CommandName: "session:prune",
			Desc:        "Delete expired database sessions (scheduled hourly with SESSION_DRIVER=database)",
			Fn: func(ctx context.Context, app *App, _ []string) error {
				if _, err := app.Sessions(); err != nil {
					return err
				}

				if app.sessPruner == nil {
					fmt.Fprintln(app.stdout, "Sessions are not stored in the database: nothing to prune.")
					return nil
				}

				n, err := app.sessPruner.Prune(ctx)
				if err != nil {
					return err
				}

				fmt.Fprintf(app.stdout, "Pruned %d expired sessions.\n", n)

				return nil
			},
		},
	}
}
