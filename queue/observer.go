package queue

import (
	"context"
	"time"
)

// JobStatus is the step a JobEvent reports.
type JobStatus string

// Job steps reported to an Observer.
const (
	// JobDispatched: the job was stored (or is about to run inline).
	JobDispatched JobStatus = "dispatched"
	// JobProcessed: an attempt succeeded.
	JobProcessed JobStatus = "processed"
	// JobFailed: an attempt failed (it may still be retried).
	JobFailed JobStatus = "failed"
)

// JobEvent describes one step of a job for an Observer. It carries no
// payload: payloads may hold user data.
type JobEvent struct {
	ID       string
	Kind     string
	Queue    string
	Status   JobStatus
	Attempt  int
	Duration time.Duration
	Err      error
}

// Observer is notified when jobs are dispatched and attempted. It runs on
// the caller's goroutine, so it must be fast and safe for concurrent use.
type Observer interface {
	ObserveJob(ctx context.Context, e JobEvent)
}

// SetObserver reports job steps to obs (development tooling such as
// devtools). Call it before the queue is used.
func (q *Queue) SetObserver(obs Observer) {
	q.mu.Lock()
	defer q.mu.Unlock()

	q.obs = obs
}

func (q *Queue) observe(ctx context.Context, m *Message, status JobStatus, d time.Duration, err error) {
	q.mu.RLock()
	obs := q.obs
	q.mu.RUnlock()

	if obs == nil {
		return
	}

	obs.ObserveJob(ctx, JobEvent{
		ID: m.ID, Kind: m.Kind, Queue: m.Queue, Status: status, Attempt: m.Attempts, Duration: d, Err: err,
	})
}

func finished(err error) JobStatus {
	if err != nil {
		return JobFailed
	}

	return JobProcessed
}
