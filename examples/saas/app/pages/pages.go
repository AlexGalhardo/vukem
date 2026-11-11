// Package pages declares the props of every Inertia page. Controllers render
// them with pages.<Page>.Render(c, props), inertia.Config lists them in
// Pages, and vukem codegen:pages writes resources/js/page-props.ts from them,
// so the React pages and the Go controllers share one declaration.
package pages

import (
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/inertia"
)

// Shared is the props every page receives (inertia.Config.SharedType).
type Shared struct {
	AppName string      `json:"appName"`
	Auth    *SharedAuth `json:"auth"` // present after vukem install:auth
}

// SharedAuth is the signed-in user, or null.
type SharedAuth struct {
	User *models.User `json:"user"`
}

// The typed pages.
var (
	Home               = inertia.Define[struct{}]("Home")
	Dashboard          = inertia.Define[struct{}]("Dashboard")
	Login              = inertia.Define[struct{}]("Auth/Login")
	Register           = inertia.Define[struct{}]("Auth/Register")
	ForgotPassword     = inertia.Define[struct{}]("Auth/ForgotPassword")
	TwoFactorChallenge = inertia.Define[struct{}]("Auth/TwoFactorChallenge")
	VerifyEmail        = inertia.Define[struct{}]("Auth/VerifyEmail")
	ProjectsCreate     = inertia.Define[struct{}]("Projects/Create")
	ProjectsIndex      = inertia.Define[ProjectsIndexProps]("Projects/Index")
	ProjectsShow       = inertia.Define[ProjectProps]("Projects/Show")
	ProjectsEdit       = inertia.Define[ProjectProps]("Projects/Edit")
	Billing            = inertia.Define[BillingProps]("Billing")
	ResetPassword      = inertia.Define[ResetPasswordProps]("Auth/ResetPassword")
	TwoFactor          = inertia.Define[TwoFactorProps]("Auth/TwoFactor")
)

// ProjectsIndexProps are the props of Projects/Index.
type ProjectsIndexProps struct {
	Projects  []models.Project `json:"projects"`
	Filters   ProjectFilters   `json:"filters"`
	FreeLimit int              `json:"freeLimit"`
}

// ProjectFilters echoes the search the list was filtered by.
type ProjectFilters struct {
	Q string `json:"q"`
}

// ProjectProps are the props of Projects/Show and Projects/Edit.
type ProjectProps struct {
	Project models.Project `json:"project"`
}

// ResetPasswordProps are the props of Auth/ResetPassword.
type ResetPasswordProps struct {
	Token string `json:"token"`
	Email string `json:"email"`
}

// BillingPlan is a plan the user can pick.
type BillingPlan struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Current bool   `json:"current"`
}

// BillingSubscription is the user's subscription.
type BillingSubscription struct {
	Plan              string `json:"plan"`
	Status            string `json:"status"`
	Valid             bool   `json:"valid"`
	CurrentPeriodEnd  string `json:"currentPeriodEnd"`
	CancelAtPeriodEnd bool   `json:"cancelAtPeriodEnd"`
}

// BillingProps are the props of Billing.
type BillingProps struct {
	Plans        []BillingPlan        `json:"plans"`
	Subscription *BillingSubscription `json:"subscription"`
	Subscribed   bool                 `json:"subscribed"`
	Checkout     bool                 `json:"checkout"`
}

// TwoFactorProps are the props of Auth/TwoFactor. Secret and QR are null until
// a secret is pending; RecoveryCodes is null unless codes were just issued.
type TwoFactorProps struct {
	Enabled           bool      `json:"enabled"`
	Pending           bool      `json:"pending"`
	Secret            *string   `json:"secret"`
	QR                *string   `json:"qr"`
	RecoveryCodes     *[]string `json:"recoveryCodes"`
	RecoveryCodesLeft int       `json:"recoveryCodesLeft"`
}

// All lists the typed pages for inertia.Config.Pages.
func All() []inertia.PageInfo {
	return []inertia.PageInfo{
		Home.Info(), Dashboard.Info(), Login.Info(), Register.Info(), ForgotPassword.Info(),
		TwoFactorChallenge.Info(), VerifyEmail.Info(), ProjectsCreate.Info(), ProjectsIndex.Info(),
		ProjectsShow.Info(), ProjectsEdit.Info(), Billing.Info(), ResetPassword.Info(), TwoFactor.Info(),
	}
}
