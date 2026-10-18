package vukemtest_test

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/api"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

type Note struct {
	orm.Model
	Body string `json:"body"`
}

type noteInput struct {
	Body string `json:"body" form:"body"`
}

var noteSchema = z.Struct[noteInput](z.Shape{"body": z.String().Trim().Min(3)})

var migrations = []schema.Migration{{
	ID: "1_notes",
	Up: func(s *schema.Schema) error {
		return s.Create("notes", func(t *schema.Table) { t.ID(); t.Text("body"); t.Timestamps() })
	},
	Down: func(s *schema.Schema) error { return s.Drop("notes") },
}}

var views = fstest.MapFS{
	"notes/index.html": {
		Data: []byte(
			`<html><body>{{block "list" .}}<ul>{{range .}}<li>{{.Body}}</li>{{end}}</ul>{{end}}</body></html>`,
		),
	},
}

// build is what an application's bootstrap.New looks like.
func build(opts ...vukem.Option) (*vukem.App, error) {
	return vukem.New(append(opts,
		vukem.WithMigrations(migrations),
		vukem.WithViews(views),
		vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
			if err := vukem.DefaultMiddleware(app, r); err != nil {
				return err
			}

			db, err := app.DB()
			if err != nil {
				return err
			}

			r.Get("/notes", func(c *web.Ctx) error {
				notes, err := orm.QueryOf[Note](db).OrderBy("id", orm.Asc).Get(c.Context())
				if err != nil {
					return err
				}

				return c.Render("notes/index", notes, web.Block("list"))
			}).Name("notes.index")

			r.Post("/notes", func(c *web.Ctx) error {
				in, err := web.Parse(c, noteSchema)
				if err != nil {
					return err
				}

				n := Note{Body: in.Body}
				if err := orm.Create(c.Context(), db, &n); err != nil {
					return err
				}

				if c.WantsJSON() {
					c.HTMX().Trigger("note-created", nil)
					return c.Created("/notes/"+strconv.FormatInt(n.ID, 10), n)
				}

				_ = c.Flash("success", "Note saved.")

				return c.RedirectRoute("notes.index")
			})

			r.Get("/notes/{id}", func(c *web.Ctx) error {
				id, err := c.ParamInt("id")
				if err != nil {
					return err
				}

				n, err := orm.QueryOf[Note](db).Find(c.Context(), id)
				if err != nil {
					return err
				}

				return c.JSON(http.StatusOK, n)
			})

			r.Get("/secret", func(*web.Ctx) error { return errs.ErrForbidden })
			r.Get("/flash-pulled", func(c *web.Ctx) error {
				_ = c.Flash("gone", "x")
				c.Session().Pull("gone") // still listed as flashed, value removed

				return c.NoContent()
			})
			r.Get("/teapot", func(c *web.Ctx) error { return c.String(418, "tea") })
			accept := func(c *web.Ctx) error { return c.String(200, c.Header("Accept")) }
			r.Get("/accept", accept)
			r.Post("/accept", accept)
			r.Put("/accept", accept)
			r.Patch("/accept", accept)
			r.Get("/hx-list", func(c *web.Ctx) error {
				c.SetHeader("HX-Trigger", "first, second-event")
				return c.NoContent()
			})
			r.Get("/session-nil", func(c *web.Ctx) error {
				c.Session().Put("empty", nil)
				return c.NoContent()
			})
			r.Delete("/notes", func(c *web.Ctx) error { return c.NoContent() })

			return nil
		}),
	)...)
}

