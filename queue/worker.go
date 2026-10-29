package queue

import (
	"context"
	"errors"
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
	// Limits caps the jobs of a queue running at once (queue name to
	// maximum; a queue not listed shares all Concurrency workers). A
	// high-priority queue with a limit cannot take every worker, so a
	// slow family (imports, ingestion) runs at 1-2 while the other queues
	// keep the rest. The cap holds per Work call (per process).
	Limits map[string]int
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
	// running after it have their context cancelled. A job that then fails
	// with that cancellation (context.Canceled or DeadlineExceeded) goes
	// back to the queue without using an attempt, MaxFreeInterruptions
	// times in a row; later interruptions use attempts, and other errors
	// are retried or failed as usual. Jobs run again from the start, so
	// they must be idempotent. 0 waits for them (each is still bounded by
	// its own timeout). Jobs must honor ctx; one that ignores it still
	// holds Work.
	StopTimeout time.Duration
}

// Work processes jobs until ctx is cancelled. On cancellation it stops
// reserving new jobs and waits for running ones (graceful drain): they
// keep a context that is not cancelled, bounded by their timeout and by
// StopTimeout.
//
//nolint:gocognit // worker loop with shutdown, retry and backoff paths that share state
func (q *Queue) Work(ctx context.Context, o WorkOptions) error {
	if len(o.Queues) == 0 {
		o.Queues = []string{"default"}
	}

	o.Concurrency = max(o.Concurrency, 1)

	if o.Sleep <= 0 {
		o.Sleep = time.Second
	}

	for name, n := range o.Limits {
		if n < 1 {
			return fmt.Errorf("%w: %q = %d", ErrInvalidLimit, name, n)
		}
	}

	slots := newQueueSlots(o.Queues, o.Limits)

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
				queues, held := slots.claim()

				var (
					m   *Message
					err error
				)

				if len(queues) > 0 {
					m, err = q.driver.Pop(ctx, queues)
				}

				// Keep only the slot of the queue the job came from.
				used := ""
				if m != nil {
					used = m.Queue
				}

				slots.release(held, used)

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
					rctx, cancel := context.WithTimeout(settleCtx, settleTimeout)
					_ = q.driver.Release(rctx, m)

					cancel()
					slots.release([]string{m.Queue}, "")

					return
				}

				err = q.process(jobCtx, settleCtx, m, interrupted, settleTimeout)
				slots.release([]string{m.Queue}, "")

				if err != nil {
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

// ErrInvalidLimit is returned by Work for a WorkOptions.Limits entry
// below 1.
var ErrInvalidLimit = errors.New("queue: queue limit must be at least 1")

// queueSlots counts the running jobs of limited queues. A worker claims a
// slot of every limited queue with room before it reserves, so two
// workers polling at once can never exceed a limit, then keeps only the
// slot of the queue its job came from.
type queueSlots struct {
	mu      sync.Mutex
	queues  []string
	limits  map[string]int
	running map[string]int
}

func newQueueSlots(queues []string, limits map[string]int) *queueSlots {
	return &queueSlots{queues: queues, limits: limits, running: map[string]int{}}
}

// claim returns the queues to poll, in priority order, and the limited
// ones whose slot it took.
func (s *queueSlots) claim() (poll, held []string) {
	if len(s.limits) == 0 {
		return s.queues, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, name := range s.queues {
		limit, ok := s.limits[name]
		if !ok {
			poll = append(poll, name)
			continue
		}

		if s.running[name] < limit {
			s.running[name]++
			poll, held = append(poll, name), append(held, name)
		}
	}

	return poll, held
}

// release gives back the slots in held except the one of keep (the queue
// of the job about to run; "" keeps none).
func (s *queueSlots) release(held []string, keep string) {
	if len(s.limits) == 0 {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, name := range held {
		if name == keep {
			keep = ""
			continue
		}

		if _, ok := s.limits[name]; ok {
			s.running[name]--
		}
	}
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

// settleTimeout bounds each Ack, Release or Fail of a worker, so a store
// that hangs cannot hold the worker (and the shutdown) forever.
const settleTimeout = 10 * time.Second

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
