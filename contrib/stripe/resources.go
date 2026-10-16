package stripe

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
)

// Customer is a Stripe customer.
type Customer struct {
	ID       string            `json:"id"`
	Email    string            `json:"email"`
	Name     string            `json:"name"`
	Metadata map[string]string `json:"metadata"`
	Deleted  bool              `json:"deleted"`
}

// CheckoutSession is a Checkout Session.
type CheckoutSession struct {
	ID            string            `json:"id"`
	URL           string            `json:"url"`
	Mode          string            `json:"mode"`
	Status        string            `json:"status"`
	PaymentStatus string            `json:"payment_status"`
	Customer      string            `json:"customer"`
	Subscription  string            `json:"subscription"`
	PaymentIntent string            `json:"payment_intent"`
	ClientRefID   string            `json:"client_reference_id"`
	AmountTotal   int64             `json:"amount_total"`
	Currency      string            `json:"currency"`
	Metadata      map[string]string `json:"metadata"`
}

// Subscription is a subscription.
type Subscription struct {
	ID                string            `json:"id"`
	Customer          string            `json:"customer"`
	Status            string            `json:"status"`
	CancelAtPeriodEnd bool              `json:"cancel_at_period_end"`
	Items             List[SubItem]     `json:"items"`
	Metadata          map[string]string `json:"metadata"`
}

// SubItem is a subscription item.
type SubItem struct {
	ID    string `json:"id"`
	Price struct {
		ID         string `json:"id"`
		LookupKey  string `json:"lookup_key"`
		Product    string `json:"product"`
		UnitAmount int64  `json:"unit_amount"`
	} `json:"price"`
	Quantity int64 `json:"quantity"`
	// CurrentPeriodEnd is a Unix time (moved to items in recent API versions).
	CurrentPeriodEnd int64 `json:"current_period_end"`
}

// PortalSession is a Billing Portal session.
type PortalSession struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

// PaymentIntent is a PaymentIntent.
type PaymentIntent struct {
	ID           string            `json:"id"`
	Amount       int64             `json:"amount"`
	Currency     string            `json:"currency"`
	Status       string            `json:"status"`
	ClientSecret string            `json:"client_secret"`
	Customer     string            `json:"customer"`
	Metadata     map[string]string `json:"metadata"`
}

// CreateCustomer creates a customer.
func (c *Client) CreateCustomer(ctx context.Context, p Params) (Customer, error) {
	return send[Customer](ctx, c, http.MethodPost, "/v1/customers", p, "")
}

// GetCustomer retrieves a customer.
func (c *Client) GetCustomer(ctx context.Context, id string) (Customer, error) {
	return send[Customer](ctx, c, http.MethodGet, "/v1/customers/"+url.PathEscape(id), nil, "")
}

// UpdateCustomer updates a customer.
func (c *Client) UpdateCustomer(ctx context.Context, id string, p Params) (Customer, error) {
	return send[Customer](ctx, c, http.MethodPost, "/v1/customers/"+url.PathEscape(id), p, "")
}

// CreateCheckoutSession creates a Checkout Session (mode "payment" or
// "subscription"). Do not pass payment_method_types: dynamic payment
// methods are configured in the Dashboard.
func (c *Client) CreateCheckoutSession(ctx context.Context, p Params) (CheckoutSession, error) {
	return send[CheckoutSession](ctx, c, http.MethodPost, "/v1/checkout/sessions", p, "")
}

// GetCheckoutSession retrieves a Checkout Session.
func (c *Client) GetCheckoutSession(ctx context.Context, id string) (CheckoutSession, error) {
	return send[CheckoutSession](ctx, c, http.MethodGet, "/v1/checkout/sessions/"+url.PathEscape(id), nil, "")
}

// GetSubscription retrieves a subscription.
func (c *Client) GetSubscription(ctx context.Context, id string) (Subscription, error) {
	return send[Subscription](ctx, c, http.MethodGet, "/v1/subscriptions/"+url.PathEscape(id), nil, "")
}

// UpdateSubscription updates a subscription (e.g. cancel_at_period_end).
func (c *Client) UpdateSubscription(ctx context.Context, id string, p Params) (Subscription, error) {
	return send[Subscription](ctx, c, http.MethodPost, "/v1/subscriptions/"+url.PathEscape(id), p, "")
}

// CancelSubscription cancels a subscription immediately.
func (c *Client) CancelSubscription(ctx context.Context, id string) (Subscription, error) {
	return send[Subscription](ctx, c, http.MethodDelete, "/v1/subscriptions/"+url.PathEscape(id), nil, "")
}

// CreatePortalSession creates a Billing Portal session for a customer.
func (c *Client) CreatePortalSession(ctx context.Context, customer, returnURL string) (PortalSession, error) {
	return send[PortalSession](
		ctx,
		c,
		http.MethodPost,
		"/v1/billing_portal/sessions",
		Params{"customer": customer, "return_url": returnURL},
		"",
	)
}

// CreatePaymentIntent creates a PaymentIntent; idemKey ties retries of the
// same business operation together (e.g. "order-42"), "" generates one.
func (c *Client) CreatePaymentIntent(ctx context.Context, p Params, idemKey string) (PaymentIntent, error) {
	return send[PaymentIntent](ctx, c, http.MethodPost, "/v1/payment_intents", p, idemKey)
}

// GetPaymentIntent retrieves a PaymentIntent.
func (c *Client) GetPaymentIntent(ctx context.Context, id string) (PaymentIntent, error) {
	return send[PaymentIntent](ctx, c, http.MethodGet, "/v1/payment_intents/"+url.PathEscape(id), nil, "")
}

// Do calls any endpoint not covered above and decodes the response into
// out (a struct or *json.RawMessage).
func (c *Client) Do(ctx context.Context, method, path string, p Params, out any) error {
	if out == nil {
		out = &json.RawMessage{}
	}

	return c.call(ctx, method, path, p, "", out)
}

// send calls path and decodes the response into a new T.
func send[T any](ctx context.Context, c *Client, method, path string, p Params, idemKey string) (T, error) {
	var out T
	err := c.call(ctx, method, path, p, idemKey, &out)

	return out, err
}
