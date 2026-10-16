package vukem

import (
	"bytes"
	"context"
	"flag"
	"strings"
	"testing"
	"time"

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