func TestEndToEnd(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, build)
	c := app.Client(t)

	c.PostForm("/notes", url.Values{"body": {"  Buy milk  "}}).
		AssertRedirectRoute("notes.index").
		AssertSessionHas("success").
		AssertFlash("success", "Note saved.")

	vukemtest.AssertDatabaseHas(t, app, "notes", map[string]any{"body": "Buy milk"})
	vukemtest.AssertDatabaseCount(t, app, "notes", 1)
	vukemtest.AssertDatabaseMissing(t, app, "notes", map[string]any{"body": "nope"})

	c.Get("/notes").AssertOK().AssertSee("<li>Buy milk</li>").AssertSee("<html>").AssertDontSee("nope")
	c.HTMX().Get("/notes").AssertOK().AssertFragment().AssertSee("<ul>")

	// Invalid form: errors and old input are flashed, then we go back.
	app.Client(t).WithHeader("Referer", "/notes").
		PostForm("/notes", url.Values{"body": {"x"}}).
		AssertRedirect("/notes").
		AssertSessionHasErrors("body").
		AssertSessionMissing("success")

	jc := app.Client(t).AcceptJSON()
	jc.PostJSON("/notes", map[string]any{"body": "From the API"}).
		AssertCreated().
		AssertHeader("Location", "/notes/2").
		AssertJSONPath("body", "From the API").
		AssertJSONPath("id", 2).
		AssertHXTrigger("note-created")

	jc.PostJSON("/notes", map[string]any{"body": ""}).AssertJSONValidationErrors("body")
	jc.GetJSON("/notes/99").AssertProblem(http.StatusNotFound)
	jc.GetJSON("/notes/abc").AssertNotFound()
	jc.GetJSON("/secret").AssertForbidden()
	jc.Get("/teapot").AssertStatus(418)
	jc.Delete("/notes").AssertNoContent()
	jc.GetJSON("/notes/1").AssertJSONPath("body", "Buy milk").AssertJSONCount("", 4)
}

