package stripe

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Refund returns part or all of a charge to the buyer.
type Refund struct {
	ID            string `json:"id"`
	Amount        int64  `json:"amount"`
	Currency      string `json:"currency"`
	Charge        string `json:"charge"`
	PaymentIntent string `json:"payment_intent"`
	// Reason is "duplicate", "fraudulent", "requested_by_customer" or a
	// Stripe-set reason.
	Reason string `json:"reason"`
	// Status is "pending", "requires_action", "succeeded", "failed" or
	// "canceled"; listen for refund.updated/refund.failed for changes.
	Status        string            `json:"status"`
	FailureReason string            `json:"failure_reason"`
	Created       int64             `json:"created"`
	Metadata      map[string]string `json:"metadata"`
	// TransferReversal is set when reverse_transfer=true reversed the
	// transfer of a destination charge; SourceTransferReversal when the
	// refunded charge was paid by a transfer.
	TransferReversal       string `json:"transfer_reversal"`
	SourceTransferReversal string `json:"source_transfer_reversal"`
}

// TransferReversal pulls funds of a transfer back from the connected
// account to the platform.
type TransferReversal struct {
	ID                       string            `json:"id"`
	Amount                   int64             `json:"amount"`
	Currency                 string            `json:"currency"`
	Transfer                 string            `json:"transfer"`
	DestinationPaymentRefund string            `json:"destination_payment_refund"`
	SourceRefund             string            `json:"source_refund"`
	Created                  int64             `json:"created"`
	Metadata                 map[string]string `json:"metadata"`
}

// CreateRefund refunds a payment: payment_intent (or charge), amount
// (omit for the rest of it), reason and metadata; for a destination charge
// add reverse_transfer and refund_application_fee. Pass an IdempotencyKey
// tied to the business operation (e.g. "refund-order-42") so a retried job
// never refunds twice. A direct charge is refunded on the connected
// account: use ForAccount or OnAccount.
func (c *Client) CreateRefund(ctx context.Context, p Params, opts ...RequestOption) (Refund, error) {
	return sendOpts[Refund](ctx, c, http.MethodPost, "/v1/refunds", p, opts...)
}

// GetRefund retrieves a refund.
func (c *Client) GetRefund(ctx context.Context, id string, opts ...RequestOption) (Refund, error) {
	if id == "" {
		return Refund{}, fmt.Errorf("%w: empty refund id", ErrInvalidRequestOption)
	}

	return sendOpts[Refund](ctx, c, http.MethodGet, "/v1/refunds/"+url.PathEscape(id), nil, opts...)
}

// CreateTransferReversal reverses a transfer (all of it, or amount), for
// refunds of separate charges and transfers; refund_application_fee and
// metadata are optional. Pass an IdempotencyKey tied to the business
// operation so a retried job never reverses twice. It acts on the platform
// account.
func (c *Client) CreateTransferReversal(
	ctx context.Context,
	transfer string,
	p Params,
	opts ...RequestOption,
) (TransferReversal, error) {
	if transfer == "" {
		return TransferReversal{}, fmt.Errorf("%w: empty transfer id", ErrInvalidRequestOption)
	}

	return sendOpts[TransferReversal](ctx, c, http.MethodPost,
		"/v1/transfers/"+url.PathEscape(transfer)+"/reversals", p, withPlatformOnly(opts)...)
}

// GetTransferReversal retrieves reversal id of transfer. It acts on the
// platform account.
func (c *Client) GetTransferReversal(
	ctx context.Context,
	transfer, id string,
	opts ...RequestOption,
) (TransferReversal, error) {
	if transfer == "" || id == "" {
		return TransferReversal{}, fmt.Errorf("%w: empty transfer or reversal id", ErrInvalidRequestOption)
	}

	return sendOpts[TransferReversal](ctx, c, http.MethodGet,
		"/v1/transfers/"+url.PathEscape(transfer)+"/reversals/"+url.PathEscape(id), nil, withPlatformOnly(opts)...)
}

// UnmarshalJSON decodes expanded charge, payment_intent, transfer_reversal
// and source_transfer_reversal as their ids.
func (r *Refund) UnmarshalJSON(b []byte) error {
	type plain Refund

	return decodeCollapsed(b, (*plain)(r), "charge", "payment_intent", "transfer_reversal", "source_transfer_reversal")
}

// UnmarshalJSON decodes expanded transfer, destination_payment_refund and
// source_refund as their ids.
func (t *TransferReversal) UnmarshalJSON(b []byte) error {
	type plain TransferReversal

	return decodeCollapsed(b, (*plain)(t), "transfer", "destination_payment_refund", "source_refund")
}
