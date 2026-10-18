// Package queue runs typed background jobs. A job is a struct with a
// Kind; its handler is a plain function registered explicitly:
//
//	type SendInvoice struct{ OrderID int64 }
//	func (SendInvoice) Kind() string { return "send_invoice" }
//
//	q.Register(queue.Handle(func(ctx context.Context, j SendInvoice) error { ... },
//		queue.Retries(5), queue.Backoff(queue.Exponential(time.Second)), queue.Timeout(time.Minute)))
//
//	err := q.Dispatch(ctx, SendInvoice{OrderID: 1}, queue.OnQueue("billing"), queue.Delay(5*time.Minute))
//
// A handler whose failure retrying cannot fix wraps [ErrPermanent] to skip
// its remaining retries; [AttemptFrom] tells it which attempt it is on and
// whether that is the last one.
//
// Payloads are JSON. Drivers store messages: Sync runs jobs inline, Memory
// keeps them in process, the database driver (and Redis) persist them.
// [Queue.Work] runs a worker (vukem queue:work); [Fake] records
// dispatched jobs in tests. Apps get a queue from app.Queue(), configured
// by QUEUE_CONNECTION.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/subsystems.md#queue.
package queue

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/database"
)

// Job is a unit of background work. Kind names its handler and must be
// stable across deploys (it is stored with the payload).
type Job interface {
	Kind() string
}

// Message is a stored job.
type Message struct {
	ID          string          `json:"id"`
	Kind        string          `json:"kind"`
	Queue       string          `json:"queue"`
	Payload     json.RawMessage `json:"payload"`
	Attempts    int             `json:"attempts"`
	MaxRetries  int             `json:"max_retries"`
	Timeout     time.Duration   `json:"timeout"`
	AvailableAt time.Time       `json:"available_at"`
	CreatedAt   time.Time       `json:"created_at"`
	// LastError is set when the job failed.
	LastError string `json:"last_error,omitempty"`
}

// Driver stores messages and hands them to workers.
type Driver interface {
	// Push stores m; it becomes available at m.AvailableAt.
	Push(ctx context.Context, m *Message) error
	// Pop reserves the next available message on one of queues (in
	// order), or returns nil when there is none.
	Pop(ctx context.Context, queues []string) (*Message, error)
	// Ack deletes a processed message.
	Ack(ctx context.Context, m *Message) error
	// Release makes m available again at m.AvailableAt (a retry).
	Release(ctx context.Context, m *Message) error
	// Fail moves m to the failed jobs.
	Fail(ctx context.Context, m *Message) error
}

// TxPusher is implemented by drivers that can enqueue inside a database
// transaction (Database).
type TxPusher interface {
	PushTx(ctx context.Context, ex database.Executor, m *Message) error
}

// FailedJob is a job that ran out of attempts.
type FailedJob struct {
	Message

	FailedAt time.Time `json:"failed_at"`
}

// FailedStore is implemented by drivers that keep failed jobs (Database).
type FailedStore interface {
	// Failed lists failed jobs, newest first.
	Failed(ctx context.Context) ([]FailedJob, error)
	// Retry re-queues failed jobs (all when ids is empty).
	Retry(ctx context.Context, ids ...string) (int, error)
	// Forget deletes failed jobs (all when ids is empty).
	Forget(ctx context.Context, ids ...string) (int, error)
}

// Inline is implemented by drivers that run jobs during Dispatch (Sync).
type Inline interface {
	Inline() bool
}

type options struct {
	queue   string
	retries int
	backoff func(attempt int) time.Duration
	timeout time.Duration
	delay   time.Duration
}

// Option configures a handler (defaults) or a single dispatch.
type Option func(*options)

// Retries sets how many times a failed job is retried (default 3).
func Retries(n int) Option { return func(o *options) { o.retries = max(n, 0) } }

// Backoff sets the delay before retry attempt n (1-based).
func Backoff(fn func(attempt int) time.Duration) Option { return func(o *options) { o.backoff = fn } }

// Timeout bounds one attempt (default 1 minute).
func Timeout(d time.Duration) Option { return func(o *options) { o.timeout = d } }

// OnQueue selects the queue (default "default").
func OnQueue(name string) Option { return func(o *options) { o.queue = name } }

// Delay makes the job available after d.
func Delay(d time.Duration) Option { return func(o *options) { o.delay = d } }

// Exponential backs off base, 2·base, 4·base... capped at one hour.
func Exponential(base time.Duration) func(int) time.Duration {
	return func(attempt int) time.Duration {
		d := time.Duration(float64(base) * math.Pow(2, float64(max(attempt-1, 0))))
		if d <= 0 || d > time.Hour {
			return time.Hour
		}

		return d
	}
}

