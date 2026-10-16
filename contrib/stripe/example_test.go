package stripe_test

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/contrib/stripe"
	"github.com/AlexGalhardo/vukem/events"
)

func Example() {
	client, _ := stripe.New("rk_test_xxxxxxxxxxxxxxxx") // a restricted key

	// Checkout for a subscription; fulfil from the webhook, not the success page.
	_, _ = client.CreateCheckoutSession(context.Background(), stripe.Params{
		"mode": "subscription", "customer": "cus_123",
		"line_items":  []stripe.Params{{"price": "price_pro", "quantity": 1}},
		"success_url": "https://app.example.com/billing?done=1",
	})

	bus := events.New(slog.Default(), 1)
	events.Listen(bus, func(_ context.Context, r stripe.Received) error {
		if r.Event.Type == "checkout.session.completed" {
			cs, err := stripe.Decode[stripe.CheckoutSession](r.Event)
			fmt.Println(cs.ID, err)
		}

		return nil
	})

	_, _ = stripe.WebhookHandler(stripe.WebhookConfig{Secret: "whsec_...", Store: cache.NewMemory(1000), Bus: bus})
	// r.Handle("POST /stripe/webhook", handler) outside CSRF protection.
	// Output:
}
