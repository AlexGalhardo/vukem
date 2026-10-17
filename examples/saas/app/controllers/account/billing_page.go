package account

import (
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/examples/saas/app/models"
	"github.com/AlexGalhardo/vukem/inertia"
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

	return inertia.Render(c, "Billing", inertia.Props{
		"plans":        page.Plans,
		"subscription": page.Subscription,
		"subscribed":   page.Subscribed,
		"checkout":     c.Query("checkout") == "success",
	})
}
