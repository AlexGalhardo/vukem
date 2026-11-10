package routes

import (
	"time"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/examples/blog/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/blog/app/controllers/account"
	"github.com/AlexGalhardo/vukem/ratelimit"
	"github.com/AlexGalhardo/vukem/router"
)

// Auth registers login, registration, password reset and email
// verification routes (vukem install:auth).
func Auth(r *router.Router, app *vukem.App) error {
	acc, err := accounts.New(app)
	if err != nil {
		return err
	}

	h := account.New(acc)
	guest := auth.Guest(acc.Web)
	required := auth.Required(acc.Web)
	// Signed-in pages also need a verified email; unverified users are sent
	// to the verification notice.
	verified := auth.Verified(acc.Web)
	throttle := ratelimit.Middleware(ratelimit.NewMemory(5, time.Minute), account.LoginThrottleKey)
	// One address trying many accounts never fills the per-email budget.
	loginByIP := ratelimit.Middleware(ratelimit.NewMemory(30, time.Minute), ratelimit.ByIP)
	mails := ratelimit.Middleware(ratelimit.NewMemory(3, time.Minute), ratelimit.ByIP)
	// Sign-up queues a mail: limit it per IP and per address. The limits count
	// requests, not accounts, so they answer alike for new and existing emails.
	signupIP := ratelimit.Middleware(ratelimit.NewMemory(10, 10*time.Minute), ratelimit.ByIP)
	signupEmail := ratelimit.Middleware(ratelimit.NewMemory(5, time.Hour), account.RegisterThrottleKey)

	r.Get("/login", h.ShowLogin).Name("login").Use(guest)
	r.Post("/login", h.Login).Use(guest, loginByIP, throttle)
	r.Get("/register", h.ShowRegister).Name("register").Use(guest)
	r.Post("/register", h.Register).Use(guest, signupIP, signupEmail)
	r.Get("/register/check-email", h.CheckEmail).Name("register.check").Use(guest)
	r.Post("/logout", h.Logout).Name("logout").Use(required)

	r.Get("/forgot-password", h.ShowForgot).Name("password.request").Use(guest)
	r.Post("/forgot-password", h.SendResetLink).Name("password.email").Use(guest, mails)
	r.Get("/reset-password/{token}", h.ShowReset).Name("password.reset").Use(guest).SecretParams("token")
	r.Post("/reset-password", h.Reset).Name("password.update").Use(guest)

	r.Get("/email/verify", h.Notice).Name("verification.notice").Use(required)
	r.Get("/email/verify/{id}", h.Verify).Name("verification.verify").Use(required, acc.Signer.Middleware())
	r.Post("/email/verification-notification", h.Resend).Name("verification.send").Use(required, mails)

	r.Get("/dashboard", h.Dashboard).Name("dashboard").Use(required, verified)

	return nil
}
