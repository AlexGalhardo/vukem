package ssetest_test

import (
	"context"
	"sync"
	"testing"

	"github.com/AlexGalhardo/vukem/sse/ssetest"
)

// memBroker is a correct in-process broker: it listens before Subscribe
// returns and closes the channel when ctx is done. Publish and the close
// share one mutex so nothing is sent on a closed channel.
type memBroker struct {
	mu   sync.Mutex
	subs map[string]map[chan []byte]struct{}
}

func (m *memBroker) Publish(_ context.Context, channel string, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	for ch := range m.subs[channel] {
		select {
		case ch <- append([]byte(nil), payload...):
		default: // a full buffer drops, like a slow consumer
		}
	}

	return nil
}

func (m *memBroker) Subscribe(ctx context.Context, channels ...string) (<-chan []byte, error) {
	out := make(chan []byte, 256)

	m.mu.Lock()

	if m.subs == nil {
		m.subs = map[string]map[chan []byte]struct{}{}
	}

	for _, c := range channels {
		if m.subs[c] == nil {
			m.subs[c] = map[chan []byte]struct{}{}
		}

		m.subs[c][out] = struct{}{}
	}

	m.mu.Unlock()

	context.AfterFunc(ctx, func() {
		m.mu.Lock()
		defer m.mu.Unlock()

		for _, c := range channels {
			delete(m.subs[c], out)
		}

		close(out)
	})

	return out, nil
}

func TestMemoryBrokerConformance(t *testing.T) {
	t.Parallel()

	ssetest.Run(t, &memBroker{}, ssetest.Options{})
}

func TestMemoryBrokerConformanceWithPayloadLimit(t *testing.T) {
	t.Parallel()

	ssetest.Run(t, &memBroker{}, ssetest.Options{MaxPayload: 7999})
}