// Constant always waits d.
func Constant(d time.Duration) func(int) time.Duration { return func(int) time.Duration { return d } }

// Registration binds a handler to its job kind; create it with Handle.
type Registration struct {
	kind string
	run  func(ctx context.Context, payload json.RawMessage) error
	opts []Option
}

// Handle registers fn for jobs of type J with default options.
func Handle[J Job](fn func(ctx context.Context, j J) error, opts ...Option) Registration {
	var zero J

	return HandleKind(zero.Kind(), fn, opts...)
}

// HandleKind registers fn for jobs named kind whose payload decodes into T
// (framework jobs such as queued mail and listeners; DispatchKind sends
// them).
func HandleKind[T any](kind string, fn func(ctx context.Context, v T) error, opts ...Option) Registration {
	return Registration{
		kind: kind,
		opts: opts,
		run: func(ctx context.Context, payload json.RawMessage) error {
			var v T
			if err := json.Unmarshal(payload, &v); err != nil {
				return fmt.Errorf("decode %s payload: %w", kind, err)
			}

			return fn(ctx, v)
		},
	}
}

// Kind returns the job kind of the registration.
func (r Registration) Kind() string { return r.kind }

type handler struct {
	run  func(ctx context.Context, payload json.RawMessage) error
	opts options
}

// Queue dispatches jobs to a driver and runs them in workers.
type Queue struct {
	driver Driver
	log    *slog.Logger
	now    func() time.Time

	mu       sync.RWMutex
	handlers map[string]handler
	obs      Observer
}

// New returns a queue on driver. log may be nil.
func New(driver Driver, log *slog.Logger) *Queue {
	if log == nil {
		log = slog.Default()
	}

	return &Queue{driver: driver, log: log, now: time.Now, handlers: map[string]handler{}}
}

// Driver returns the queue's driver.
func (q *Queue) Driver() Driver { return q.driver }

// Clock is implemented by drivers whose availability checks read a clock
// (Memory, Database); app.Queue sets the app clock on them.
type Clock interface {
	SetClock(now func() time.Time)
}

// SetClock sets the clock that stamps dispatched jobs and schedules
// retries (default time.Now). Delays only follow it when the driver
// compares availability with the same clock (Memory.SetClock,
// DatabaseOptions.Now); app.Queue wires the app clock (WithClock) into
// both.
func (q *Queue) SetClock(now func() time.Time) {
	if now == nil {
		now = time.Now
	}

	q.mu.Lock()
	q.now = now
	q.mu.Unlock()
}

func (q *Queue) clock() time.Time {
	q.mu.RLock()
	now := q.now
	q.mu.RUnlock()

	return now()
}

// ErrDuplicateKind is returned by Register for a kind registered twice.
var ErrDuplicateKind = errors.New("queue: job kind already registered")

// ErrUnknownKind is returned by Dispatch for a kind without a handler.
var ErrUnknownKind = errors.New("queue: no handler for job kind")

// Register adds handlers; a kind may be registered once.
func (q *Queue) Register(regs ...Registration) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	for _, r := range regs {
		if r.kind == "" {
			return errors.New("queue: job Kind() is empty")
		}

		if _, dup := q.handlers[r.kind]; dup {
			return fmt.Errorf("%w: %q", ErrDuplicateKind, r.kind)
		}

		o := defaults()
		for _, opt := range r.opts {
			opt(&o)
		}

		q.handlers[r.kind] = handler{run: r.run, opts: o}
	}

	return nil
}

func defaults() options {
	return options{queue: "default", retries: 3, backoff: Exponential(time.Second), timeout: time.Minute}
}

// Dispatch enqueues j. Options override the handler's defaults for this
// dispatch. With an Inline driver the job runs now and its error is
// returned; Inline drivers cannot delay jobs, so Delay returns
// ErrInlineDelay there.
func (q *Queue) Dispatch(ctx context.Context, j Job, opts ...Option) error {
	return q.DispatchKind(ctx, j.Kind(), j, opts...)
}

// DispatchKind enqueues payload (encoded as JSON) for the handler of kind.
func (q *Queue) DispatchKind(ctx context.Context, kind string, payload any, opts ...Option) error {
	m, h, err := q.message(kind, payload, opts)
	if err != nil {
		return err
	}

	return q.send(ctx, m, h)
}

// ErrInlineDelay is returned when a delayed job is dispatched to an Inline
// driver (Sync): running it now would run it early, so it is refused.
var ErrInlineDelay = errors.New(
	"queue: the sync driver cannot delay jobs (use QUEUE_CONNECTION=memory or database, or queue.Fake in tests)",
)

func (q *Queue) inline() bool {
	in, ok := q.driver.(Inline)
	return ok && in.Inline()
}

