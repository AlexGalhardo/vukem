package mail

import (
	"context"
	"errors"

	"github.com/AlexGalhardo/vukem/queue"
)

// Enqueue stores a rendered message in a queue job that sends it later.
// The app's mailer (app.Mailer) enqueues on the app queue with the payload
// encrypted, so mail bodies (reset links, tokens) never sit in the jobs
// tables in clear text.
type Enqueue func(ctx context.Context, msg *Message, opts ...queue.Option) error

// ErrNoQueue is returned by SendLater on a mailer without an Enqueue.
var ErrNoQueue = errors.New("mail: SendLater needs a queue (use app.Mailer or Mailer.SetEnqueue)")

// SetEnqueue sets how SendLater queues messages. Call it before the mailer
// is used; app.Mailer sets it.
func (m *Mailer) SetEnqueue(fn Enqueue) { m.enqueue = &fn }

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
