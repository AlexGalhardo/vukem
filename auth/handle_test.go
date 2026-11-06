package auth_test

import (
	"context"
	"net/url"
	"testing"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
)

type handleUser struct {
	id       string
	handle   string
	password string
}

func (u handleUser) AuthID() string       { return u.id }
func (u handleUser) AuthPassword() string { return u.password }

type handleDir struct{ users []handleUser }

func (d handleDir) ByID(_ context.Context, id string) (handleUser, error) {
	for _, u := range d.users {
		if u.id == id {
			return u, nil
		}
	}

	return handleUser{}, auth.ErrUserNotFound
}

func (d handleDir) ByCredentials(_ context.Context, creds map[string]string) (handleUser, error) {
	for _, u := range d.users {
		if u.handle == creds["handle"] {
			return u, nil
		}
	}

	return handleUser{}, auth.ErrUserNotFound
}

func TestHandleGuardIsolatesUsers(t *testing.T) {
	t.Parallel()

	hasher := hash.Fake()

	secret, err := hasher.Make("s3cret-pass")
	if err != nil {
		t.Fatal(err)
	}

	dir := handleDir{users: []handleUser{
		{id: "1", handle: "ana", password: secret},
		{id: "2", handle: "bruno", password: secret},
	}}

	var guard *auth.SessionGuard[handleUser]

	app := vukemtest.New(t, func(opts ...vukem.Option) (*vukem.App, error) {
		return vukem.New(append(opts, vukem.WithRoutes(func(r *router.Router, a *vukem.App) error {
			if err := vukem.DefaultMiddleware(a, r); err != nil {
				return err
			}

			guard = auth.NewSessionGuard("web", dir, hasher, nil)
			guard.IdentifierField = "handle"

			r.Get("/", func(c *web.Ctx) error { return c.String(200, "public") })
			r.Post("/login", func(c *web.Ctx) error {
				in, err := c.Input()
				if err != nil {
					return err
				}

				if _, err := guard.Attempt(c, map[string]string{
					"handle":   str(in["handle"]),
					"password": str(in["password"]),
				}); err != nil {
					return err
				}

				return c.Redirect("/me")
			}).Name("login")
			r.Get("/me", func(c *web.Ctx) error {
				u, err := auth.User[handleUser](c, "web")
				if err != nil {
					return err
				}

				return c.String(200, u.handle)
			}).Use(auth.Required(guard))

			return nil
		}))...)
	})

	app.Client(t).Get("/").AssertOK().AssertSee("public")

	app.Client(t).AcceptJSON().
		PostJSON("/login", map[string]any{"handle": "ana", "password": "nope"}).
		AssertUnprocessable().
		AssertJSONValidationErrors("handle")

	ana := app.Client(t)
	ana.PostForm("/login", url.Values{"handle": {"ana"}, "password": {"s3cret-pass"}}).AssertRedirect("/me")
	ana.Get("/me").AssertOK().AssertSee("ana").AssertDontSee("bruno")

	bruno := app.Client(t)
	bruno.PostForm("/login", url.Values{"handle": {"bruno"}, "password": {"s3cret-pass"}}).AssertRedirect("/me")
	bruno.Get("/me").AssertOK().AssertSee("bruno").AssertDontSee("ana")
	ana.Get("/me").AssertOK().AssertSee("ana").AssertDontSee("bruno")
}