// check refuses what the driver cannot honour before anything is stored.
func (q *Queue) check(m *Message) error {
	if q.inline() && m.AvailableAt.After(m.CreatedAt) {
		return fmt.Errorf("%w: %s", ErrInlineDelay, m.Kind)
	}

	return nil
}

func (q *Queue) send(ctx context.Context, m *Message, h handler) error {
	if err := q.check(m); err != nil {
		return err
	}

	if q.inline() {
		// An Inline driver runs the job once and hands the error to the
		// caller, so its retry limit is 0: the single attempt is the last
		// one, both for the handler and for the observers.
		m.MaxRetries = 0
		q.observe(ctx, m, JobDispatched, 0, nil, false)

		start := time.Now()
		err := q.attempt(ctx, h, m, Attempt{Number: 1, MaxRetries: m.MaxRetries})
		q.observe(ctx, m, finished(err), time.Since(start), err, err != nil)

		return err
	}

	err := q.driver.Push(ctx, m)
	if err == nil {
		q.observe(ctx, m, JobDispatched, 0, nil, false)
	}

	return err
}

// ErrNoTx is returned by DispatchTx when ex is neither a *database.DB nor
// a *database.Tx and the driver cannot enqueue inside a transaction.
var ErrNoTx = errors.New("queue: driver does not support DispatchTx")

// DispatchTx enqueues j with the transaction of ex: the job exists only if
// the transaction commits, so it never runs for rolled-back work and is
// never lost for committed work.
//
// Drivers with transactional storage (Database) insert the job inside the
// transaction. Other drivers (Sync, Memory, Fake) dispatch it right after
// the commit (database.Tx.AfterCommit): Sync runs it then, and since the
// caller already returned, a handler error is logged instead of returned.
// Errors known up front (unknown kind, ErrInlineDelay) are returned inside
// the transaction. With a *database.DB (no open transaction) the job is
// dispatched now.
func (q *Queue) DispatchTx(ctx context.Context, ex database.Executor, j Job, opts ...Option) error {
	return q.DispatchKindTx(ctx, ex, j.Kind(), j, opts...)
}

// DispatchKindTx is DispatchTx for a payload sent to the handler of kind
// (framework jobs such as queued mail, see DispatchKind).
func (q *Queue) DispatchKindTx(ctx context.Context, ex database.Executor, kind string, payload any,
	opts ...Option,
) error {
	m, h, err := q.message(kind, payload, opts)
	if err != nil {
		return err
	}

	if tp, ok := q.driver.(TxPusher); ok {
		err = tp.PushTx(ctx, ex, m)
		if err == nil {
			q.observe(ctx, m, JobDispatched, 0, nil, false)
		}

		return err
	}

	switch e := ex.(type) {
	case *database.Tx:
		if err := q.check(m); err != nil {
			return err
		}

		e.AfterCommit(func(ctx context.Context) {
			// The work is committed: a cancelled request must not drop its job.
			if err := q.send(context.WithoutCancel(ctx), m, h); err != nil {
				q.log.ErrorContext(ctx, "job dispatched after commit failed", "job", m.Kind, "id", m.ID, "err", err)
			}
		})

		return nil
	case *database.DB:
		return q.send(ctx, m, h)
	default:
		return ErrNoTx
	}
}

// Message builds the stored message for j (drivers with their own
// transactional push use it).
func (q *Queue) Message(j Job, opts ...Option) (*Message, error) {
	m, _, err := q.message(j.Kind(), j, opts)
	return m, err
}

func (q *Queue) message(kind string, j any, opts []Option) (*Message, handler, error) {
	q.mu.RLock()
	h, ok := q.handlers[kind]
	q.mu.RUnlock()

	if !ok {
		return nil, handler{}, fmt.Errorf("%w: %q", ErrUnknownKind, kind)
	}

	o := h.opts
	for _, opt := range opts {
		opt(&o)
	}

	payload, err := json.Marshal(j)
	if err != nil {
		return nil, handler{}, fmt.Errorf("queue: encode %s: %w", kind, err)
	}

	now := q.clock().UTC()

	return &Message{
		ID: newID(), Kind: kind, Queue: o.queue, Payload: payload, MaxRetries: o.retries,
		Timeout: o.timeout, AvailableAt: now.Add(o.delay), CreatedAt: now,
	}, h, nil
}

// Process runs one reserved message and acks, releases (retry with
// backoff) or fails it.
func (q *Queue) Process(ctx context.Context, m *Message) error {
	return q.process(ctx, ctx, m, nil, 0)
}

// MaxFreeInterruptions is how many shutdowns in a row may interrupt a job
// without using one of its attempts (see WorkOptions.StopTimeout).
const MaxFreeInterruptions = 3

