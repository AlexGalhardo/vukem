export type TabFile = { path: string; lang: "go" | "html" | "bash"; code: string };

export type Tab = {
  id: string;
  label: string;
  /** Docs pages and example files the code is trimmed from; keep them in sync. */
  source: string;
  files: TabFile[];
  title: string;
  /** `[[term|definition]]` becomes a tooltip. */
  text: string;
  points: string[];
};

export const tabs: Tab[] = [
  {
    id: "routing",
    label: "Routing",
    source: "basics/routing.mdx, basics/controllers.mdx",
    files: [
      {
        path: "routes/web.go",
        lang: "go",
        code: `package routes

import (
	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/router"

	"blog/app/controllers"
)

func Web(r *router.Router, app *vukem.App) error {
	db, err := app.DB()
	if err != nil {
		return err
	}

	posts := controllers.NewPostController(db)

	r.Get("/", controllers.Home).Name("home")

	// Seven routes from the controller's methods:
	// posts.index, posts.show, posts.store...
	r.Resource("posts", posts)

	// A prefix, a name and middleware shared by a group.
	r.Group("/admin", func(g *router.Group) {
		g.Use(middleware.BodyLimit(1 << 20))

		g.Get("/stats", controllers.Stats).Name("stats")
	}).Name("admin.")

	return nil
}`,
      },
      {
        path: "app/controllers/post_controller.go",
        lang: "go",
        code: `package controllers

import (
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"

	"blog/app/models"
)

type PostController struct{ db *database.DB }

func NewPostController(db *database.DB) *PostController {
	return &PostController{db: db}
}

// Show handles GET /posts/{post}.
func (pc *PostController) Show(c *web.Ctx) error {
	post, err := pc.find(c)
	if err != nil {
		return err
	}

	return c.Render("posts/show", post)
}

// find loads the post in the URL, or fails with 404.
func (pc *PostController) find(c *web.Ctx) (models.Post, error) {
	id, err := c.ParamInt("post")
	if err != nil {
		return models.Post{}, err
	}

	return orm.QueryOf[models.Post](pc.db).Find(c.Context(), id)
}`,
      },
    ],
    title: "Plain net/http routing, with names and groups.",
    text: "Routes sit on Go's own [[ServeMux|The standard library router in net/http. Since Go 1.22 it matches methods and wildcards.]], so a handler is an ordinary function. Vukem adds the parts it lacks.",
    points: [
      "Named routes and URL generation",
      "A [[resource|One call that registers the seven RESTful routes of a controller: index, create, store, show, edit, update, destroy.]] line gives seven routes",
      "Route mistakes fail at boot, not in production",
    ],
  },
  {
    id: "validation",
    label: "Validation",
    source: "basics/validation.mdx",
    files: [
      {
        path: "app/requests/comment.go",
        lang: "go",
        code: `package requests

import "github.com/AlexGalhardo/vukem/z"

// StoreComment is the input of the comment form.
type StoreComment struct {
	Author string \`json:"author" form:"author"\`
	Body   string \`json:"body"   form:"body"\`
}

// StoreCommentSchema validates StoreComment.
var StoreCommentSchema = z.Struct[StoreComment](z.Shape{
	"author": z.String().Trim().Min(2).Max(60),
	"body":   z.String().Trim().Min(3).Max(500),
})`,
      },
      {
        path: "app/controllers/comment_controller.go",
        lang: "go",
        code: `func (cc *CommentController) Store(c *web.Ctx) error {
	postID, err := c.ParamInt("post")
	if err != nil {
		return err
	}

	in, err := web.Parse(c, requests.StoreCommentSchema)
	if err != nil {
		// Invalid input: Vukem answers 422 (or redirects back).
		return err
	}

	// in is a requests.StoreComment with trimmed, checked fields.
	comment := models.Comment{
		PostID: postID,
		Author: in.Author,
		Body:   in.Body,
	}
	if err := orm.Create(c.Context(), cc.db, &comment); err != nil {
		return err
	}

	_ = c.Flash("success", "Thanks for your comment.")

	return c.RedirectRoute("posts.show", "post", postID)
}`,
      },
      {
        path: "routes/web.go",
        lang: "go",
        code: `r.Resource("posts.comments", controllers.NewCommentController(db))`,
      },
    ],
    title: "Parse the input, don't just check it.",
    text: "A [[z schema|Zod-style schemas in the z package. They describe valid input once and turn raw form, JSON or query data into a typed Go value.]] turns untrusted input into a typed struct, or a list of every problem it found.",
    points: [
      "Forms, JSON and query strings, one schema",
      "Errors as a 422, redirect or htmx fragment",
      "Translatable messages, shared with the admin",
    ],
  },
  {
    id: "orm",
    label: "DB / ORM",
    source: "database/orm.mdx, database/migrations.mdx",
    files: [
      {
        path: "database/migrations/20261016184615_create_posts_table.go",
        lang: "go",
        code: `package migrations

import "github.com/AlexGalhardo/vukem/schema"

func m20261016184615CreatePostsTable() schema.Migration {
	return schema.Migration{
		ID: "20261016184615_create_posts_table",
		Up: func(s *schema.Schema) error {
			return s.Create("posts", func(t *schema.Table) {
				t.ID()
				t.ForeignID("user_id").Constrained().CascadeOnDelete()
				t.String("title")
				t.String("slug").Unique()
				t.Text("body")
				t.Integer("views").Default(0)
				t.Timestamps()
			})
		},
		Down: func(s *schema.Schema) error {
			return s.DropIfExists("posts")
		},
	}
}`,
      },
      {
        path: "app/models/post.go",
        lang: "go",
        code: `package models

import "github.com/AlexGalhardo/vukem/orm"

// Post is a row of the posts table.
type Post struct {
	orm.Model
	UserID int64  \`json:"user_id"\`
	Title  string \`json:"title"\`
	Slug   string \`json:"slug"\`
	Body   string \`json:"body"\`
	Views  int    \`json:"views"\`
}`,
      },
      {
        path: "app/controllers/post_controller.go",
        lang: "go",
        code: `func (h *PostController) Index(c *web.Ctx) error {
	posts, err := orm.QueryOf[models.Post](h.db).
		Where("views", ">=", 100).
		OrderBy("id", orm.Desc).
		Limit(20).
		Get(c.Context())
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, posts)
}

func (h *PostController) Store(c *web.Ctx) error {
	post := models.Post{UserID: 1, Title: "Hello", Slug: "hello"}
	if err := orm.Create(c.Context(), h.db, &post); err != nil {
		return err
	}

	return c.JSON(http.StatusCreated, post)
}`,
      },
    ],
    title: "Typed queries, no Active Record.",
    text: "A model is a plain struct. [[Generics|Go's type parameters. QueryOf[models.Post] returns a []models.Post, so there is nothing to cast.]] give you the right slice back, and every value travels as a bound parameter.",
    points: [
      "The executor is passed in: a db or a [[tx|A database transaction: the same call works inside it, so a handler can be atomic.]], same call",
      "Relations, soft deletes and encrypted columns",
      "Migrations, factories and seeders included",
    ],
  },
  {
    id: "views",
    label: "Views",
    source: "frontend/htmx.mdx",
    files: [
      {
        path: "resources/views/posts/index.html",
        lang: "html",
        code: `{{define "content"}}
<input type="search" name="q" value="{{.Search}}"
	hx-get="{{route "posts.index"}}"
	hx-trigger="input changed delay:300ms, search"
	hx-target="#post-list">

{{block "post-list" .}}
<ul id="post-list" class="list">
	{{range .Posts}}
	<li><a href="{{route "posts.show" "post" .ID}}">{{.Title}}</a></li>
	{{else}}
	<li class="empty">No posts yet.</li>
	{{end}}
</ul>
{{end}}
{{end}}`,
      },
      {
        path: "app/controllers/post_controller.go",
        lang: "go",
        code: `func (pc *PostController) Index(c *web.Ctx) error {
	q := orm.QueryOf[models.Post](pc.db).OrderBy("id", orm.Desc)
	if search := c.Query("q"); search != "" {
		q = q.Where("title", "LIKE", "%"+search+"%")
	}

	posts, err := q.Get(c.Context())
	if err != nil {
		return err
	}

	if c.WantsJSON() {
		return c.JSON(http.StatusOK, map[string]any{"data": posts})
	}

	data := map[string]any{"Posts": posts, "Search": c.Query("q")}

	// An htmx request gets only the list, a browser the full page.
	return c.Render("posts/index", data, web.Block("post-list"))
}`,
      },
      {
        path: "routes/web.go",
        lang: "go",
        code: `r.Get("/posts", posts.Index).Name("posts.index")`,
      },
    ],
    title: "Server-rendered pages, htmx where you want it.",
    text: "Views are [[html/template|Go's standard HTML templating package. It escapes output by default.]] files. A named block can be rendered alone, so one URL serves the page and its live fragment.",
    points: [
      "Layouts, partials and Vite assets",
      "[[htmx|A small library that swaps HTML fragments from the server, so a page updates without a JSON API.]], templ or Inertia for the frontend",
      "The same action answers HTML and JSON",
    ],
  },
  {
    id: "auth",
    label: "Auth",
    source: "examples/blog/routes/auth.go, examples/blog/app/policies/post_policy.go, security/authorization.mdx",
    files: [
      {
        path: "routes/auth.go",
        lang: "go",
        code: `package routes

import (
	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/router"

	"blog/app/accounts"
	"blog/app/controllers/account"
)

// Written by vukem install:auth; yours to change.
func Auth(r *router.Router, app *vukem.App) error {
	acc, err := accounts.New(app)
	if err != nil {
		return err
	}

	h := account.New(acc)
	guest := auth.Guest(acc.Web)
	required := auth.Required(acc.Web)
	verified := auth.Verified(acc.Web)

	r.Get("/login", h.ShowLogin).Name("login").Use(guest)
	r.Post("/login", h.Login).Use(guest)
	r.Post("/logout", h.Logout).Name("logout").Use(required)
	r.Get("/dashboard", h.Dashboard).
		Name("dashboard").
		Use(required, verified)

	return nil
}`,
      },
      {
        path: "app/policies/post_policy.go",
        lang: "go",
        code: `package policies

import (
	"context"

	"github.com/AlexGalhardo/vukem/authz"

	"blog/app/models"
)

// PostPolicy lets only the author change a post.
type PostPolicy struct{}

func (PostPolicy) Update(
	_ context.Context, u *models.User, p *models.Post,
) authz.Result {
	if p.UserID == u.ID {
		return authz.Allow()
	}

	return authz.Deny("Only the author can change this post.")
}`,
      },
      {
        path: "app/controllers/post_controller.go",
        lang: "go",
        code: `func (pc *PostController) Update(c *web.Ctx) error {
	post, err := pc.find(c)
	if err != nil {
		return err
	}

	// 403 for other users, 401 when nobody is signed in.
	if err := c.Authorize("update", &post); err != nil {
		return err
	}

	// ... validate and save the post.
	return c.RedirectRoute("posts.show", "post", post.ID)
}`,
      },
    ],
    title: "Sign-in that you own.",
    text: "vukem install:auth writes the flow into your project as code you can read and change. A [[policy|A type with one method per action, such as Update(ctx, user, post). It decides what a signed-in user may do with a record.]] decides what each user may do.",
    points: [
      "Sessions, API tokens and JWT",
      "Two-factor, passkeys and social login",
      "Policies fail closed: no rule, no access",
    ],
  },
  {
    id: "queue",
    label: "Jobs",
    source: "digging-deeper/queue.mdx",
    files: [
      {
        path: "app/jobs/send_invoice.go",
        lang: "go",
        code: `package jobs

import (
	"context"

	vukem "github.com/AlexGalhardo/vukem"
)

// SendInvoice emails the invoice of one order. Its fields are
// stored as JSON, so keep them small and serializable.
type SendInvoice struct {
	OrderID int64 \`json:"order_id"\`
}

// Kind names the job's handler; keep it stable across deploys.
func (SendInvoice) Kind() string { return "send_invoice" }

// HandleSendInvoice builds the handler from the app. Returning an
// error retries the job with backoff.
func HandleSendInvoice(
	app *vukem.App,
) func(ctx context.Context, j SendInvoice) error {
	return func(ctx context.Context, j SendInvoice) error {
		db, err := app.DB()
		if err != nil {
			return err
		}

		_, err = db.ExecContext(ctx,
			"UPDATE orders SET invoiced_at = ? WHERE id = ?",
			app.Now(), j.OrderID,
		)
		return err
	}
}`,
      },
      {
        path: "app/jobs/registry.go",
        lang: "go",
        code: `package jobs

import (
	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/queue"
)

// All lists the job handlers; vukem make:job appends here.
func All(app *vukem.App) []queue.Registration {
	return []queue.Registration{
		queue.Handle(HandleSendInvoice(app)),
	}
}`,
      },
      {
        path: "app/controllers/order_controller.go",
        lang: "go",
        code: `func (oc *OrderController) Store(c *web.Ctx) error {
	// ... insert the order, get its id ...

	q, err := oc.app.Queue()
	if err != nil {
		return err
	}

	err = q.Dispatch(c.Context(), jobs.SendInvoice{OrderID: id},
		queue.OnQueue("billing"),
		queue.Delay(5*time.Minute),
	)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusCreated, web.Map{"id": id})
}`,
      },
    ],
    title: "Background work with a struct.",
    text: "A job is a Go struct and its handler a typed function. It is stored as [[JSON|Plain text that any version of your code can read, so a deploy can pick up the jobs queued yesterday.]], so a deploy never strands the queue.",
    points: [
      "Memory, SQL and Redis drivers",
      "Retries with backoff and a failed-jobs bin",
      "Delays and named queues per dispatch",
    ],
  },
  {
    id: "mail",
    label: "Mail",
    source: "digging-deeper/mail.mdx",
    files: [
      {
        path: "app/mail/order_shipped.go",
        lang: "go",
        code: `package mail

import (
	"github.com/AlexGalhardo/vukem/mail"

	"shop/app/models"
)

// OrderShipped tells a customer their parcel is on its way.
type OrderShipped struct {
	Order models.Order
	// TrackingURL is shown as a button in the email.
	TrackingURL string
}

// Envelope implements mail.Mailable.
func (m OrderShipped) Envelope() mail.Envelope {
	return mail.Envelope{
		To:      []string{m.Order.CustomerEmail},
		Subject: "Your order has shipped",
		Tags:    map[string]string{"category": "orders"},
	}
}

// Content implements mail.Mailable.
func (m OrderShipped) Content() mail.Content {
	return mail.View("mail/order_shipped", m)
}`,
      },
      {
        path: "app/controllers/orders.go",
        lang: "go",
        code: `func (o *OrderController) Ship(c *web.Ctx) error {
	order, err := o.find(c)
	if err != nil {
		return err
	}

	// app.Mailer() builds the mailer from MAIL_* on first use.
	mailer, err := o.app.Mailer()
	if err != nil {
		return err
	}

	err = mailer.Send(c.Context(), shopmail.OrderShipped{
		Order:       order,
		TrackingURL: "https://example.com/track/" + order.Number,
	})
	if err != nil {
		return err
	}

	return c.Redirect("/orders")
}`,
      },
    ],
    title: "Describe an email once, send it anywhere.",
    text: "A mailable is a small type with an envelope and a view. An environment variable picks the [[transport|The part that delivers the message: SMTP, Resend, Mailtrap, a log line or memory.]], so sending code never changes.",
    points: [
      "SMTP, Resend, Mailtrap, log, in-memory",
      "HTML and text bodies from your views",
      "Send now, queued, or after a commit",
    ],
  },
  {
    id: "cache",
    label: "Cache",
    source: "digging-deeper/cache.mdx",
    files: [
      {
        path: "app/controllers/post_controller.go",
        lang: "go",
        code: `package controllers

import (
	"context"
	"net/http"
	"time"

	vukem "github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/cache"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"

	"blog/app/models"
)

type PostController struct {
	db    *database.DB
	cache *cache.Cache
}

// NewPostController gets the database and the cache once, at boot.
func NewPostController(app *vukem.App) (*PostController, error) {
	db, err := app.DB()
	if err != nil {
		return nil, err
	}

	c, err := app.Cache()
	if err != nil {
		return nil, err
	}

	return &PostController{db: db, cache: c}, nil
}

// Popular lists the ten most viewed posts, cached for ten minutes.
func (pc *PostController) Popular(c *web.Ctx) error {
	posts, err := cache.Remember(
		c.Context(), pc.cache, "posts:popular", 10*time.Minute,
		func(ctx context.Context) ([]models.Post, error) {
			return orm.QueryOf[models.Post](pc.db).
				OrderBy("views", orm.Desc).
				Limit(10).
				Get(ctx)
		},
	)
	if err != nil {
		return err
	}

	return c.JSON(http.StatusOK, posts)
}`,
      },
      {
        path: "routes/web.go",
        lang: "go",
        code: `posts, err := controllers.NewPostController(app)
if err != nil {
	return err
}

r.Get("/posts/popular", posts.Popular).Name("posts.popular")`,
      },
    ],
    title: "Remember the slow part.",
    text: "Wrap a query in [[Remember|cache.Remember returns the cached value when there is one. Otherwise it runs your function, stores the result and returns it.]] and the second request skips the database. The value is any Go type that encodes to JSON.",
    points: [
      "Memory, database and Redis stores",
      "Typed values with a time to live",
      "Counters and once-only locks for scheduled work",
    ],
  },
  {
    id: "realtime",
    label: "Realtime",
    source: "digging-deeper/sse.mdx",
    files: [
      {
        path: "routes/web.go",
        lang: "go",
        code: `r.Get("/orders/{number}/events", orders.Events).
	Name("orders.events")`,
      },
      {
        path: "app/controllers/order_controller.go",
        lang: "go",
        code: `func (oc *OrderController) Events(c *web.Ctx) error {
	return c.SSE(func(s *sse.Stream) error {
		t := time.NewTicker(time.Second)
		defer t.Stop()

		for {
			select {
			case <-s.Context().Done():
				// The browser left or the server is stopping.
				return nil
			case now := <-t.C:
				err := s.SendJSON("order", map[string]any{
					"number": c.Param("number"),
					"status": "shipped",
					"at":     now.Format(time.TimeOnly),
				})
				if err != nil {
					return err
				}
			}
		}
	})
}

// In the browser, standard EventSource, no library:
//
//	const source = new EventSource("/orders/1042/events");
//	source.addEventListener("order", (e) => show(e.data));`,
      },
    ],
    title: "Live updates over plain HTTP.",
    text: "[[Server-sent events|A one-way stream from server to browser over HTTP, with automatic reconnect built into EventSource.]] push changes to the page. For two-way chat and private channels, use broadcasting.",
    points: [
      "Streams that stop when the browser leaves",
      "Heartbeats and write deadlines handled for you",
      "WebSocket broadcasting for channels and chat",
    ],
  },
  {
    id: "admin",
    label: "Admin",
    source: "digging-deeper/admin.mdx",
    files: [
      {
        path: "app/admin/post.go",
        lang: "go",
        code: `// Post is the admin resource for models.Post at /admin/posts.
func Post() *vadmin.Resource[models.Post] {
	return vadmin.NewResource[models.Post](requests.PostSchema).
		Search("title", "body").
		Filter("status", "draft", "published").
		List("id", "title", "status", "created_at")
}`,
      },
      {
        path: "app/requests/post.go",
        lang: "go",
        code: `type PostInput struct {
	Title  string \`form:"title"\`
	Body   string \`form:"body"\`
	Status string \`form:"status"\`
}

// The same schema validates your forms and the panel's.
var PostSchema = z.Struct[PostInput](z.Shape{
	"title":  z.String().Trim().Min(3).Max(120),
	"body":   z.String().Max(5000).Optional(),
	"status": z.Enum("draft", "published"),
})`,
      },
      {
        path: "routes/admin.go",
        lang: "go",
        code: `package routes

import (
	"context"

	"github.com/AlexGalhardo/vukem"
	vadmin "github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/authz"
	"github.com/AlexGalhardo/vukem/router"

	"blog/app/accounts"
	"blog/app/admin"
	"blog/app/models"
)

func Admin(r *router.Router, app *vukem.App) error {
	acc, err := accounts.New(app)
	if err != nil {
		return err
	}

	admins := admin.NewAdmins(app.Source.Get("ADMIN_EMAILS"))

	// The panel has its own gate, so its policies never mix
	// with the app's.
	gate := authz.New(func(ctx context.Context) (*models.User, error) {
		u, err := auth.UserFrom[models.User](ctx, acc.Web.Name())
		return &u, err
	})
	isAdmin := func(_ context.Context, u *models.User) bool {
		return admins.Has(u)
	}
	gate.Define(vadmin.AbilityAdmin, isAdmin)

	panel, err := vadmin.New(vadmin.Config{
		DB:         acc.DB,
		Guard:      acc.Web,
		Authorizer: gate,
		Title:      app.Config.Name + " admin",
	})
	if err != nil {
		return err
	}

	if err := panel.Register(admin.Post()); err != nil {
		return err
	}

	return panel.Mount(r)
}`,
      },
    ],
    title: "An admin panel from your models.",
    text: "One resource per model, validated by the schema your forms use. Only schema keys are written: [[mass-assignment|A bug where a crafted form field changes a column the form never showed. The panel ignores any field that is not in the schema.]] protection by design.",
    points: [
      "vukem install:admin wires the panel",
      "Search, filters and list columns in code",
      "Audit log and its own authorization gate",
    ],
  },
  {
    id: "testing",
    label: "Testing",
    source: "testing/overview.mdx",
    files: [
      {
        path: "tests/post_test.go",
        lang: "go",
        code: `package tests

import (
	"net/url"
	"testing"

	"github.com/AlexGalhardo/vukem/vukemtest"

	"blog/bootstrap"
)

func TestCreatePost(t *testing.T) {
	t.Parallel()

	// Your real app, with a private in-memory database.
	app := vukemtest.New(t, bootstrap.New)
	c := app.Client(t)

	form := url.Values{
		"title": {"Hello Vukem"},
		"body":  {"First post."},
	}

	c.PostForm("/posts", form).
		AssertRedirectRoute("posts.show", "post", 1).
		AssertFlash("success", "Post created.")

	c.Get("/posts/1").
		AssertOK().
		AssertSee("Hello Vukem").
		AssertSee("Post created.")

	vukemtest.AssertDatabaseHas(t, app, "posts", map[string]any{
		"title": "Hello Vukem",
	})
}`,
      },
    ],
    title: "Test the real app, in-process.",
    text: "[[vukemtest|The testing package. It boots your bootstrap.New in a Go test and sends requests straight to the handler, with no network port.]] boots your app with its own empty database and a client that keeps cookies, like a browser.",
    points: [
      "Fluent assertions on responses, sessions and rows",
      "Fakes for mail, queues, events, cache and time",
      "Parallel tests, each with its own database",
    ],
  },
  {
    id: "cli",
    label: "CLI",
    source: "cli/overview.mdx, cli/reference-new.mdx, cli/reference-make.mdx",
    files: [
      {
        path: "terminal",
        lang: "bash",
        code: `# Scaffold a project with the Finance example, migrated and seeded
vukem new shop --example finance --seed
cd shop

# Generate code you own
vukem make:model Post -m -f
vukem make:controller Comment --resource
vukem make:job SendInvoice
vukem make:mail OrderShipped

# Wire a whole feature
vukem install:auth
vukem install:admin

# Run migrations, inspect routes, test
vukem migrate
vukem route:list
go test ./tests/...

# Live reload on every save
vukem serve`,
      },
    ],
    title: "One binary, from scaffold to live reload.",
    text: "Create a project, generate code, run with rebuilds on every save. Generators write [[ordinary Go files|No runtime magic: the generated code is yours to read, change and commit.]], not a hidden layer.",
    points: [
      "Starters: HTMX, templ, Inertia, JSON API",
      "Generators for models, jobs, mail and more",
      "Finance SaaS example: auth, billing, admin",
    ],
  },
  {
    id: "docker",
    label: "Docker",
    source: "cli/reference-new.mdx, internal/cli/prebuilt/README.md.tmpl",
    files: [
      {
        path: "terminal",
        lang: "bash",
        code: `# No Go toolchain, no source: a folder with compose.yaml,
# .env (with a fresh APP_KEY) and README.md
vukem new demo --example finance --prebuilt
cd demo

# Runs the published image with PostgreSQL and a mail catcher
docker compose up
# app:  http://localhost:3000
# mail: http://localhost:8025

# Seed the demo admin and workspace, then read the password
docker compose exec app /app/app db:seed FinanceAdminSeeder FinanceDemoSeeder
docker compose exec app sh -c "cat storage/finance-admin-*.txt"

# Stop the stack and delete its data
docker compose down -v`,
      },
    ],
    title: "Try an example without installing Go.",
    text: "--prebuilt writes a Compose file that runs the published example [[image|A packaged copy of the app. Images are published to GHCR and Docker Hub with X.Y.Z, X.Y and latest tags.]]. Drop the flag later to get the source.",
    points: [
      "Todo and Finance example images, signed in CI",
      "Ports bound to 127.0.0.1: off the network by default",
      "A demo, not a deployment: read the README first",
    ],
  },
];

