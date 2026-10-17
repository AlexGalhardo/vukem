package vukemtest_test

import (
	"context"
	"net/url"
	"testing"
	"testing/fstest"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
)

func buildInertia(opts ...vukem.Option) (*vukem.App, error) {
	return buildInertiaWith(inertia.Config{}, opts...)
}

func buildInertiaWith(cfg inertia.Config, opts ...vukem.Option) (*vukem.App, error) {
	return vukem.New(append(opts,
		vukem.WithViews(fstest.MapFS{
			"app.html": {Data: []byte(`{{/* layout: none */}}<html><body>{{.Inertia}}</body></html>`)},
		}),
		vukem.WithInertia(cfg),
		vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
			if err := vukem.DefaultMiddleware(app, r); err != nil {
				return err
			}

			r.Use(app.Inertia.Middleware)
			r.Get("/posts", func(c *web.Ctx) error {
				return inertia.Render(c, "Posts/Index", inertia.Props{
					"posts":   []map[string]any{{"title": "a"}, {"title": "</script>"}},
					"filters": map[string]any{"q": "go"},
					"stats":   inertia.Optional(func(context.Context) (any, error) { return 1, nil }),
				})
			}).Name("posts.index")
			r.Post("/posts", func(c *web.Ctx) error {
				inertia.Flash(c, "status", "Post saved.")
				return c.RedirectRoute("posts.index")
			})

			return nil
		}),
	)...)
}

func TestAssertInertia(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, buildInertia)

	for name, c := range map[string]*vukemtest.Client{
		"html": app.Client(t),
		"json": app.Client(t).Inertia(""),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c.Get("/posts").AssertOK().
				AssertInertia("Posts/Index").
				HasProp("posts", 2).
				HasProp("filters.q").
				PropEquals("posts.1.title", "</script>").
				PropEquals("filters", map[string]any{"q": "go"}).
				MissingProp("stats").
				MissingProp("posts.5")
		})
	}
}

func TestAssertInertiaFailures(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, buildInertia)

	cases := map[string]func(tb *fakeTB){
		`component = "Posts/Index", want "Home"`: func(tb *fakeTB) {
			app.Client(tb).Inertia("").Get("/posts").AssertInertia("Home")
		},
		`prop "nope" missing`: func(tb *fakeTB) {
			app.Client(tb).Inertia("").Get("/posts").AssertInertia("Posts/Index").HasProp("nope")
		},
		`prop "posts" has 2 items, want 3`: func(tb *fakeTB) {
			app.Client(tb).Inertia("").Get("/posts").AssertInertia("Posts/Index").HasProp("posts", 3)
		},
		`prop "posts" present`: func(tb *fakeTB) {
			app.Client(tb).Inertia("").Get("/posts").AssertInertia("Posts/Index").MissingProp("posts")
		},
		`prop "filters.q" = "go", want "rust"`: func(tb *fakeTB) {
			app.Client(tb).Inertia("").Get("/posts").AssertInertia("Posts/Index").PropEquals("filters.q", "rust")
		},
		"not an Inertia response": func(tb *fakeTB) { app.Client(tb).Get("/up").AssertInertia("Posts/Index") },
	}

	for want, fn := range cases {
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			expectFailure(t, want, fn)
		})
	}
}

func TestInertiaClientUsesAppVersion(t *testing.T) {
	t.Parallel()

	// An app with a Vite build has a non-empty asset version.
	app := vukemtest.New(t, func(opts ...vukem.Option) (*vukem.App, error) {
		return buildInertiaWith(inertia.Config{Version: func() string { return "abc" }}, opts...)
	})

	app.Client(t).Inertia("").Get("/posts").AssertOK().AssertInertia("Posts/Index")
	app.Client(t).Inertia("stale").Get("/posts").AssertStatus(409)
}

func TestAssertFlash(t *testing.T) {
	t.Parallel()

	t.Run("inertia", func(t *testing.T) {
		t.Parallel()

		app := vukemtest.New(t, buildInertia)
		c := app.Client(t).Inertia("")

		// Before the redirect is followed the flash sits in the session...
		c.PostJSON("/posts", map[string]any{}).AssertRedirect("/posts").AssertFlash("status", "Post saved.")
		// ...then the next page carries it in page.flash.
		c.Get("/posts").AssertFlash("status", "Post saved.").
			AssertInertia("Posts/Index").FlashEquals("status", "Post saved.")
		c.Get("/posts").AssertNoFlash("status").AssertInertia("Posts/Index").MissingFlash("status")

		expectFailure(t, `flash "status" = "Post saved.", want "nope"`, func(tb *fakeTB) {
			app.Client(tb).Inertia("").PostJSON("/posts", map[string]any{}).AssertFlash("status", "nope")
		})
		expectFailure(t, `no flash "status"`, func(tb *fakeTB) {
			app.Client(tb).Inertia("").Get("/posts").AssertFlash("status", "x")
		})
		expectFailure(t, `flash "status" present`, func(tb *fakeTB) {
			cl := app.Client(tb).Inertia("")
			cl.PostJSON("/posts", map[string]any{})
			cl.Get("/posts").AssertInertia("Posts/Index").MissingFlash("status")
		})
		expectFailure(t, `flash "status" present ("Post saved."), want none`, func(tb *fakeTB) {
			app.Client(tb).Inertia("").PostJSON("/posts", map[string]any{}).AssertNoFlash("status")
		})
		expectFailure(t, `flash "status" missing`, func(tb *fakeTB) {
			app.Client(tb).Inertia("").Get("/posts").AssertInertia("Posts/Index").FlashEquals("status", "x")
		})
	})

	for _, driver := range []string{"cookie", "memory", "database"} {
		t.Run("html/"+driver, func(t *testing.T) {
			t.Parallel()

			app := vukemtest.New(t, buildInertia, vukemtest.Options{Env: map[string]string{"SESSION_DRIVER": driver}})
			c := app.Client(t)

			c.PostForm("/posts", url.Values{}).AssertFlash("status", "Post saved.")
			c.Get("/posts").AssertFlash("status", "Post saved.")
		})
	}
}

func TestInertiaErrorPageInstalledByWithInertia(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, buildInertia)

	app.Client(t).Inertia("").Get("/missing").AssertStatus(404).
		AssertInertia("Error").
		PropEquals("status", 404).
		PropEquals("message", "Not Found")

	app.Client(t).Get("/missing").AssertStatus(404).AssertHeader("X-Inertia", "")
}
