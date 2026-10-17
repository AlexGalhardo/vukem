package routes

import (
	"time"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/examples/saas/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/saas/app/controllers/account"
	"github.com/AlexGalhardo/vukem/ratelimit"
	"github.com/AlexGalhardo/vukem/router"
)

// TwoFactor registers the two-factor settings page and the login
// challenge (vukem install:auth --2fa).
func TwoFactor(r *router.Router, app *vukem.App) error {
	acc, err := accounts.New(app)
	if err != nil {
		return err
	}

	h := account.NewTwoFactor(acc, app.Config.Name)
	required := auth.Required(acc.Web)
	// 5 codes per minute per user and IP: 6-digit codes cannot be guessed.
	challenge := ratelimit.Middleware(ratelimit.NewMemory(5, time.Minute), account.TwoFactorThrottleKey)
	confirm := ratelimit.Middleware(ratelimit.NewMemory(5, time.Minute), account.TwoFactorThrottleKey)

	r.Get("/user/two-factor", h.Show).Name("two-factor.show").Use(required)
	r.Post("/user/two-factor", h.Enable).Name("two-factor.enable").Use(required)
	r.Post("/user/two-factor/confirm", h.Confirm).Name("two-factor.confirm").Use(required, confirm)
	r.Post("/user/two-factor/recovery-codes", h.RegenerateRecoveryCodes).Name("two-factor.recovery-codes").Use(required)
	r.Post("/user/two-factor/disable", h.Disable).Name("two-factor.disable").Use(required, confirm)

	r.Get("/two-factor-challenge", h.ShowChallenge).Name("two-factor.login").Use(auth.Guest(acc.Web))
	r.Post("/two-factor-challenge", h.Challenge).Use(auth.Guest(acc.Web), challenge)

	return nil
}
