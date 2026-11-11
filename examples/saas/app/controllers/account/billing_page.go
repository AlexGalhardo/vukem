package account

import (
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/examples/saas/app/pages"
	"github.com/AlexGalhardo/vukem/web"
)

// Show renders the plans and the current subscription.
func (h *BillingHandlers) Show(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	page, err := h.page(c, u)
	if err != nil {
		return err
	}

	return pages.Billing.Render(c, pages.BillingProps{
		Plans:        page.Plans,
		Subscription: page.Subscription,
		Subscribed:   page.Subscribed,
		Checkout:     c.Query("checkout") == "success",
	})
}
