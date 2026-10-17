package queue_test

import (
	"context"
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
