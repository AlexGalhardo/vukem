package gcra

import (
	"testing"
	"time"
)

func TestNext(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	const (
		interval  = 100 * time.Millisecond
		tolerance = 500 * time.Millisecond
	)

	tests := []struct {
		name        string
		tat         time.Time
		wantNext    time.Time
		wantAllowAt time.Time
	}{
		{"zero tat", time.Time{}, now.Add(interval), now.Add(interval - tolerance)},
		{"tat in the past", now.Add(-time.Hour), now.Add(interval), now.Add(interval - tolerance)},
		{"tat now", now, now.Add(interval), now.Add(interval - tolerance)},
		{
			"tat ahead within the tolerance",
			now.Add(300 * time.Millisecond),
			now.Add(400 * time.Millisecond),
			now.Add(-100 * time.Millisecond),
		},
		{
			"tat ahead beyond the tolerance",
			now.Add(time.Second),
			now.Add(1100 * time.Millisecond),
			now.Add(600 * time.Millisecond),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			next, allowAt := Next(tt.tat, now, interval, tolerance)
			if !next.Equal(tt.wantNext) || !allowAt.Equal(tt.wantAllowAt) {
				t.Errorf("Next() = %v, %v; want %v, %v", next, allowAt, tt.wantNext, tt.wantAllowAt)
			}
		})
	}
}
