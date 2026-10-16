package queue

import (
	"context"
	"encoding/json"
	"sync"
)

// Fake records dispatched jobs for tests; it stores them like Memory, so
// a test can also run them with Queue.Work.
//
//	fake := queue.NewFake()
//	app := vukemtest.New(t, bootstrap.New, vukemtest.Options{AppOptions: []vukem.Option{vukem.WithQueueDriver(fake)}})
//	... exercise the app ...
//	invoices := queue.Dispatched[jobs.SendInvoice](fake)
type Fake struct {
	*Memory

	mu   sync.Mutex
	sent []Message
}

// NewFake returns an empty fake driver.
func NewFake() *Fake { return &Fake{Memory: NewMemory()} }

// Push implements Driver and records m.
func (f *Fake) Push(ctx context.Context, m *Message) error {
	f.mu.Lock()
	f.sent = append(f.sent, *m)
	f.mu.Unlock()

	return f.Memory.Push(ctx, m)
}

// Messages returns every dispatched message in order.
func (f *Fake) Messages() []Message {
	f.mu.Lock()
	defer f.mu.Unlock()

	out := make([]Message, len(f.sent))
	copy(out, f.sent)

	return out
}

// Dispatched returns the dispatched jobs of type J, decoded, in order.
func Dispatched[J Job](f *Fake) []J {
	var (
		zero J
		out  []J
	)

	msgs := f.Messages()
	for i := range msgs {
		m := &msgs[i]
		if m.Kind != zero.Kind() {
			continue
		}

		var j J
		if json.Unmarshal(m.Payload, &j) == nil {
			out = append(out, j)
		}
	}

	return out
}
