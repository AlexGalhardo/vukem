package queue

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// WorkOptions configure Work.
type WorkOptions struct {
	// Queues are polled in priority order (default ["default"]).
	Queues []string
	// Concurrency is the number of parallel workers (default 1).
	Concurrency int
	// Sleep is the wait when no job is available (default 1s).
	Sleep time.Duration
	// MaxJobs stops after processing this many jobs (0 = no limit), e.g.
	// for "queue:work --once".
	MaxJobs int
	// StopWhenEmpty returns when no job is available. A reserve error then
	// ends Work with that error (without it, the error is logged and the
	// worker keeps polling).
	StopWhenEmpty bool
	// StopTimeout bounds the drain once ctx is cancelled: jobs still
	// running after it have their context cancelled and go back to the
	// queue without using an attempt. 0 waits for them (each is still
	// bounded by its own timeout). Jobs must honor ctx; one that ignores
	// it still holds Work.
	StopTimeout time.Duration
}

// Work processes jobs until ctx is cancelled. On cancellation it stops
// reserving new jobs and waits for running ones (graceful drain): they
// keep a context that is not cancelled, bounded by their timeout and by
// StopTimeout.
func (q *Queue) Work(ctx context.Context, o WorkOptions) error {
	if len(o.Queues) == 0 {
		o.Queues = []string{"default"}
	}

	o.Concurrency = max(o.Concurrency, 1)

	if o.Sleep <= 0 {
		o.Sleep = time.Second
	}

	var (
		wg       sync.WaitGroup
		mu       sync.Mutex
		done     int
		firstErr error
	)

	// Running jobs outlive ctx so a shutdown does not abort them mid-way;
	// settling always uses a context that is never cancelled.
	settleCtx := context.WithoutCancel(ctx)
	jobCtx, cancelJobs := context.WithCancel(settleCtx)

	defer cancelJobs()

	var stopping atomic.Bool

	interrupted := func() bool { return stopping.Load() && jobCtx.Err() != nil }

	take := func() bool {
		mu.Lock()
		defer mu.Unlock()

		if o.MaxJobs > 0 && done >= o.MaxJobs {
			return false
		}

		done++

		return true
	}

	for range o.Concurrency {
		wg.Go(func() {
			for ctx.Err() == nil {
				m, err := q.driver.Pop(ctx, o.Queues)
				if err != nil {
					if ctx.Err() != nil {
						return
					}

					q.log.ErrorContext(ctx, "queue: reserve job", "err", err)

					// A store error is not an empty queue: report it rather
					// than exiting cleanly with jobs left behind.
					if o.StopWhenEmpty {
						mu.Lock()
						if firstErr == nil {
							firstErr = fmt.Errorf("queue: reserve job: %w", err)
						}
						mu.Unlock()

						return
					}
				}

				if m == nil {
					if o.StopWhenEmpty {
						return
					}

					sleep(ctx, o.Sleep)

					continue
				}

				if !take() {
					// Over the limit: give the job back untouched.
					_ = q.driver.Release(settleCtx, m)
					return
				}

				if err := q.process(jobCtx, settleCtx, m, interrupted); err != nil {
					mu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					mu.Unlock()

					q.log.ErrorContext(ctx, "queue: settle job", "job", m.Kind, "id", m.ID, "err", err)
				}

				mu.Lock()
				limit := o.MaxJobs > 0 && done >= o.MaxJobs
				mu.Unlock()

				if limit {
					return
				}
			}
		})
	}

	q.drain(ctx, &wg, o.StopTimeout, func() { stopping.Store(true); cancelJobs() })

	return firstErr
}

// drain waits for the workers; once ctx is cancelled it gives running jobs
// stopTimeout (0 = no limit) before calling cancel.
func (q *Queue) drain(ctx context.Context, wg *sync.WaitGroup, stopTimeout time.Duration, cancel func()) {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()

	select {
	case <-done:
		return
	case <-ctx.Done():
	}

	if stopTimeout <= 0 {
		<-done
		return
	}

	stop := time.NewTimer(stopTimeout)
	defer stop.Stop()

	select {
	case <-done:
		return
	case <-stop.C:
	}

	q.log.WarnContext(ctx, "queue jobs still running at shutdown: cancelling them", "stop_timeout", stopTimeout)
	cancel()
	<-done
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
