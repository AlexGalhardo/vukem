package schedule_test

import (
	"context"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/schedule"
)

func TestEveryDay(t *testing.T) {
	t.Parallel()

	s := newSchedule(schedule.Options{})
	got := s.Func("daily", func(context.Context) error { return nil }).Every(24 * time.Hour).Expression()

	if got != "0 0 * * *" {
		t.Errorf("Every(24h).Expression() = %q, want %q", got, "0 0 * * *")
	}
}

func TestCronSteppedWildcardDaysUseAnd(t *testing.T) {
	t.Parallel()

	c := schedule.MustParseCron("0 0 */2 * 1") // odd days that are Mondays, as Vixie cron
	tests := []struct {
		name string
		day  int
		want bool
	}{
		{"odd day, Monday", 9, true},
		{"odd day, Tuesday", 3, false},
		{"even day, Monday", 2, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			at := time.Date(2026, time.November, tt.day, 0, 0, 0, 0, time.UTC)
			if got := c.Matches(at); got != tt.want {
				t.Errorf("Matches(%s) = %v, want %v", at.Format("Mon 2006-01-02"), got, tt.want)
			}
		})
	}
}

// dueRuns calls Due on every minute of the 24 real hours from start and
// returns the instants at which task expr was due.
func dueRuns(t *testing.T, loc *time.Location, expr string, start time.Time) []time.Time {
	t.Helper()

	s := newSchedule(schedule.Options{Location: loc})
	task := s.Func("task", func(context.Context) error { return nil }).Cron(expr)

	var runs []time.Time

	for at := start; at.Before(start.Add(24 * time.Hour)); at = at.Add(time.Minute) {
		for _, d := range s.Due(at) {
			if d == task {
				runs = append(runs, at)
			}
		}
	}

	return runs
}

func TestDueAcrossDSTChanges(t *testing.T) {
	t.Parallel()

	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}

	fallBack := time.Date(2026, time.November, 1, 0, 0, 0, 0, ny) // 01:00-01:59 happens twice
	springFwd := time.Date(2026, time.March, 8, 0, 0, 0, 0, ny)   // 02:00-02:59 never happens

	tests := []struct {
		name  string
		loc   *time.Location
		expr  string
		start time.Time
		want  int
		first time.Time // zero: not checked
	}{
		{
			"repeated hour runs once",
			ny,
			"30 1 * * *",
			fallBack,
			1,
			time.Date(2026, time.November, 1, 5, 30, 0, 0, time.UTC),
		},
		{
			"skipped hour runs after the jump",
			ny,
			"30 2 * * *",
			springFwd,
			1,
			time.Date(2026, time.March, 8, 7, 0, 0, 0, time.UTC),
		},
		{"wildcard hour keeps real time on fall back", ny, "*/15 * * * *", fallBack, 96, time.Time{}},
		{"wildcard hour keeps real time on spring forward", ny, "*/15 * * * *", springFwd, 96, time.Time{}},
		{
			"no DST zone unchanged",
			time.UTC,
			"30 1 * * *",
			time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC),
			1,
			time.Date(2026, time.November, 1, 1, 30, 0, 0, time.UTC),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			runs := dueRuns(t, tt.loc, tt.expr, tt.start)
			if len(runs) != tt.want {
				t.Fatalf("%q due %d times in 24h from %s, want %d: %v", tt.expr, len(runs), tt.start, tt.want, runs)
			}

			if !tt.first.IsZero() && !runs[0].Equal(tt.first) {
				t.Errorf("%q first due at %s, want %s", tt.expr, runs[0].UTC(), tt.first)
			}
		})
	}
}
