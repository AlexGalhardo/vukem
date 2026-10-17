package mail

import (
	"context"
	"errors"

	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/queue"
)

// Enqueue stores a rendered message in a queue job that sends it later.
// The app's mailer (app.Mailer) enqueues on the app queue with the payload
// encrypted, so mail bodies (reset links, tokens) never sit in the jobs
// tables in clear text.
type Enqueue func(ctx context.Context, msg *Message, opts ...queue.Option) error

// EnqueueTx is Enqueue tied to the transaction of ex (Queue.DispatchTx):
// the mail is queued only if the transaction commits.
type EnqueueTx func(ctx context.Context, ex database.Executor, msg *Message, opts ...queue.Option) error

// ErrNoQueue is returned by SendLater (SendLaterTx) on a mailer without an
// Enqueue (EnqueueTx).
var ErrNoQueue = errors.New("mail: SendLater needs a queue (use app.Mailer or Mailer.SetEnqueue)")

// SetEnqueue sets how SendLater queues messages. Call it before the mailer
// is used; app.Mailer sets it.
func (m *Mailer) SetEnqueue(fn Enqueue) { m.enqueue = &fn }

// SetEnqueueTx sets how SendLaterTx queues messages. Call it before the
// mailer is used; app.Mailer sets it.
func (m *Mailer) SetEnqueueTx(fn EnqueueTx) { m.enqueueTx = &fn }

// SendLater renders mailable now (so rendering errors reach the caller)
// and sends it from a queue job, retried with the queue's backoff. The
// message ID stays the same across retries, so providers that deduplicate
// by it send once. opts select the queue, delay or retries of this mail.
// With QUEUE_CONNECTION=sync the job runs during the call.
func (m *Mailer) SendLater(ctx context.Context, mailable Mailable, opts ...queue.Option) error {
	if m.enqueue == nil || *m.enqueue == nil {
		return ErrNoQueue
	}

	msg, err := m.Render(ctx, mailable)
	if err != nil {
		return err
	}

	return (*m.enqueue)(ctx, msg, opts...)
}

// SendLaterTx is SendLater inside the transaction of ex: the mail is queued
// only if the transaction commits, so rolled-back work (a failed payment)
// never emails anyone. Rendering errors are returned at once. On the
// database queue the job is inserted with the transaction; on other
// drivers it is queued right after the commit (with QUEUE_CONNECTION=sync
// the mail is sent then, and a send error is logged, not returned).
func (m *Mailer) SendLaterTx(ctx context.Context, ex database.Executor, mailable Mailable, opts ...queue.Option) error {
	if m.enqueueTx == nil || *m.enqueueTx == nil {
		return ErrNoQueue
	}

	msg, err := m.Render(ctx, mailable)
	if err != nil {
		return err
	}

	return (*m.enqueueTx)(ctx, ex, msg, opts...)
}
