package queue

import (
	"context"
	"sync"
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
	// StopWhenEmpty returns when no job is available.
	StopWhenEmpty bool
}

// Work processes jobs until ctx is cancelled. On cancellation it stops
// reserving new jobs and waits for running ones (graceful drain): they
// keep a context that is not cancelled but bounded by their timeout.
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

	jobCtx := context.WithoutCancel(ctx)

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
					_ = q.driver.Release(jobCtx, m)
					return
				}

				if err := q.Process(jobCtx, m); err != nil {
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

	wg.Wait()

	return firstErr
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
