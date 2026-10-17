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
