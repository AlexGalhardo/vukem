package security_test

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/AlexGalhardo/vukem/events"
	"github.com/AlexGalhardo/vukem/security"
)

func Example() {
	bus := events.New(slog.New(slog.DiscardHandler), 0)
	events.Listen(bus, func(_ context.Context, e security.Event) error {
		if e.Name == "auth.login_failed" {
			fmt.Println("alert:", e.Name, e.IP)
		}
		return nil
	})

	// The framework dispatches these; apps only listen.
	err := bus.Dispatch(context.Background(), security.Event{
		Name: "auth.login_failed", Method: "POST", Path: "/login", IP: "203.0.113.7",
	})
	fmt.Println(err)
	// Output:
	// alert: auth.login_failed 203.0.113.7
	// <nil>
}
