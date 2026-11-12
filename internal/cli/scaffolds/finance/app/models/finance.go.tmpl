package models

import (
	"database/sql"

	"github.com/AlexGalhardo/vukem/orm"
)

// Roles of a workspace member. The owner pays, the admin manages people
// and data, the member records transactions.
const (
	RoleOwner  = "owner"
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// Workspace is the tenant: it owns every account, transaction and budget,
// and it is what Stripe bills. Money is int64 cents in Currency (ISO 4217).
type Workspace struct {
	orm.Model

	Name             string           `json:"name"`
	Currency         string           `json:"currency"`
	StripeCustomerID sql.Null[string] `json:"-"`
}

// Membership puts a user in a workspace with a role.
type Membership struct {
	orm.Model

	WorkspaceID int64  `json:"workspace_id"`
	UserID      int64  `json:"user_id"`
	Role        string `json:"role"`
}

// Invite is a pending invitation by email; only the SHA-256 of its token is stored.
type Invite struct {
	orm.Model

	WorkspaceID int64        `json:"workspace_id"`
	Email       string       `json:"email"`
	Role        string       `json:"role"`
	TokenHash   string       `json:"-"`
	ExpiresAt   orm.NullTime `json:"expires_at"`
	AcceptedAt  orm.NullTime `json:"accepted_at"`
}

// Account is a place money sits: a bank account, a card, cash.
type Account struct {
	orm.Model

	WorkspaceID  int64  `json:"workspace_id"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	OpeningCents int64  `json:"opening_cents"`
}

// Category groups transactions of one kind ("income" or "expense").
type Category struct {
	orm.Model

	WorkspaceID int64  `json:"workspace_id"`
	Name        string `json:"name"`
	Kind        string `json:"kind"`
}

// Transaction kinds.
const (
	KindIncome  = "income"
	KindExpense = "expense"
)

// Transaction is one movement. AmountCents is always positive: Kind says
// whether it adds or subtracts. OccurredOn is an ISO date, "2026-11-12".
type Transaction struct {
	orm.Model

	WorkspaceID int64           `json:"workspace_id"`
	AccountID   int64           `json:"account_id"`
	CategoryID  sql.Null[int64] `json:"category_id"`
	Kind        string          `json:"kind"`
	AmountCents int64           `json:"amount_cents"`
	Description string          `json:"description"`
	OccurredOn  string          `json:"occurred_on"`
	ImportID    sql.Null[int64] `json:"-"`
}

// Budget is the monthly spending limit of one expense category.
type Budget struct {
	orm.Model

	WorkspaceID int64 `json:"workspace_id"`
	CategoryID  int64 `json:"category_id"`
	LimitCents  int64 `json:"limit_cents"`
}

// Import statuses.
const (
	ImportUploaded = "uploaded"
	ImportQueued   = "queued"
	ImportDone     = "done"
	ImportFailed   = "failed"
)

// Import is an uploaded bank statement (CSV) and what became of it.
type Import struct {
	orm.Model

	WorkspaceID int64  `json:"workspace_id"`
	AccountID   int64  `json:"account_id"`
	Filename    string `json:"filename"`
	Content     string `json:"-"`
	Mapping     string `json:"-"`
	Status      string `json:"status"`
	Imported    int    `json:"imported"`
	Failed      int    `json:"failed"`
	Error       string `json:"error"`
}

// WorkspaceSubscription mirrors a Stripe subscription of a workspace;
// webhooks keep it in sync. UnitAmount is the price in cents per month.
type WorkspaceSubscription struct {
	orm.Model

	WorkspaceID          int64        `json:"workspace_id"`
	StripeSubscriptionID string       `json:"-"`
	Status               string       `json:"status"`
	PriceID              string       `json:"-"`
	UnitAmount           int64        `json:"unit_amount"`
	CurrentPeriodEnd     orm.NullTime `json:"current_period_end"`
	CancelAtPeriodEnd    bool         `json:"cancel_at_period_end"`
}

// Table maps WorkspaceSubscription to workspace_subscriptions.
func (WorkspaceSubscription) Table() string { return "workspace_subscriptions" }

// Valid reports whether the subscription grants the Pro plan: active or in
// its trial. past_due, unpaid, incomplete and canceled do not.
func (s WorkspaceSubscription) Valid() bool { return s.Status == "active" || s.Status == "trialing" }

// EmailSuppression is an address that bounced or complained: it gets no
// more mail (mail.Mailer.SetSuppression, filled by the Resend webhook).
type EmailSuppression struct {
	orm.Model

	Address string `json:"address"`
	Reason  string `json:"reason"`
}
