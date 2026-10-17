package account

import (
	"errors"
	"net/http"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/examples/saas/app/billing"
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/web"
)

// BillingHandlers serve the billing page, Stripe Checkout and the Billing
// Portal (vukem install:billing).
type BillingHandlers struct {
	b *billing.Billing
}

// NewBilling returns the billing handlers.
func NewBilling(b *billing.Billing) *BillingHandlers { return &BillingHandlers{b: b} }

type planView struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Current bool   `json:"current"`
}

type subscriptionView struct {
	Plan              string `json:"plan"`
	Status            string `json:"status"`
	Valid             bool   `json:"valid"`
	CurrentPeriodEnd  string `json:"currentPeriodEnd"`
	CancelAtPeriodEnd bool   `json:"cancelAtPeriodEnd"`
}

type billingPage struct {
	Plans        []planView        `json:"plans"`
	Subscription *subscriptionView `json:"subscription"`
	Subscribed   bool              `json:"subscribed"`
}

func (h *BillingHandlers) page(c *web.Ctx, u models.User) (billingPage, error) {
	sub, ok, err := h.b.Current(c.Context(), u.ID)
	if err != nil {
		return billingPage{}, err
	}

	var page billingPage

	if ok {
		view := &subscriptionView{
			Plan: sub.PriceID, Status: sub.Status, Valid: sub.Valid(), CancelAtPeriodEnd: sub.CancelAtPeriodEnd,
		}

		if p, found := h.b.Config.PlanForPrice(sub.PriceID); found {
			view.Plan = p.Label
		}

		if sub.CurrentPeriodEnd.Valid {
			view.CurrentPeriodEnd = sub.CurrentPeriodEnd.V.Format("January 2, 2006")
		}

		page.Subscription, page.Subscribed = view, sub.Valid()
	}

	for _, p := range h.b.Config.Plans {
		page.Plans = append(page.Plans, planView{
			Name: p.Name, Label: p.Label, Current: page.Subscribed && p.Price == sub.PriceID,
		})
	}

	return page, nil
}

// Checkout sends the user to Stripe Checkout for the plan in the URL.
func (h *BillingHandlers) Checkout(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	target, err := h.b.Checkout(c.Context(), u, c.Param("plan"))
	if err != nil {
		return backWithError(c, err)
	}

	return away(c, target)
}

// Portal sends the user to the Stripe Billing Portal.
func (h *BillingHandlers) Portal(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	target, err := h.b.Portal(c.Context(), u)
	if err != nil {
		return backWithError(c, err)
	}

	return away(c, target)
}

// backWithError shows conflicts (already subscribed, no customer yet) on
// the billing page; other errors go to the error handler.
func backWithError(c *web.Ctx, err error) error {
	if e, ok := errors.AsType[*errs.HTTPError](err); ok && e.Status == http.StatusConflict && !c.WantsJSON() {
		_ = c.Flash("status", e.Message)
		return c.Redirect("/billing")
	}

	return err
}

// away leaves the app for a Stripe page: a 303 redirect for forms, a full
// page visit for Inertia and htmx, the URL for JSON clients.
func away(c *web.Ctx, target string) error {
	switch {
	case inertia.IsInertia(c.Request()):
		return inertia.Location(c, target)
	case c.IsHTMX():
		c.SetHeader("HX-Redirect", target)
		return c.NoContent()
	case c.WantsJSON():
		return c.JSON(http.StatusOK, map[string]string{"url": target})
	}

	return c.RedirectStatus(http.StatusSeeOther, target)
}
