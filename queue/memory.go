package queue

import (
	"context"
	"slices"
	"sync"
	"time"
)

// Memory keeps messages in process: jobs are lost on restart. Use it for
// tests and single-process apps that can afford that.
type Memory struct {
	now func() time.Time

	mu       sync.Mutex
	ready    []*Message
	reserved map[string]*Message
	failed   []Message
}

// NewMemory returns an empty in-memory driver.
func NewMemory() *Memory {
	return &Memory{now: time.Now, reserved: map[string]*Message{}}
}

// SetClock sets the clock Pop compares availability with (default
// time.Now); it implements Clock.
func (d *Memory) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}

	d.mu.Lock()
	d.now = now
	d.mu.Unlock()
}

// Push implements Driver.
func (d *Memory) Push(_ context.Context, m *Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	cp := *m
	d.ready = append(d.ready, &cp)

	return nil
}

// Pop implements Driver: the earliest available message of the first
// queue in queues that has one.
func (d *Memory) Pop(_ context.Context, queues []string) (*Message, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.now()

	for _, name := range queues {
		best := -1

		for i, m := range d.ready {
			if m.Queue != name || m.AvailableAt.After(now) {
				continue
			}

			if best < 0 || m.AvailableAt.Before(d.ready[best].AvailableAt) {
				best = i
			}
		}

		if best >= 0 {
			m := d.ready[best]
			d.ready = slices.Delete(d.ready, best, best+1)
			d.reserved[m.ID] = m
			cp := *m

			return &cp, nil
		}
	}

	return nil, nil
}

// Ack implements Driver.
func (d *Memory) Ack(_ context.Context, m *Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.reserved, m.ID)

	return nil
}

// Release implements Driver.
func (d *Memory) Release(_ context.Context, m *Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.reserved, m.ID)

	cp := *m
	d.ready = append(d.ready, &cp)

	return nil
}

// Fail implements Driver.
func (d *Memory) Fail(_ context.Context, m *Message) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	delete(d.reserved, m.ID)
	d.failed = append(d.failed, *m)

	return nil
}

// Pending returns the number of waiting (including delayed) messages.
func (d *Memory) Pending() int {
	d.mu.Lock()
	defer d.mu.Unlock()

	return len(d.ready)
}

// Failed returns the failed messages.
func (d *Memory) Failed() []Message {
	d.mu.Lock()
	defer d.mu.Unlock()

	return slices.Clone(d.failed)
}
