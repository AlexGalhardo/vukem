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
