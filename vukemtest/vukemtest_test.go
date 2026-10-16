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
			r.Get("/teapot", func(c *web.Ctx) error { return c.String(418, "tea") })
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
		AssertSessionHas("success")

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

			return nil
		}))...)
	})

	app.Client(t).GetJSON("/api/codes/abc").AssertOK().AssertMatchesOpenAPI()

	expectFailure(t, "$.code: length 6, want at most 3", func(tb *fakeTB) {
		app.Client(tb).GetJSON("/api/codes/abcdef").AssertMatchesOpenAPI()
	})
	expectFailure(t, "no operation", func(tb *fakeTB) { app.Client(tb).GetJSON("/nowhere").AssertMatchesOpenAPI() })
}
