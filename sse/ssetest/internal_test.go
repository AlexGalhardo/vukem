package ssetest

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/AlexGalhardo/vukem/sse"
)

// faulty is an in-process broker with one switchable defect, to prove the
// suite catches the regressions it exists for.
type faulty struct {
	// leak: the channel is not closed when ctx is done.
	leak bool
	// late: the subscription is registered 50 ms after Subscribe returns.
	late bool
	// everything: Publish delivers to every channel's subscribers.
	everything bool

	mu   sync.Mutex
	subs map[string]map[chan []byte]struct{}
}

func (f *faulty) Publish(_ context.Context, channel string, payload []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	for c, set := range f.subs {
		if c != channel && !f.everything {
			continue
		}

		for ch := range set {
			select {
			case ch <- append([]byte(nil), payload...):
			default:
			}
		}
	}

	return nil
}

func (f *faulty) register(out chan []byte, channels []string) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.subs == nil {
		f.subs = map[string]map[chan []byte]struct{}{}
	}

	for _, c := range channels {
		if f.subs[c] == nil {
			f.subs[c] = map[chan []byte]struct{}{}
		}

		f.subs[c][out] = struct{}{}
	}
}

func (f *faulty) Subscribe(ctx context.Context, channels ...string) (<-chan []byte, error) {
	out := make(chan []byte, 256)

	if f.late {
		time.AfterFunc(50*time.Millisecond, func() { f.register(out, channels) })
	} else {
		f.register(out, channels)
	}

	context.AfterFunc(ctx, func() {
		f.mu.Lock()
		defer f.mu.Unlock()

		for _, c := range channels {
			delete(f.subs[c], out)
		}

		if !f.leak {
			close(out)
		}
	})

	return out, nil
}

// recorder is a testing.TB that records a failure and stops the case's
// goroutine instead of failing the real test.
type recorder struct {
	testing.TB

	failed atomic.Bool
}

func (r *recorder) fail()                 { r.failed.Store(true) }
func (r *recorder) Fail()                 { r.fail() }
func (r *recorder) Error(...any)          { r.fail() }
func (r *recorder) Errorf(string, ...any) { r.fail() }
func (r *recorder) FailNow()              { r.fail(); runtime.Goexit() }
func (r *recorder) Fatal(...any)          { r.fail(); runtime.Goexit() }
func (r *recorder) Fatalf(string, ...any) {
	r.fail()
	runtime.Goexit()
}

// fails runs the named case against b and reports whether it failed.
func fails(t *testing.T, name string, b sse.Broker) bool {
	t.Helper()

	for _, c := range cases() {
		if c.name != name {
			continue
		}

		rec := &recorder{TB: t}
		done := make(chan struct{})

		go func() {
			defer close(done)

			c.fn(rec, b, Options{Timeout: 300 * time.Millisecond})
		}()

		<-done

		return rec.failed.Load()
	}

	t.Fatalf("no case named %q", name)

	return false
}

func TestSuiteCatchesBrokenBrokers(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		broker *faulty
		caught string
	}{
		{"channel not closed on cancel", &faulty{leak: true}, "CancelClosesTheChannel"},
		{"subscription registered after Subscribe returns", &faulty{late: true}, "SubscribeIsReadyWhenItReturns"},
		{"every channel delivered to every subscriber", &faulty{everything: true}, "OnlySubscribedChannel"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if !fails(t, tt.caught, tt.broker) {
				t.Errorf("%s passed against a broker with this defect", tt.caught)
			}

			// The same case passes against a correct broker.
			if fails(t, tt.caught, &faulty{}) {
				t.Errorf("%s failed against a correct broker", tt.caught)
			}
		})
	}
}
