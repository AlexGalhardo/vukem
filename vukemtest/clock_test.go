package vukemtest_test

import (
	"testing"
	"time"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
)

func TestSessionSurvivesPastFakeClock(t *testing.T) {
	t.Parallel()

	for _, driver := range []string{"cookie", "database", "memory"} {
		t.Run(driver, func(t *testing.T) {
			t.Parallel()

			clock := vukemtest.NewFakeClock(time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC))
			app := vukemtest.New(t, func(opts ...vukem.Option) (*vukem.App, error) {
				return vukem.New(append(opts, vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
					if err := vukem.DefaultMiddleware(app, r); err != nil {
						return err
					}
					r.Get("/set", func(c *web.Ctx) error { c.Session().Put("user", "u1"); return c.String(200, "ok") })
					r.Get(
						"/get",
						func(c *web.Ctx) error { v, _ := c.Session().Get("user").(string); return c.String(200, "user="+v) },
					)
					return nil
				}))...)
			}, vukemtest.Options{Clock: clock.Now, Env: map[string]string{"SESSION_DRIVER": driver}})

			c := app.Client(t)
			c.Get("/set").AssertOK()
			c.Get("/get").AssertSee("user=u1")
		})
	}
}
