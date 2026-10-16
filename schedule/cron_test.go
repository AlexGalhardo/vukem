package schedule_test

import (
	"errors"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/schedule"
)

func at(s string) time.Time {
	t, err := time.Parse("2006-01-02 15:04", s)
	if err != nil {
		panic(err)
	}

	return t
}

func TestCronMatches(t *testing.T) {
	t.Parallel()

	tests := []struct {
		expr string
		yes  []string
		no   []string
	}{
		{"* * * * *", []string{"2026-03-04 05:06"}, nil},
		{"*/15 * * * *", []string{"2026-01-01 10:00", "2026-01-01 10:45"}, []string{"2026-01-01 10:05"}},
		{"30 8 * * 1-5", []string{"2026-10-16 08:30"}, []string{"2026-10-17 08:30", "2026-10-16 08:31"}}, // Fri / Sat
		{"0 0 1 JAN *", []string{"2026-01-01 00:00"}, []string{"2026-02-01 00:00"}},
		{"0 12 * * SUN", []string{"2026-10-18 12:00"}, []string{"2026-10-19 12:00"}},
		{"0 12 * * 7", []string{"2026-10-18 12:00"}, nil},
		{
			"10-50/20 * * * *",
			[]string{"2026-01-01 00:10", "2026-01-01 00:30", "2026-01-01 00:50"},
			[]string{"2026-01-01 00:20"},
		},
		{"5/30 * * * *", []string{"2026-01-01 00:05", "2026-01-01 00:35"}, []string{"2026-01-01 00:00"}},
		{
			"0 0 13 * FRI",
			[]string{"2026-11-13 00:00", "2026-10-16 00:00"},
			[]string{"2026-10-15 00:00"},
		}, // OR semantics
		{"0 0 1,15 * *", []string{"2026-05-15 00:00"}, []string{"2026-05-14 00:00"}},
		{"@hourly", []string{"2026-01-01 07:00"}, []string{"2026-01-01 07:01"}},
		{"@daily", []string{"2026-01-01 00:00"}, []string{"2026-01-01 01:00"}},
		{"@weekly", []string{"2026-10-18 00:00"}, nil},
		{"@monthly", []string{"2026-02-01 00:00"}, nil},
		{"@yearly", []string{"2027-01-01 00:00"}, []string{"2027-02-01 00:00"}},
	}

	for _, tt := range tests {
		c := schedule.MustParseCron(tt.expr)

		for _, s := range tt.yes {
			if !c.Matches(at(s)) {
				t.Errorf("%q should match %s", tt.expr, s)
			}
		}

		for _, s := range tt.no {
			if c.Matches(at(s)) {
				t.Errorf("%q should not match %s", tt.expr, s)
			}
		}
	}
}

func TestCronErrors(t *testing.T) {
	t.Parallel()

	for _, expr := range []string{
		"", "* * * *", "* * * * * *", "60 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "* * * * 8",
		"*/0 * * * *", "5-1 * * * *", "a * * * *", "*/x * * * *", "1-x * * * *", "x-1 * * * *",
	} {
		if _, err := schedule.ParseCron(expr); !errors.Is(err, schedule.ErrCron) {
			t.Errorf("ParseCron(%q) = %v, want ErrCron", expr, err)
		}
	}

	defer func() {
		if recover() == nil {
			t.Error("MustParseCron did not panic")
		}
	}()

	schedule.MustParseCron("bad")
}

func TestCronNext(t *testing.T) {
	t.Parallel()

	kolkata := time.FixedZone("IST", 5*3600+30*60)

	tests := []struct {
		expr string
		from time.Time
		want time.Time
	}{
		{"30 8 * * 1-5", at("2026-10-16 09:00"), at("2026-10-19 08:30")}, // Friday after → Monday
		{"0 0 29 2 *", at("2026-03-01 00:00"), at("2028-02-29 00:00")},
		{"*/15 * * * *", at("2026-01-01 10:14"), at("2026-01-01 10:15")},
		{"0 * * * *", time.Date(2026, 1, 1, 10, 10, 0, 0, kolkata), time.Date(2026, 1, 1, 11, 0, 0, 0, kolkata)},
		{"0 0 31 2 *", at("2026-01-01 00:00"), time.Time{}}, // never
	}

	for _, tt := range tests {
		if got := schedule.MustParseCron(tt.expr).Next(tt.from); !got.Equal(tt.want) {
			t.Errorf("Next(%q, %s) = %s, want %s", tt.expr, tt.from, got, tt.want)
		}
	}
}

func FuzzParseCron(f *testing.F) {
	for _, s := range []string{"* * * * *", "*/5 1-3 1,15 JAN-MAR MON-FRI", "@daily", "0 0 31 2 *", "1-2/3 */4 5 6 7"} {
		f.Add(s)
	}

	from := at("2026-06-15 12:34")

	f.Fuzz(func(t *testing.T, expr string) {
		c, err := schedule.ParseCron(expr)
		if err != nil {
			return
		}

		next := c.Next(from)
		if next.IsZero() {
			return
		}

		if !next.After(from) || !c.Matches(next) {
			t.Fatalf("Next(%q) = %s: not after %s or not matching", expr, next, from)
		}
	})
}