func TestRefreshDatabaseAndClock(t *testing.T) {
	t.Parallel()

	clock := vukemtest.NewFakeClock(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	app := vukemtest.New(t, build, vukemtest.Options{Clock: clock.Now, Env: map[string]string{"APP_NAME": "Notes"}})

	if app.Config.Name != "Notes" || !app.Now().Equal(clock.Now()) {
		t.Fatalf("options not applied: %s %v", app.Config.Name, app.Now())
	}

	clock.Advance(time.Hour)
	clock.Set(clock.Now().Add(time.Minute))
	if app.Now().Hour() != 1 {
		t.Fatal("clock")
	}

	app.Client(t).PostForm("/notes", url.Values{"body": {"one"}})
	vukemtest.AssertDatabaseCount(t, app, "notes", 1)

	app.RefreshDatabase(t)
	vukemtest.AssertDatabaseCount(t, app, "notes", 0)

	if app.Handler() == nil {
		t.Fatal("handler")
	}
}

func TestIsolatedDatabases(t *testing.T) {
	t.Parallel()

	a := vukemtest.New(t, build)
	b := vukemtest.New(t, build)

	a.Client(t).PostForm("/notes", url.Values{"body": {"only in a"}})

	vukemtest.AssertDatabaseCount(t, a, "notes", 1)
	vukemtest.AssertDatabaseCount(t, b, "notes", 0)
}

// fakeTB records Fatalf instead of failing the real test.
type fakeTB struct {
	testing.TB

	failed bool
	msg    string
}

func (f *fakeTB) Helper() {}

func (f *fakeTB) Fatalf(format string, args ...any) {
	f.failed = true
	f.msg = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func expectFailure(t *testing.T, want string, fn func(tb *fakeTB)) {
	t.Helper()

	f := &fakeTB{TB: t}
	done := make(chan struct{})

	go func() {
		defer close(done)
		fn(f)
	}()
	<-done

	if !f.failed || !strings.Contains(f.msg, want) {
		t.Fatalf("assertion did not fail with %q (failed=%v msg=%q)", want, f.failed, f.msg)
	}
}

func TestAssertionsReportFailures(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, build)

	cases := map[string]func(tb *fakeTB){
		"status = 404, want 200": func(tb *fakeTB) { app.Client(tb).Get("/missing").AssertOK() },
		"want a redirect":        func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertRedirect("/x") },
		`redirect to "/notes"`: func(tb *fakeTB) {
			app.Client(tb).PostForm("/notes", url.Values{"body": {"long enough"}}).AssertRedirect("/other")
		},
		`does not contain "zzz"`:     func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertSee("zzz") },
		`body contains "html"`:       func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertDontSee("html") },
		`header X-Nope = ""`:         func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertHeader("X-Nope", "1") },
		"body is not JSON":           func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertJSONPath("a", 1) },
		`JSON path "nope" not found`: func(tb *fakeTB) { app.Client(tb).GetJSON("/notes/99").AssertJSONPath("nope", 1) },
		`JSON path "status" = 404`:   func(tb *fakeTB) { app.Client(tb).GetJSON("/notes/99").AssertJSONPath("status", 500) },
		`has -1 entries, want 3`:     func(tb *fakeTB) { app.Client(tb).GetJSON("/notes/99").AssertJSONCount("title", 3) },
		"JSON body differs":          func(tb *fakeTB) { app.Client(tb).GetJSON("/notes/99").AssertJSON(map[string]any{}) },
		"want application/problem":   func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertProblem(200) },
		`no validation error for "x"`: func(tb *fakeTB) {
			app.Client(tb).PostJSON("/notes", map[string]any{}).AssertJSONValidationErrors("x")
		},
		`does not include "evt"`: func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertHXTrigger("evt") },
		"want a fragment":        func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertFragment() },
		`session has no "k"`:     func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertSessionHas("k") },
		`session has "success"`: func(tb *fakeTB) {
			app.Client(tb).PostForm("/notes", url.Values{"body": {"long enough"}}).AssertSessionMissing("success")
		},
		`flash "success" = "Note saved.", want "x"`: func(tb *fakeTB) {
			app.Client(tb).PostForm("/notes", url.Values{"body": {"long enough"}}).AssertFlash("success", "x")
		},
		`no flash "success"`:          func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertFlash("success", "Note saved.") },
		`no flashed validation error`: func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertSessionHasErrors("body") },
		"no row in notes":             func(tb *fakeTB) { vukemtest.AssertDatabaseHas(tb, app, "notes", map[string]any{"body": "ghost"}) },
		"want none":                   func(tb *fakeTB) { vukemtest.AssertDatabaseMissing(tb, app, "migrations", nil) },
		"want 9":                      func(tb *fakeTB) { vukemtest.AssertDatabaseCount(tb, app, "notes", 9) },
		"invalid table":               func(tb *fakeTB) { vukemtest.AssertDatabaseCount(tb, app, "notes; --", 0) },
		"invalid column":              func(tb *fakeTB) { vukemtest.AssertDatabaseHas(tb, app, "notes", map[string]any{"a b": 1}) },
		"count nope":                  func(tb *fakeTB) { vukemtest.AssertDatabaseCount(tb, app, "nope", 0) },
		"no route named":              func(tb *fakeTB) { app.Client(tb).Get("/notes").AssertRedirectRoute("ghost") },
		"build app": func(tb *fakeTB) {
			vukemtest.New(tb, func(...vukem.Option) (*vukem.App, error) { return nil, errors.New("x") })
		},
	}

	for want, fn := range cases {
		t.Run(want, func(t *testing.T) {
			t.Parallel()
			expectFailure(t, want, fn)
		})
	}

	// Failure output includes the request, status and body.
	expectFailure(t, "GET /missing → 404", func(tb *fakeTB) { app.Client(tb).Get("/missing").AssertOK() })
	expectFailure(t, "vukemtest: encode body", func(tb *fakeTB) { app.Client(tb).PostJSON("/notes", func() {}) })

	// Null columns can be asserted.
	vukemtest.AssertDatabaseHas(t, app, "migrations", map[string]any{"id": "1_notes"})
	vukemtest.AssertDatabaseMissing(t, app, "notes", map[string]any{"body": nil, "id": 999})

	r := app.Client(t).WithCookie("a", "b").WithToken("tok").PutJSON("/notes", map[string]any{})
	if r.Status() != http.StatusMethodNotAllowed || r.Header("Allow") == "" || r.Body() == "" || r.Recorder() == nil {
		t.Fatalf("PutJSON = %d", r.Status())
	}
	_ = app.Client(t).PatchJSON("/notes", map[string]any{})
	if v, ok := app.Client(t).GetJSON("/notes/99").JSONPath(""); !ok || v == nil {
		t.Fatal("JSONPath root")
	}
}

