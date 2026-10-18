package queue_test

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/AlexGalhardo/vukem/queue"
)

type SendInvoice struct{ OrderID int64 }

func (SendInvoice) Kind() string { return "send_invoice" }

func Example() {
	ctx := context.Background()

	// Sync runs jobs inline; apps use the database or Redis driver and a
	// worker (vukem queue:work).
	q := queue.New(queue.Sync{}, slog.New(slog.DiscardHandler))

	err := q.Register(queue.Handle(func(_ context.Context, j SendInvoice) error {
		fmt.Println("sending invoice for order", j.OrderID)
		return nil
	}, queue.Retries(5), queue.Backoff(queue.Exponential(time.Second))))
	if err != nil {
		fmt.Println(err)
		return
	}

	err = q.Dispatch(ctx, SendInvoice{OrderID: 1}, queue.OnQueue("billing"))
	fmt.Println(err)
	// Output:
	// sending invoice for order 1
	// <nil>
}

// A handler that cannot succeed reports a permanent failure instead of
// being retried, and reads its attempt number from the context to settle
// the work it owns before its retries run out.
func ExampleErrPermanent() {
	ctx := context.Background()
	q := queue.New(queue.Sync{}, slog.New(slog.DiscardHandler))

	err := q.Register(queue.Handle(func(ctx context.Context, j SendInvoice) error {
		a, _ := queue.AttemptFrom(ctx)
		fmt.Printf("attempt %d of %d (last: %v)\n", a.Number, a.MaxRetries+1, a.Last())

		if j.OrderID == 0 {
			return fmt.Errorf("order %d does not exist: %w", j.OrderID, queue.ErrPermanent)
		}

		return nil
	}, queue.Retries(5)))
	if err != nil {
		fmt.Println(err)
		return
	}

	// Sync runs the job once, so its single attempt is the last one.
	fmt.Println(errors.Is(q.Dispatch(ctx, SendInvoice{}), queue.ErrPermanent))
	// Output:
	// attempt 1 of 1 (last: true)
	// true
}

func ExampleFake() {
	ctx := context.Background()
	fake := queue.NewFake()
	q := queue.New(fake, slog.New(slog.DiscardHandler))

	// Dispatch only accepts registered kinds; the Fake stores the job
	// instead of running it.
	_ = q.Register(queue.Handle(func(context.Context, SendInvoice) error { return nil }))
	_ = q.Dispatch(ctx, SendInvoice{OrderID: 7})

	for _, j := range queue.Dispatched[SendInvoice](fake) {
		fmt.Println("dispatched order", j.OrderID)
	}
	// Output: dispatched order 7
}
