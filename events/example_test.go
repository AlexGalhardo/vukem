package events_test

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/AlexGalhardo/vukem/events"
)

type UserRegistered struct{ Email string }

func Example() {
	ctx := context.Background()
	bus := events.New(slog.New(slog.DiscardHandler), 4)
	defer func() { _ = bus.Shutdown(ctx) }()

	bus.Register(events.On(func(_ context.Context, e UserRegistered) error {
		fmt.Println("welcome mail to", e.Email)
		return nil
	}))

	err := bus.Dispatch(ctx, UserRegistered{Email: "ada@example.com"})
	fmt.Println(err)
	// Output:
	// welcome mail to ada@example.com
	// <nil>
}

func ExampleFake() {
	fake := events.NewFake()

	_ = fake.Dispatch(context.Background(), UserRegistered{Email: "ada@example.com"})

	for _, e := range events.Dispatched[UserRegistered](fake) {
		fmt.Println("dispatched", e.Email)
	}
	// Output: dispatched ada@example.com
}
