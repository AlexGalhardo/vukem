package queue

import (
	"context"
	"errors"
	"fmt"
	"math"
)

// JobProgress is one progress report of a running job, delivered to the
// [WithProgress] hook and to observers that implement [ProgressObserver].
// Like [JobEvent] it carries no payload.
type JobProgress struct {
	ID    string
	Kind  string
	Queue string
	// Attempt is the 1-based attempt number that reported.
	Attempt int
	// Fraction is the completed share of the work, clamped to [0, 1].
	Fraction float64
	// Message is a short free-form status such as "encoding".
	Message string
}

// ProgressObserver is implemented by an [Observer] that wants progress
// reports too. It is optional so existing observers keep compiling.
type ProgressObserver interface {
	ObserveProgress(ctx context.Context, p JobProgress)
}

// WithProgress sets the hook that receives the progress a handler reports
// with [Progress]. It is a handler option (pass it to [Handle] or
// [HandleKind]): a dispatch-time option is ignored, because the hook is a
// function and is not stored with the job. Progress is not persisted; the
// hook decides what to do with it (keep it in memory, publish an event).
// The hook runs on the handler's goroutine, so it must be fast and safe
// for concurrent use; a panic in it is logged and skipped.
func WithProgress(fn func(ctx context.Context, p JobProgress)) Option {
	return func(o *options) { o.progress = fn }
}

// progressKey keys the progress reporter of the running job in a handler's
// context.
type progressKey struct{}

// Progress reports how far the running job is, from its handler: fraction
// is the completed share of the work (clamped to [0, 1]; NaN is ignored)
// and message a short status. It reaches the [WithProgress] hook of the
// handler and every observer that implements [ProgressObserver]. Outside a
// queue handler (a handler called directly in a test) it does nothing.
//
// Progress is not stored: a worker that restarts loses it, and a retried
// attempt starts again from whatever the handler reports.
func Progress(ctx context.Context, fraction float64, message string) {
	report, ok := ctx.Value(progressKey{}).(func(context.Context, float64, string))
	if !ok || math.IsNaN(fraction) {
		return
	}

	report(ctx, min(max(fraction, 0), 1), message)
}

// reporter returns the function Progress calls for attempt a of m.
func (q *Queue) reporter(h handler, m *Message, a Attempt) func(context.Context, float64, string) {
	return func(ctx context.Context, fraction float64, message string) {
		p := JobProgress{
			ID: m.ID, Kind: m.Kind, Queue: m.Queue, Attempt: a.Number, Fraction: fraction, Message: message,
		}

		if h.opts.progress != nil {
			q.deliverProgress(ctx, p, "hook", h.opts.progress)
		}

		q.mu.RLock()
		obs := q.obs
		q.mu.RUnlock()

		var list []Observer

		switch v := obs.(type) {
		case nil:
		case observers:
			list = v
		default:
			list = []Observer{v}
		}

		for _, o := range list {
			if po, ok := o.(ProgressObserver); ok {
				q.deliverProgress(ctx, p, fmt.Sprintf("%T", o), po.ObserveProgress)
			}
		}
	}
}

// deliverProgress calls fn; a panic is logged and skipped so a faulty hook
// cannot fail the job.
func (q *Queue) deliverProgress(ctx context.Context, p JobProgress, who string, fn func(context.Context, JobProgress)) {
	defer func() {
		if r := recover(); r != nil {
			q.log.ErrorContext(ctx, "queue: progress receiver panicked",
				"receiver", who, "job", p.Kind, "id", p.ID, "panic", fmt.Sprint(r))
		}
	}()

	fn(ctx, p)
}

// ErrNotRunning is returned by [Queue.Cancel] when no job with that id is
// running in this process: it is queued, waiting for a retry, finished, or
// running in another process.
var ErrNotRunning = errors.New("queue: job is not running in this process")

// ErrCancelled is the cause of the context of a job stopped by
// [Queue.Cancel] (context.Cause(ctx)), and part of the error the attempt
// then fails with.
var ErrCancelled = errors.New("queue: job cancelled")

// runningJob is a job attempt registered so Cancel can stop it.
type runningJob struct {
	cancel context.CancelCauseFunc
}

// track registers the attempt of id and returns the function that removes it.
func (q *Queue) track(id string, cancel context.CancelCauseFunc) func() {
	rj := &runningJob{cancel: cancel}

	q.runMu.Lock()
	if q.running == nil {
		q.running = map[string]*runningJob{}
	}

	q.running[id] = rj
	q.runMu.Unlock()

	return func() {
		q.runMu.Lock()
		defer q.runMu.Unlock()

		if q.running[id] == rj {
			delete(q.running, id)
		}
	}
}

// Cancel stops the job with this id if one of its attempts is running in
// this process: the handler's context is cancelled with [ErrCancelled] as
// its cause. A handler that honours its context returns, and the job then
// moves to the failed jobs without further retries (a cancel is a verdict,
// like [ErrPermanent]); a handler that returns nil anyway completes
// normally. It returns [ErrNotRunning] when the job is not running here,
// so it does not reach a job another process runs, nor one that is only
// queued: there is no cross-process cancel and a queued job is not removed.
//
// Get the id from [Queue.DispatchID] or from the JobDispatched event of an
// [Observer]. A job run inline (Sync) finishes before Dispatch returns, so
// only an observer sees its id in time.
func (q *Queue) Cancel(id string) error {
	q.runMu.Lock()
	rj, ok := q.running[id]
	q.runMu.Unlock()

	if !ok {
		return fmt.Errorf("%w: %s", ErrNotRunning, id)
	}

	rj.cancel(ErrCancelled)

	return nil
}

// DispatchID is [Queue.Dispatch] that also returns the id of the job, for
// [Queue.Cancel] and for matching the events of observers. For a job sent
// with [Unique] that already exists it returns the id of the existing job.
// The id is empty when the dispatch fails.
func (q *Queue) DispatchID(ctx context.Context, j Job, opts ...Option) (string, error) {
	return q.DispatchKindID(ctx, j.Kind(), j, opts...)
}

// DispatchKindID is [Queue.DispatchKind] that also returns the id of the job.
func (q *Queue) DispatchKindID(ctx context.Context, kind string, payload any, opts ...Option) (string, error) {
	m, h, err := q.message(kind, payload, opts)
	if err != nil {
		return "", err
	}

	if err := q.send(ctx, m, h); err != nil {
		return "", err
	}

	return m.ID, nil
}