// interruptedPrefix marks LastError after a free interruption; the count
// lives there so every driver keeps it without a schema change.
const interruptedPrefix = "queue: interrupted by shutdown ("

// interruptions returns the free interruptions recorded in lastError.
func interruptions(lastError string) int {
	rest, ok := strings.CutPrefix(lastError, interruptedPrefix)
	if !ok {
		return 0
	}

	n, _, _ := strings.Cut(rest, "/")
	v, err := strconv.Atoi(n)
	if err != nil {
		return 0
	}

	return v
}

// process runs m with runCtx and settles it with ctx, each driver call
// bounded by settleTimeout (0 = no bound). interrupted reports whether the
// worker cancelled runCtx at shutdown: a failure caused by that cancel
// sends the job back without using an attempt, MaxFreeInterruptions times
// in a row.
func (q *Queue) process(runCtx, ctx context.Context, m *Message, interrupted func() bool,
	settleTimeout time.Duration,
) error {
	settle := func(fn func(context.Context, *Message) error) error {
		if settleTimeout <= 0 {
			return fn(ctx, m)
		}

		sctx, cancel := context.WithTimeout(ctx, settleTimeout)
		defer cancel()

		return fn(sctx, m)
	}

	q.mu.RLock()
	h, ok := q.handlers[m.Kind]
	q.mu.RUnlock()

	freeSoFar := interruptions(m.LastError)
	m.Attempts++

	var err error

	start := time.Now()

	if !ok {
		err = fmt.Errorf("%w: %q", ErrUnknownKind, m.Kind)
		m.MaxRetries = 0
	} else {
		err = q.attempt(runCtx, h, m, Attempt{Number: m.Attempts, MaxRetries: m.MaxRetries})
	}

	took := time.Since(start)

	if err == nil {
		q.observe(ctx, m, JobProcessed, took, nil, false)
		return settle(q.driver.Ack)
	}

	// ErrPermanent is the handler's own verdict that retrying cannot help:
	// it overrides both the shutdown grace and the remaining retries.
	permanent := errors.Is(err, ErrPermanent)
	cancelled := errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
	free := !permanent && cancelled && interrupted != nil && interrupted() && freeSoFar < MaxFreeInterruptions
	exhausted := m.Attempts > m.MaxRetries

	// A free interruption is not the job's last attempt even when its
	// number is already past the limit: the attempt is given back.
	q.observe(ctx, m, JobFailed, took, err, permanent || (!free && exhausted))

	m.LastError = err.Error()
	log := q.log.With("job", m.Kind, "id", m.ID, "attempt", m.Attempts, "err", err)

	if free {
		m.Attempts--
		m.AvailableAt = q.clock().UTC()
		m.LastError = fmt.Sprintf("%s%d/%d): %v", interruptedPrefix, freeSoFar+1, MaxFreeInterruptions, err)
		log.WarnContext(ctx, "job interrupted by shutdown: released", "interruptions", freeSoFar+1)

		return settle(q.driver.Release)
	}

	if permanent {
		log.ErrorContext(ctx, "job failed permanently: not retried")
		return settle(q.driver.Fail)
	}

	if exhausted {
		log.ErrorContext(ctx, "job failed")
		return settle(q.driver.Fail)
	}

	backoff := defaults().backoff
	if ok && h.opts.backoff != nil {
		backoff = h.opts.backoff
	}

	m.AvailableAt = q.clock().UTC().Add(backoff(m.Attempts))
	log.WarnContext(ctx, "job will be retried", "at", m.AvailableAt)

	return settle(q.driver.Release)
}

// attempt runs the handler with the message timeout and a, recovering
// panics.
func (q *Queue) attempt(ctx context.Context, h handler, m *Message, a Attempt) (err error) {
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = h.opts.timeout
	}

	ctx, cancel := context.WithTimeout(withAttempt(ctx, a), timeout)
	defer cancel()

	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
	}()

	return h.run(ctx, m.Payload)
}

func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// Sync runs jobs inline during Dispatch (development, tests, simple apps).
// Retries do not apply (the handler's error goes to the caller) and delayed
// jobs are refused with ErrInlineDelay; DispatchTx runs the job after the
// transaction commits.
type Sync struct{}

// Inline implements Inline.
func (Sync) Inline() bool { return true }

// Push implements Driver (never called for Sync).
func (Sync) Push(context.Context, *Message) error {
	return errors.New("queue: sync driver has no storage")
}

// Pop implements Driver.
func (Sync) Pop(context.Context, []string) (*Message, error) { return nil, nil }

// Ack implements Driver.
func (Sync) Ack(context.Context, *Message) error { return nil }

// Release implements Driver.
func (Sync) Release(context.Context, *Message) error { return nil }

// Fail implements Driver.
func (Sync) Fail(context.Context, *Message) error { return nil }
