// Package account holds the login, registration, password reset and
// email verification handlers (vukem install:auth), rendered with Inertia
// pages in resources/js/pages/Auth.
package account

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/examples/saas/app/accounts"
	"github.com/AlexGalhardo/vukem/examples/saas/app/requests"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/web"
)

// Handlers serves the account pages.
type Handlers struct {
	a *accounts.Accounts
}

// New returns the handlers.
func New(a *accounts.Accounts) *Handlers { return &Handlers{a: a} }

// LoginThrottleKey limits login attempts per email and IP. Inertia posts
// JSON, so the email is read from a JSON body as well as from a form.
func LoginThrottleKey(r *http.Request) string {
	if r.Method != http.MethodPost {
		return ""
	}

	email := r.PostFormValue("email")

	if email == "" && strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
		if err == nil {
			var in struct {
				Email string `json:"email"`
			}

			_ = json.Unmarshal(body, &in)
			email = in.Email
		}

		r.Body = io.NopCloser(bytes.NewReader(body))
	}

	return "login:" + strings.ToLower(email) + ":" + web.NewCtx(nil, r, web.ServicesFrom(r.Context())).IP()
}

// ShowLogin renders the login page.
func (*Handlers) ShowLogin(c *web.Ctx) error { return inertia.Render(c, "Auth/Login", nil) }

// Login signs the user in and goes to the page they wanted.
func (h *Handlers) Login(c *web.Ctx) error {
	in, err := web.Parse(c, requests.LoginSchema)
	if err != nil {
		return err
	}

	creds := map[string]string{"email": strings.ToLower(in.Email), "password": in.Password}

	u, err := h.a.Web.Validate(c, creds)
	if err != nil {
		return err
	}

	// Users with two-factor authentication enter a code first (vukem install:auth --2fa).
	if pending, err := h.startTwoFactor(c, u, in.Remember); pending || err != nil {
		return err
	}

	if err := h.a.Web.Login(c, u, auth.Remember(in.Remember)); err != nil {
		return err
	}

	target, _ := c.Session().Pull(auth.IntendedKey).(string)

	return c.Redirect(web.SafeRedirect(c.Request(), target, "/dashboard"))
}

// ShowRegister renders the registration page.
func (*Handlers) ShowRegister(c *web.Ctx) error { return inertia.Render(c, "Auth/Register", nil) }

// Register creates the account, logs in and asks for email verification.
func (h *Handlers) Register(c *web.Ctx) error {
	in, err := web.Parse(c, requests.RegisterSchema)
	if err != nil {
		return err
	}

	u, err := h.a.Register(c.Context(), in)
	if err != nil {
		return err
	}

	if err := h.a.Web.Login(c, u); err != nil {
		return err
	}

	return c.Redirect("/email/verify")
}

// Logout signs the user out and rotates the history encryption key.
func (h *Handlers) Logout(c *web.Ctx) error {
	if err := h.a.Web.Logout(c); err != nil {
		return err
	}

	inertia.ClearHistory(c)

	return c.Redirect("/")
}

// Dashboard is the first page after login; the user arrives as the shared
// auth.user prop.
func (*Handlers) Dashboard(c *web.Ctx) error { return inertia.Render(c, "Dashboard", nil) }
