package sse

import "testing"

func TestHubDropsEmptyTopics(t *testing.T) {
	t.Parallel()

	hub := NewHub(nil)
	t.Cleanup(hub.Close)

	_, stop := hub.Subscribe(t.Context(), "a", "b")
	stop()

	hub.mu.Lock()
	n := len(hub.subs)
	hub.mu.Unlock()

	if n != 0 {
		t.Errorf("topics after last unsubscribe = %d, want 0", n)
	}
}

func TestHubBufferSetsHowFarASubscriberMayLag(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name     string
		opts     []HubOption
		events   int
		wantOpen bool
	}{
		{"default fits 64", nil, 64, true},
		{"default drops at 65", nil, 65, false},
		{"custom fits 200", []HubOption{Buffer(200)}, 200, true},
		{"custom drops past its size", []HubOption{Buffer(3)}, 4, false},
		{"zero keeps the default", []HubOption{Buffer(0)}, 64, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			hub := NewHub(nil, tt.opts...)
			t.Cleanup(hub.Close)

			ch, stop := hub.Subscribe(t.Context(), "a")
			defer stop()

			for range tt.events {
				if err := hub.Publish(t.Context(), "a", Event{Data: "x"}); err != nil {
					t.Fatal(err)
				}
			}

			// The channel is buffered: drain what was kept, then see whether it closed.
			open := true
			for range tt.events {
				if _, ok := <-ch; !ok {
					open = false
					break
				}
			}

			if open != tt.wantOpen {
				t.Errorf("subscriber open after %d events = %v, want %v", tt.events, open, tt.wantOpen)
			}
		})
	}
}
