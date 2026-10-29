package stripe

import (
	"context"
	"fmt"
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
	// Created and ExpiresAt are Unix times.
	Created   int64 `json:"created"`
	ExpiresAt int64 `json:"expires_at"`
}

// Subscription is a subscription.
type Subscription struct {
	ID                string            `json:"id"`
	Customer          string            `json:"customer"`
	Status            string            `json:"status"`
	CancelAtPeriodEnd bool              `json:"cancel_at_period_end"`
	Items             List[SubItem]     `json:"items"`
	Metadata          map[string]string `json:"metadata"`
	// Created is a Unix time; the newest of a customer's subscriptions has
	// the largest one.
	Created int64 `json:"created"`
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

// PaymentIntent is a PaymentIntent. For a destination charge pass
// application_fee_amount and transfer_data[destination] (and on_behalf_of
// when the connected account is the merchant of record).
type PaymentIntent struct {
	ID           string            `json:"id"`
	Amount       int64             `json:"amount"`
	Currency     string            `json:"currency"`
	Status       string            `json:"status"`
	ClientSecret string            `json:"client_secret"`
	Customer     string            `json:"customer"`
	Metadata     map[string]string `json:"metadata"`
	// ApplicationFeeAmount is the platform's fee (Connect), in the smallest
	// currency unit.
	ApplicationFeeAmount int64  `json:"application_fee_amount"`
	OnBehalfOf           string `json:"on_behalf_of"`
	TransferData         struct {
		Destination string `json:"destination"`
		Amount      int64  `json:"amount"`
	} `json:"transfer_data"`
	TransferGroup string `json:"transfer_group"`
	LatestCharge  string `json:"latest_charge"`
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

// ListCheckoutSessions lists Checkout Sessions, newest first, filtered by
// p (customer, status "open"/"complete"/"expired", limit, starting_after).
// Expire a customer's open sessions before creating a new one so a second
// tab cannot start a second subscription.
func (c *Client) ListCheckoutSessions(ctx context.Context, p Params) (List[CheckoutSession], error) {
	return send[List[CheckoutSession]](ctx, c, http.MethodGet, "/v1/checkout/sessions", p, "")
}

// ExpireCheckoutSession expires an open Checkout Session (cs_ id) so the
// customer can no longer complete it.
func (c *Client) ExpireCheckoutSession(ctx context.Context, id string) (CheckoutSession, error) {
	// Checkout Session ids carry the mode: cs_test_… or cs_live_….
	if !validID(id, "cs_test_", "cs_live_", "cs_") {
		return CheckoutSession{}, fmt.Errorf("%w: checkout session id must be a cs_ id", ErrInvalidRequestOption)
	}

	return send[CheckoutSession](
		ctx,
		c,
		http.MethodPost,
		"/v1/checkout/sessions/"+url.PathEscape(id)+"/expire",
		nil,
		"",
	)
}

// ListSubscriptions lists subscriptions, newest first, filtered by p
// (customer, price, status; Stripe omits canceled ones unless status is
// "canceled" or "all"; limit, starting_after). Page with starting_after
// set to the last ID while HasMore is true.
func (c *Client) ListSubscriptions(ctx context.Context, p Params) (List[Subscription], error) {
	return send[List[Subscription]](ctx, c, http.MethodGet, "/v1/subscriptions", p, "")
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

// send calls path and decodes the response into a new T.
func send[T any](ctx context.Context, c *Client, method, path string, p Params, idemKey string) (T, error) {
	return sendOpts[T](ctx, c, method, path, p, IdempotencyKey(idemKey))
}

// sendOpts is send with request options.
func sendOpts[T any](ctx context.Context, c *Client, method, path string, p Params, opts ...RequestOption) (T, error) {
	var out T
	err := c.call(ctx, method, path, p, &out, opts...)

	return out, err
}
