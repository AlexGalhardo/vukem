package stripe

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Accounts v2 (https://docs.stripe.com/connect/accounts-v2) is how Stripe
// asks platforms to represent connected accounts: one Account with
// merchant, customer and recipient configurations. Requests go to /v2/core
// with JSON bodies; the v2 account id is an acct_ id that v1 endpoints
// (transfers, Stripe-Account) accept. These methods act on the platform
// account, like the v1 Connect methods.

// AccountV2 is an Accounts v2 connected account. Configuration,
// Requirements and Identity data are only returned for the paths listed in
// include (e.g. "configuration.recipient", "requirements"); a nil field was
// not included.
type AccountV2 struct {
	ID                    string                 `json:"id"`
	Object                string                 `json:"object"`
	AppliedConfigurations []string               `json:"applied_configurations"`
	Closed                bool                   `json:"closed"`
	ContactEmail          string                 `json:"contact_email"`
	DisplayName           string                 `json:"display_name"`
	Dashboard             string                 `json:"dashboard"`
	Created               time.Time              `json:"created"`
	Livemode              bool                   `json:"livemode"`
	Metadata              map[string]string      `json:"metadata"`
	Configuration         AccountV2Configuration `json:"configuration"`
	Requirements          *AccountV2Requirements `json:"requirements"`
}

// AccountV2Configuration holds the configurations an account was given and
// that the request included.
type AccountV2Configuration struct {
	Merchant  *AccountV2Merchant  `json:"merchant"`
	Recipient *AccountV2Recipient `json:"recipient"`
}

// AccountV2Merchant is the merchant configuration (accepting payments).
type AccountV2Merchant struct {
	Capabilities AccountV2MerchantCapabilities `json:"capabilities"`
}

// AccountV2MerchantCapabilities are the merchant capabilities the platform
// reads most.
type AccountV2MerchantCapabilities struct {
	CardPayments  CapabilityV2             `json:"card_payments"`
	StripeBalance AccountV2MerchantBalance `json:"stripe_balance"`
}

// AccountV2MerchantBalance holds the merchant's balance capabilities.
type AccountV2MerchantBalance struct {
	Payouts CapabilityV2 `json:"payouts"`
}

// AccountV2Recipient is the recipient configuration (receiving transfers).
type AccountV2Recipient struct {
	Capabilities AccountV2RecipientCapabilities `json:"capabilities"`
}

// AccountV2RecipientCapabilities are the recipient capabilities.
type AccountV2RecipientCapabilities struct {
	StripeBalance AccountV2RecipientBalance `json:"stripe_balance"`
}

// AccountV2RecipientBalance holds the recipient's balance capabilities:
// StripeTransfers (v1 "transfers") must be "active" before /v1/transfers
// can pay the account.
type AccountV2RecipientBalance struct {
	StripeTransfers CapabilityV2 `json:"stripe_transfers"`
	Payouts         CapabilityV2 `json:"payouts"`
}

// CapabilityV2 is a v2 capability: Status is "active", "pending",
// "restricted", "rejected" or "unsupported" ("" when not requested).
type CapabilityV2 struct {
	Status        string                     `json:"status"`
	StatusDetails []CapabilityV2StatusDetail `json:"status_details"`
}

// CapabilityV2StatusDetail says why a capability is not active and what
// resolves it ("provide_info", "contact_stripe", "no_resolution").
type CapabilityV2StatusDetail struct {
	Code       string `json:"code"`
	Resolution string `json:"resolution"`
}

// AccountV2Requirements lists what Stripe still needs from the account.
type AccountV2Requirements struct {
	Entries []AccountV2Requirement       `json:"entries"`
	Summary AccountV2RequirementsSummary `json:"summary"`
}

// AccountV2Requirement is one requirement; AwaitingActionFrom is "user"
// (the platform must collect it) or "stripe".
type AccountV2Requirement struct {
	Description        string            `json:"description"`
	AwaitingActionFrom string            `json:"awaiting_action_from"`
	MinimumDeadline    AccountV2Deadline `json:"minimum_deadline"`
}

// AccountV2RequirementsSummary is the strictest deadline across the
// requirements (nil when nothing is due).
type AccountV2RequirementsSummary struct {
	MinimumDeadline *AccountV2Deadline `json:"minimum_deadline"`
}

// AccountV2Deadline is a requirement deadline: Status is "currently_due",
// "eventually_due" or "past_due".
type AccountV2Deadline struct {
	Status string     `json:"status"`
	Time   *time.Time `json:"time,omitempty"`
}

// AccountLinkV2 is a single-use, short-lived Accounts v2 onboarding (or
// update) URL.
type AccountLinkV2 struct {
	Object    string    `json:"object"`
	Account   string    `json:"account"`
	URL       string    `json:"url"`
	Created   time.Time `json:"created"`
	ExpiresAt time.Time `json:"expires_at"`
	Livemode  bool      `json:"livemode"`
}

// CreateAccountV2 creates an Accounts v2 connected account (POST
// /v2/core/accounts). For an Express-style seller pass dashboard=express,
// defaults[responsibilities] fees_collector and losses_collector =
// application, identity[country], the configurations it needs (e.g.
// configuration[recipient][capabilities][stripe_balance][stripe_transfers][requested]=true
// for separate charges and transfers) and include for the parts of the
// response you read. Pass an IdempotencyKey tied to the seller so a
// retried signup never creates two accounts.
func (c *Client) CreateAccountV2(ctx context.Context, p Params, opts ...RequestOption) (AccountV2, error) {
	return sendOpts[AccountV2](ctx, c, http.MethodPost, "/v2/core/accounts", p, withPlatformOnly(opts)...)
}

// GetAccountV2 retrieves an Accounts v2 account with the include paths
// (e.g. "configuration.recipient", "requirements"); check the recipient's
// StripeTransfers status after onboarding returns.
func (c *Client) GetAccountV2(
	ctx context.Context,
	id string,
	include []string,
	opts ...RequestOption,
) (AccountV2, error) {
	if !validAccountID(id) {
		return AccountV2{}, fmt.Errorf("%w: account id must be an acct_ id", ErrInvalidRequestOption)
	}

	var p Params
	if len(include) > 0 {
		p = Params{"include": include}
	}

	return sendOpts[AccountV2](ctx, c, http.MethodGet, "/v2/core/accounts/"+url.PathEscape(id), p,
		withPlatformOnly(opts)...)
}

// UpdateAccountV2 updates an Accounts v2 account (POST
// /v2/core/accounts/{id}); a nil metadata value removes that key.
func (c *Client) UpdateAccountV2(ctx context.Context, id string, p Params, opts ...RequestOption) (AccountV2, error) {
	if !validAccountID(id) {
		return AccountV2{}, fmt.Errorf("%w: account id must be an acct_ id", ErrInvalidRequestOption)
	}

	return sendOpts[AccountV2](ctx, c, http.MethodPost, "/v2/core/accounts/"+url.PathEscape(id), p,
		withPlatformOnly(opts)...)
}

// CreateAccountLinkV2 creates an Accounts v2 onboarding link (POST
// /v2/core/account_links): account and use_case with type
// "account_onboarding" (or "account_update") and the matching object
// holding refresh_url, return_url and configurations (e.g. ["merchant",
// "recipient"]): required on the pinned APIVersion, removed by Stripe in
// 2026-09-30.endive. Each link is single-use: refresh_url should create a
// new one, and the URL goes only to the signed-in account holder.
func (c *Client) CreateAccountLinkV2(ctx context.Context, p Params, opts ...RequestOption) (AccountLinkV2, error) {
	return sendOpts[AccountLinkV2](ctx, c, http.MethodPost, "/v2/core/account_links", p, withPlatformOnly(opts)...)
}
