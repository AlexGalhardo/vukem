package stripe

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
)

// Account is a Connect connected account (v1 Accounts API).
type Account struct {
	ID               string            `json:"id"`
	Type             string            `json:"type"`
	Email            string            `json:"email"`
	Country          string            `json:"country"`
	DefaultCurrency  string            `json:"default_currency"`
	ChargesEnabled   bool              `json:"charges_enabled"`
	PayoutsEnabled   bool              `json:"payouts_enabled"`
	DetailsSubmitted bool              `json:"details_submitted"`
	Requirements     Requirements      `json:"requirements"`
	Metadata         map[string]string `json:"metadata"`
}

// Requirements lists what Stripe still needs from a connected account.
type Requirements struct {
	CurrentlyDue   []string `json:"currently_due"`
	EventuallyDue  []string `json:"eventually_due"`
	PastDue        []string `json:"past_due"`
	DisabledReason string   `json:"disabled_reason"`
}

// AccountLink is a single-use, short-lived onboarding (or update) URL.
type AccountLink struct {
	URL       string `json:"url"`
	Created   int64  `json:"created"`
	ExpiresAt int64  `json:"expires_at"`
}

// LoginLink is a single-use URL to an Express account's dashboard.
type LoginLink struct {
	URL     string `json:"url"`
	Created int64  `json:"created"`
}

// Transfer moves funds from the platform balance to a connected account.
type Transfer struct {
	ID                 string            `json:"id"`
	Amount             int64             `json:"amount"`
	AmountReversed     int64             `json:"amount_reversed"`
	Currency           string            `json:"currency"`
	Destination        string            `json:"destination"`
	DestinationPayment string            `json:"destination_payment"`
	SourceTransaction  string            `json:"source_transaction"`
	TransferGroup      string            `json:"transfer_group"`
	Reversed           bool              `json:"reversed"`
	Metadata           map[string]string `json:"metadata"`
}

// CreateAccount creates a connected account. For an Express-style account
// pass controller[stripe_dashboard][type]=express (with
// controller[fees][payer] and controller[losses][payments] = application),
// or the legacy type=express; add capabilities[transfers][requested]=true
// and any data you already know.
func (c *Client) CreateAccount(ctx context.Context, p Params, opts ...RequestOption) (Account, error) {
	return sendOpts[Account](ctx, c, http.MethodPost, "/v1/accounts", p, opts...)
}

// GetAccount retrieves a connected account; check ChargesEnabled and
// DetailsSubmitted after onboarding returns, or listen for account.updated.
func (c *Client) GetAccount(ctx context.Context, id string, opts ...RequestOption) (Account, error) {
	if !validAccountID(id) {
		return Account{}, fmt.Errorf("%w: account id must be an acct_ id", ErrInvalidRequestOption)
	}

	return sendOpts[Account](ctx, c, http.MethodGet, "/v1/accounts/"+url.PathEscape(id), nil, opts...)
}

// CreateAccountLink creates an onboarding link: account, type
// ("account_onboarding" or "account_update"), refresh_url and return_url.
// Hand the URL only to the authenticated account holder inside the app;
// refresh_url should create a new link, since each one is single-use.
func (c *Client) CreateAccountLink(ctx context.Context, p Params, opts ...RequestOption) (AccountLink, error) {
	return sendOpts[AccountLink](ctx, c, http.MethodPost, "/v1/account_links", p, opts...)
}

// CreateLoginLink creates a single-use link to an Express account's
// dashboard.
func (c *Client) CreateLoginLink(ctx context.Context, account string, opts ...RequestOption) (LoginLink, error) {
	if !validAccountID(account) {
		return LoginLink{}, fmt.Errorf("%w: account id must be an acct_ id", ErrInvalidRequestOption)
	}

	return sendOpts[LoginLink](
		ctx, c, http.MethodPost, "/v1/accounts/"+url.PathEscape(account)+"/login_links", nil, opts...,
	)
}

// CreateTransfer sends amount/currency to destination (an acct_ id), for
// separate charges and transfers; set transfer_group and, to transfer
// funds before they settle, source_transaction. Pass an IdempotencyKey
// tied to the business operation so a retried job never pays twice.
func (c *Client) CreateTransfer(ctx context.Context, p Params, opts ...RequestOption) (Transfer, error) {
	return sendOpts[Transfer](ctx, c, http.MethodPost, "/v1/transfers", p, opts...)
}

// GetTransfer retrieves a transfer.
func (c *Client) GetTransfer(ctx context.Context, id string, opts ...RequestOption) (Transfer, error) {
	return sendOpts[Transfer](ctx, c, http.MethodGet, "/v1/transfers/"+url.PathEscape(id), nil, opts...)
}
