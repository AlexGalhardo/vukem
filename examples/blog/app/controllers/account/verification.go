package account

import (
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/examples/blog/app/models"
	"github.com/AlexGalhardo/vukem/web"
)

// Notice asks the user to check their inbox.
func (*Handlers) Notice(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	if u.EmailVerified() {
		return c.Redirect("/dashboard")
	}

	return c.Render("auth/verify-email", u)
}

// Verify handles the signed link from the verification email.
func (h *Handlers) Verify(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	if c.Param("id") != u.AuthID() {
		return errs.New(403, "This verification link belongs to another account.")
	}

	if err := h.a.MarkVerified(c.Context(), h.a.DB, &u); err != nil {
		return err
	}

	_ = c.Flash("status", "Your email address is verified.")

	return c.Redirect("/dashboard")
}

// Resend sends a new verification email.
func (h *Handlers) Resend(c *web.Ctx) error {
	u, err := auth.User[models.User](c, "web")
	if err != nil {
		return err
	}

	if err := h.a.SendVerification(c.Context(), u); err != nil {
		return err
	}

	_ = c.Flash("status", "A new verification link was sent.")

	return c.Redirect("/email/verify")
}
