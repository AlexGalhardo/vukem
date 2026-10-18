package queue

import (
	"context"
	"fmt"
	"reflect"
	"time"
)

// JobStatus is the step a JobEvent reports.
type JobStatus string

// Job steps reported to an Observer.
const (
	// JobDispatched reports that the job was stored (or is about to run inline).
	JobDispatched JobStatus = "dispatched"
	// JobProcessed reports that an attempt succeeded.
	JobProcessed JobStatus = "processed"
	// JobFailed reports that an attempt failed (it may still be retried).
	JobFailed JobStatus = "failed"
)

// JobEvent describes one step of a job for an Observer. It carries no
// payload: payloads may hold user data.
type JobEvent struct {
	ID    string
	Kind  string
	Queue string
	// Status is the step reported.
	Status JobStatus
	// Attempt is the 1-based attempt number (0 on JobDispatched).
	Attempt int
	// MaxRetries is how many retries the job was dispatched with, so
	// Attempt of MaxRetries+1 is its last one. It is 0 with an Inline
	// driver (Sync), which does not retry.
	MaxRetries int
	// Final reports, on JobFailed, that the job was not retried: it moved
	// to the failed jobs, either because the handler returned
	// ErrPermanent or because its retries ran out. It is false on the
	// other statuses, and false for an attempt a shutdown gave back (see
	// MaxFreeInterruptions), which Attempt and MaxRetries alone cannot
	// tell apart from an exhausted one.
	Final    bool
	Duration time.Duration
	Err      error
}

// Observer is notified when jobs are dispatched and attempted. It runs on
// the caller's goroutine, so it must be fast and safe for concurrent use.
type Observer interface {
	ObserveJob(ctx context.Context, e JobEvent)
}

// SetObserver replaces the queue's observers with obs (development tooling
// such as devtools; nil, or a typed nil pointer, removes them). Call it
// before the queue is used; AddObserver keeps the existing ones.
func (q *Queue) SetObserver(obs Observer) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.obs = chainObservers(nil, obs)
}

// AddObserver adds obs after the queue's current observers (devtools next
// to a tracing observer). Call it before the queue is used.
func (q *Queue) AddObserver(obs Observer) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.obs = chainObservers(q.obs, obs)
}

// observers notifies each observer in order.
type observers []Observer

func (obs observers) ObserveJob(ctx context.Context, e JobEvent) {
	for _, o := range obs {
		o.ObserveJob(ctx, e)
	}
}

// chainObservers returns cur followed by next, skipping nils (typed nil
// pointers too, whose methods would panic), without nesting fan-outs.
func chainObservers(cur, next Observer) Observer {
	switch {
	case isNil(next):
		return cur
	case cur == nil:
		return next
	}

	var out observers
	if multi, ok := cur.(observers); ok {
		out = append(out, multi...)
	} else {
		out = append(out, cur)
	}

	return append(out, next)
}

func isNil(obs Observer) bool {
	if obs == nil {
		return true
	}

	v := reflect.ValueOf(obs)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return v.IsNil()
	default:
		return false
	}
}

func (q *Queue) observe(ctx context.Context, m *Message, status JobStatus, d time.Duration, err error, final bool) {
	q.mu.RLock()
	obs := q.obs
	q.mu.RUnlock()

	if obs == nil {
		return
	}

	e := JobEvent{
		ID: m.ID, Kind: m.Kind, Queue: m.Queue, Status: status, Attempt: m.Attempts,
		MaxRetries: m.MaxRetries, Final: final, Duration: d, Err: err,
	}

	if multi, ok := obs.(observers); ok {
		for _, o := range multi {
			q.notifyOne(ctx, o, e)
		}

		return
	}

	q.notifyOne(ctx, obs, e)
}

// notifyOne reports e to obs. A panicking observer is logged and skipped:
// it must not fail the dispatch or the attempt, and the observers after it
// still run.
func (q *Queue) notifyOne(ctx context.Context, obs Observer, e JobEvent) {
	defer func() {
		if p := recover(); p != nil {
			// The event carries no payload; the error text may, so it is left out.
			q.log.ErrorContext(ctx, "queue: job observer panicked",
				"observer", fmt.Sprintf("%T", obs), "job", e.Kind, "id", e.ID, "panic", fmt.Sprint(p))
		}
	}()

	obs.ObserveJob(ctx, e)
}

func finished(err error) JobStatus {
	if err != nil {
		return JobFailed
	}

	return JobProcessed
}
