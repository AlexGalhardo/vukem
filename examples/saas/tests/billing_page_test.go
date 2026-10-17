package tests

import (
	"net/http"
	"testing"
)

func TestBillingPage(t *testing.T) {
	t.Parallel()

	f := newFakeStripe(t)
	app := billingApp(t, f)
	u := newUser(t, app, "page@example.com")
	c := app.Client(t).ActingAs("web", u)

	c.Get("/billing").AssertInertia("Billing").
		HasProp("plans", 2).
		PropEquals("plans.1.name", "pro").
		PropEquals("subscribed", false).
		PropEquals("subscription", nil)

	setCustomer(t, app, u, "cus_1")
	f.setSubscription("sub_1", "cus_1", "active", "price_pro")
	postWebhook(t, app, "evt_1", "customer.subscription.created", map[string]any{"id": "sub_1"}).AssertOK()

	c.Get("/billing").AssertInertia("Billing").
		PropEquals("subscribed", true).
		PropEquals("subscription.plan", "Pro").
		PropEquals("subscription.status", "active").
		PropEquals("plans.1.current", true)

	// Inertia visits leave for Stripe with a full page load.
	c.Inertia("").PostJSON("/billing/portal", nil).
		AssertStatus(http.StatusConflict).
		AssertHeader("X-Inertia-Location", "https://billing.stripe.com/p/session/test_1")
}