type codeOut struct {
	Code string `json:"code" validate:"max=3"`
}

func TestAssertMatchesOpenAPI(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, func(opts ...vukem.Option) (*vukem.App, error) {
		return vukem.New(append(opts, vukem.WithRoutes(func(r *router.Router, _ *vukem.App) error {
			api.Register(r, "GET /api/codes/{code}", api.Op{},
				func(_ *web.Ctx, in struct {
					Code string `json:"code"`
				},
				) (codeOut, error) {
					return codeOut{Code: in.Code}, nil
				})
			r.Get("/api/plain", func(c *web.Ctx) error { return c.JSON(http.StatusOK, map[string]any{"ok": true}) })

			return nil
		}))...)
	})

	app.Client(t).GetJSON("/api/codes/abc").AssertOK().AssertMatchesOpenAPI()
	// A plain route documents an untyped response: any body matches.
	app.Client(t).GetJSON("/api/plain").AssertOK().AssertMatchesOpenAPI()

	expectFailure(t, "$.code: length 6, want at most 3", func(tb *fakeTB) {
		app.Client(tb).GetJSON("/api/codes/abcdef").AssertMatchesOpenAPI()
	})
	expectFailure(t, "no operation", func(tb *fakeTB) { app.Client(tb).GetJSON("/nowhere").AssertMatchesOpenAPI() })
}

func TestGetJSONDoesNotStick(t *testing.T) {
	t.Parallel()

	c := vukemtest.New(t, build).Client(t)

	c.GetJSON("/accept").AssertSee("application/json")

	if got := c.Get("/accept").Body(); strings.Contains(got, "application/json") {
		t.Errorf("Accept after GetJSON = %q, want the client's default", got)
	}

	c.AcceptJSON()
	c.GetJSON("/accept")
	c.Get("/accept").AssertSee("application/json") // AcceptJSON stays
}

func TestAssertHeaderContainsAndMissing(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, build)

	app.Client(t).Get("/teapot").AssertHeaderContains("Content-Type", "text/plain").AssertHeaderMissing("X-Powered-By")

	expectFailure(t, `header Content-Type = "text/plain; charset=utf-8", want it to contain "json"`, func(tb *fakeTB) {
		app.Client(tb).Get("/teapot").AssertHeaderContains("Content-Type", "json")
	})
	expectFailure(t, `header Content-Type present`, func(tb *fakeTB) {
		app.Client(tb).Get("/teapot").AssertHeaderMissing("Content-Type")
	})
}

func TestBuildReturnsErrors(t *testing.T) {
	t.Parallel()

	errBad := errors.New("bad config")

	cases := map[string]struct {
		build vukem.BuildFunc
		opts  vukemtest.Options
		want  string
	}{
		"build": {
			build: func(...vukem.Option) (*vukem.App, error) { return nil, errBad },
			want:  "vukemtest: build app: bad config",
		},
		"database": {
			build: build,
			opts:  vukemtest.Options{Env: map[string]string{"DATABASE_URL": "nope://x"}},
			want:  `no driver for "nope" URLs`,
		},
		"session driver": {
			build: build,
			opts:  vukemtest.Options{Env: map[string]string{"SESSION_DRIVER": "bogus"}},
			want:  `unknown SESSION_DRIVER "bogus"`,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			app, err := vukemtest.Build(t, tc.build, tc.opts)
			if err == nil || app != nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Build() = %v, %v, want nil, error containing %q", app, err, tc.want)
			}
		})
	}

	if _, err := vukemtest.Build(
		t,
		func(...vukem.Option) (*vukem.App, error) { return nil, errBad },
	); !errors.Is(
		err,
		errBad,
	) {
		t.Errorf("Build() error = %v, want it to wrap %v", err, errBad)
	}

	app, err := vukemtest.Build(t, build)
	if err != nil {
		t.Fatalf("Build(valid) error = %v", err)
	}

	app.Client(t).Get("/notes").AssertOK()
}

