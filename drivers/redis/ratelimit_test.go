package redis

import (
	"testing"
	"time"
)

func TestRateLimiterNormalisesWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		limit      int
		window     time.Duration
		wantLimit  int
		wantWindow time.Duration
	}{
		{"limit above window milliseconds", 5000, time.Second, 1000, time.Second},
		{"zero window", 5, 0, 5, time.Second},
		{"negative window", 5, -time.Second, 5, time.Second},
		{"sub-millisecond window", 5, time.Microsecond, 1, time.Microsecond},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			l := (&Client{}).RateLimiter("", tt.limit, tt.window)
			if l.limit != tt.wantLimit || l.window != tt.wantWindow {
				t.Errorf("limit, window = %d, %v; want %d, %v", l.limit, l.window, tt.wantLimit, tt.wantWindow)
			}
		})
	}
}
