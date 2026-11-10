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
