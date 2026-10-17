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

// Connect Express: onboard a seller, then charge with a platform fee.
// (No Output line: examples that call Stripe are compiled, not run.)
func Example_connect() {
	ctx := context.Background()
	client, _ := stripe.New("rk_test_xxxxxxxxxxxxxxxx")

	acct, _ := client.CreateAccount(ctx, stripe.Params{
		"controller": stripe.Params{
			"stripe_dashboard": stripe.Params{"type": "express"},
			"fees":             stripe.Params{"payer": "application"},
			"losses":           stripe.Params{"payments": "application"},
		},
		"capabilities": stripe.Params{"transfers": stripe.Params{"requested": true}},
	}, stripe.IdempotencyKey("seller-7-account"))

	link, _ := client.CreateAccountLink(ctx, stripe.Params{
		"account": acct.ID, "type": "account_onboarding",
		"refresh_url": "https://app.example.com/connect/refresh",
		"return_url":  "https://app.example.com/connect/return",
	})
	fmt.Println(link.URL) // redirect the signed-in seller here

	// Destination charge: the seller receives 10.00 minus the 1.00 fee.
	_, _ = client.CreatePaymentIntent(ctx, stripe.Params{
		"amount": 1000, "currency": "usd", "application_fee_amount": 100,
		"transfer_data": stripe.Params{"destination": acct.ID},
	}, "order-42")

	// Reads on the connected account itself send Stripe-Account.
	_, _ = client.ForAccount(acct.ID).GetPaymentIntent(ctx, "pi_123")
}