func TestAssertFlashNeedsPresence(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, build)

	app.Client(t).Get("/flash-pulled").AssertNoFlash("gone")
	app.Client(t).PostForm("/notes", url.Values{"body": {"long enough"}}).AssertNoFlash("other")

	expectFailure(t, `no flash "gone"`, func(tb *fakeTB) {
		app.Client(tb).Get("/flash-pulled").AssertFlash("gone", nil)
	})
	expectFailure(t, `flash "success" present ("Note saved."), want none`, func(tb *fakeTB) {
		app.Client(tb).PostForm("/notes", url.Values{"body": {"long enough"}}).AssertNoFlash("success")
	})
}

type actor string

func (a actor) AuthID() string { return string(a) }

// ActingAs and WithSession add to the session in either order, keeping
// what is already there.
func TestActingAsWithSession(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, build)

	tests := map[string]*vukemtest.Client{
		"values first": app.Client(t).WithSession(map[string]any{"stamp": "s1"}).ActingAs("web", actor("7")),
		"login first":  app.Client(t).ActingAs("web", actor("7")).WithSession(map[string]any{"stamp": "s1"}),
	}

	for name, c := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			s := c.Get("/teapot").Session()
			got := map[string]any{"stamp": s["stamp"], "auth_web": s["auth_web"], "owner": s[web.SessionUserKey]}
			want := map[string]any{"stamp": "s1", "auth_web": "7", "owner": "web:7"}

			if diff := cmp.Diff(want, got); diff != "" {
				t.Fatalf("session (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSendJSONDoesNotStick(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, build)

	sends := map[string]func(c *vukemtest.Client) *vukemtest.Response{
		"PostJSON":  func(c *vukemtest.Client) *vukemtest.Response { return c.PostJSON("/accept", map[string]any{}) },
		"PutJSON":   func(c *vukemtest.Client) *vukemtest.Response { return c.PutJSON("/accept", map[string]any{}) },
		"PatchJSON": func(c *vukemtest.Client) *vukemtest.Response { return c.PatchJSON("/accept", map[string]any{}) },
	}

	for name, send := range sends {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			c := app.Client(t)
			send(c).AssertSee("application/json")

			if got := c.Get("/accept").Body(); strings.Contains(got, "application/json") {
				t.Errorf("Accept after %s = %q, want the client's default", name, got)
			}

			c.WithHeader("Accept", "text/html")
			send(c).AssertSee("text/html")
			c.Get("/accept").AssertSee("text/html")
		})
	}
}

func TestAssertHXTriggerCommaList(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, build)

	app.Client(t).Get("/hx-list").AssertHXTrigger("first").AssertHXTrigger("second-event")

	expectFailure(t, `does not include "second"`, func(tb *fakeTB) {
		app.Client(tb).Get("/hx-list").AssertHXTrigger("second")
	})
}

func TestSessionAssertionsSeeStoredNil(t *testing.T) {
	t.Parallel()

	app := vukemtest.New(t, build)

	app.Client(t).Get("/session-nil").AssertSessionHas("empty").AssertSessionMissing("other")

	expectFailure(t, `session has "empty"`, func(tb *fakeTB) {
		app.Client(tb).Get("/session-nil").AssertSessionMissing("empty")
	})
}
