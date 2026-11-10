package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestMemoryDegenerateWindow(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		m    *Memory
	}{
		{"zero window", NewMemory(5, 0)},
		{"negative window", NewMemory(5, -time.Second)},
		{"sub-interval window", NewMemory(1000, 500*time.Nanosecond)},
		{"burst zero window", NewMemoryBurst(1, 0, 5)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			res, err := tt.m.Allow(context.Background(), "k")
			if err != nil || !res.Allowed {
				t.Errorf("Allow = %+v, %v; want allowed", res, err)
			}
		})
	}
}
