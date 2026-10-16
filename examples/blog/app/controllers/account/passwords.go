package account

import (
	"github.com/AlexGalhardo/vukem/examples/blog/app/requests"
	"github.com/AlexGalhardo/vukem/web"
)

// ShowForgot renders the reset link request form.
func (*Handlers) ShowForgot(c *web.Ctx) error { return c.Render("auth/forgot-password", nil) }

// SendResetLink emails a reset link; the answer is the same whether or not
// the email has an account.
func (h *Handlers) SendResetLink(c *web.Ctx) error {
	in, err := web.Parse(c, requests.ForgotPasswordSchema)
	if err != nil {
		return err
	}

	if err := h.a.SendResetLink(c.Context(), in.Email); err != nil {
		return err
	}

	_ = c.Flash("status", "If that email has an account, we sent a password reset link.")

	return c.Redirect("/forgot-password")
}

// ShowReset renders the new password form.
func (*Handlers) ShowReset(c *web.Ctx) error {
	return c.Render("auth/reset-password", map[string]string{"Token": c.Param("token"), "Email": c.Query("email")})
}

// Reset sets the new password.
func (h *Handlers) Reset(c *web.Ctx) error {
	in, err := web.Parse(c, requests.ResetPasswordSchema)
	if err != nil {
		return err
	}

	if _, err := h.a.ResetPassword(c.Context(), in.Email, in.Token, in.Password); err != nil {
		return err
	}

	_ = c.Flash("status", "Your password was reset. You can log in now.")

	return c.Redirect("/login")
}