export const commands = [
  "go install github.com/AlexGalhardo/vukem/cmd/vukem@latest",
  "vukem new myapp",
  "vukem new shop --example finance --seed",
];

export const highlights = [
  {
    label: "Standard library first.",
    text: "Handlers and middleware are plain [[net/http|Go's own HTTP package. Any http.Handler works inside a Vukem app.]].",
  },
  {
    label: "No magic.",
    text: "No globals or [[init()|Go runs init functions on import. Vukem registers nothing there: you wire everything in code you can read.]] registration. Generators write files you own.",
  },
  {
    label: "Batteries, opt-in.",
    text: "SQLite works out of the box with a pure-Go driver. PostgreSQL, MySQL, Redis and S3 live in separate driver modules.",
  },
  {
    label: "Secure by default.",
    text: "Security headers with [[CSP nonces|A fresh random value per response that marks which inline scripts the browser may run.]], deny-by-default CORS and boot checks that fail closed.",
  },
];

const escapeHtml = (s: string): string =>
  s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

let tipCount = 0;

/** Escapes text and turns `[[term|definition]]` into an accessible tooltip trigger. */
export function richText(text: string): string {
  return text
    .split(/(\[\[.+?\|.+?\]\])/g)
    .map((part) => {
      const m = /^\[\[(.+?)\|(.+?)\]\]$/.exec(part);
      if (!m) return escapeHtml(part);
      const id = `tip-${++tipCount}`;
      return `<span class="tip" tabindex="0" aria-describedby="${id}">${escapeHtml(m[1])}</span><span id="${id}" hidden>${escapeHtml(m[2])}</span>`;
    })
    .join("");
}

/** Plain text of a tab file for the copy button, with a comment naming each file. */
export function copyText(files: TabFile[]): string {
  if (files.length === 1) return files[0].code;
  return files
    .map((f) => {
      const header = f.lang === "html" ? `<!-- ${f.path} -->` : f.lang === "bash" ? `# ${f.path}` : `// ${f.path}`;
      return `${header}\n${f.code}`;
    })
    .join("\n\n");
}
