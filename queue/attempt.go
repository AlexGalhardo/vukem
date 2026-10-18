package queue

import (
	"context"
	"errors"
)

// ErrPermanent marks a failure that retrying cannot fix (a corrupt
// payload, a deleted row, a rejected input). A handler wraps it in its own
// error:
//
//	if errors.Is(err, image.ErrFormat) {
//		return fmt.Errorf("convert %d: %w: %w", j.ID, err, queue.ErrPermanent)
//	}
//
// The job skips its remaining retries and moves to the failed jobs with
// that error, so it stays visible to queue:failed and queue:retry instead
// of disappearing. With an Inline driver (Sync) there are no retries to
// skip and the error reaches the caller unchanged.
var ErrPermanent = errors.New("queue: permanent failure, will not be retried")

// Attempt is what a handler knows about the attempt it is running; read it
// with AttemptFrom.
type Attempt struct {
	// Number is the 1-based attempt number (1 on the first run).
	Number int
	// MaxRetries is how many retries the job was dispatched with, so the
	// job gets at most MaxRetries+1 attempts. It is 0 with an Inline
	// driver (Sync), which does not retry.
	MaxRetries int
}

// Last reports whether this attempt is the job's last, so a handler can
// settle the work it owns (mark a row failed, notify) instead of leaving
// it mid-flight when its retries run out. A handler that already knows
// retrying is pointless returns ErrPermanent rather than waiting for the
// last attempt.
func (a Attempt) Last() bool { return a.Number > a.MaxRetries }

// attemptKey keys the Attempt of the running job in a handler's context.
type attemptKey struct{}

// AttemptFrom returns the attempt the handler is running on, and whether
// there is one: it is false outside a queue handler (a handler function
// called directly, say from a test), where a zero Attempt would read as a
// last attempt.
func AttemptFrom(ctx context.Context) (Attempt, bool) {
	a, ok := ctx.Value(attemptKey{}).(Attempt)
	return a, ok
}

// withAttempt puts a in ctx for the handler.
func withAttempt(ctx context.Context, a Attempt) context.Context {
	return context.WithValue(ctx, attemptKey{}, a)
}
