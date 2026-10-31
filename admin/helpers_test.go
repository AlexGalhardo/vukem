package admin_test

import (
	"context"
	"io/fs"
	"strconv"
	"testing"

	"github.com/AlexGalhardo/vukem"
	"github.com/AlexGalhardo/vukem/admin"
	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/auth/hash"
	"github.com/AlexGalhardo/vukem/authz"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/schema"
	"github.com/AlexGalhardo/vukem/vukemtest"
	"github.com/AlexGalhardo/vukem/web"
	"github.com/AlexGalhardo/vukem/z"
)

type User struct {
	orm.Model

	Email    string
	Password string `json:"-"`
	Role     string
}

func (u User) AuthID() string       { return strconv.FormatInt(u.ID, 10) }
func (u User) AuthPassword() string { return u.Password }

type Post struct {
	orm.Model

	Title     string `json:"title"`
	Body      string `json:"body"`
	Status    string `json:"status"`
	Published bool   `json:"published"`
	AuthorID  int64  `json:"author_id"`
}

type PostInput struct {
	Title     string `form:"title"`
	Body      string `form:"body"`
	Status    string `form:"status"`
	Published bool   `form:"published"`
}

var postSchema = z.Struct[PostInput](z.Shape{
	"title":     z.String().Trim().Min(3).Max(120),
	"body":      z.String().Max(5000).Optional(),
	"status":    z.Enum("draft", "live"),
	"published": z.Bool().Optional(),
})

// Account has secrets: Password (json:"-") and APIToken (by name).
type Account struct {
	orm.Model

	Email    string `json:"email"`
	Password string `json:"-"`
	APIToken string `json:"api_token"`
}

type AccountInput struct {
	Email    string `form:"email"`
	Password string `form:"password"`
	APIToken string `form:"api_token"`
}

var accountSchema = z.Struct[AccountInput](z.Shape{
	"email":     z.String().Email(),
	"password":  z.String().Min(8).Optional(),
	"api_token": z.String().Optional(),
})

// Note has no registered policy: every action must be denied.
type Note struct {
	orm.Model

	Text string `json:"text"`
}

type NoteInput struct {
	Text string `form:"text"`
}

// adminPolicy lets admins do everything and denies everyone else.
type adminPolicy[T any] struct{}

func (adminPolicy[T]) ViewAny(_ context.Context, u *User) bool { return u.Role == "admin" }
func (adminPolicy[T]) Create(_ context.Context, u *User) bool  { return u.Role == "admin" }
func (adminPolicy[T]) View(_ context.Context, u *User, _ T) bool {
	return u.Role == "admin"
}

func (adminPolicy[T]) Update(_ context.Context, u *User, _ T) bool {
	return u.Role == "admin"
}

func (adminPolicy[T]) Delete(_ context.Context, u *User, _ T) bool {
	return u.Role == "admin"
}

// postPolicy adds the custom actions of the post resource; a post titled
// "Locked" cannot be approved.
type postPolicy struct {
	adminPolicy[*Post]

	// onUpdate runs between the panel loading the row and its transaction:
	// the window where another writer can change the row.
	onUpdate func(context.Context)
}

func (p postPolicy) Update(ctx context.Context, u *User, post *Post) bool {
	if p.onUpdate != nil {
		p.onUpdate(ctx)
	}

	return p.adminPolicy.Update(ctx, u, post)
}

func (postPolicy) Approve(_ context.Context, u *User, p *Post) bool {
	return u.Role == "admin" && p.Title != "Locked"
}

func (postPolicy) Reject(_ context.Context, u *User, _ *Post) bool { return u.Role == "admin" }

var migrations = []schema.Migration{
	{
		ID: "1_tables",
		Up: func(s *schema.Schema) error {
			for _, step := range []func() error{
				func() error {
					return s.Create("users", func(t *schema.Table) {
						t.ID()
						t.String("email").Unique()
						t.String("password")
						t.String("role")
						t.Timestamps()
					})
				},
				func() error {
					return s.Create("posts", func(t *schema.Table) {
						t.ID()
						t.String("title")
						t.Text("body")
						t.String("status")
						t.Boolean("published").Default(false)
						t.BigInteger("author_id").Default(0)
						t.Timestamps()
					})
				},
				func() error {
					return s.Create("accounts", func(t *schema.Table) {
						t.ID()
						t.String("email")
						t.String("password")
						t.String("api_token")
						t.Timestamps()
					})
				},
				func() error {
					return s.Create("notes", func(t *schema.Table) {
						t.ID()
						t.String("text")
						t.Timestamps()
					})
				},
				func() error {
					return s.Create("pairs", func(t *schema.Table) {
						t.BigInteger("user_id")
						t.BigInteger("item_id")
						t.String("note")
						t.Primary("user_id", "item_id")
					})
				},
				func() error {
					return s.Create("tag_pairs", func(t *schema.Table) {
						t.String("scope")
						t.String("name")
						t.String("label")
						t.Primary("scope", "name")
					})
				},
			} {
				if err := step(); err != nil {
					return err
				}
			}

			return nil
		},
		Down: func(*schema.Schema) error { return nil },
	},
	admin.Migration("2_create_admin_audit_logs_table"),
	flags.Migration("3_create_feature_flags_table"),
}

type options struct {
	views    fs.FS
	noGate   bool
	resource func() []admin.AnyResource
	// flags registers these feature flags and passes app.Flags() to the panel.
	flags []flags.AnyFlag
	// harden edits the panel config (TwoFactor, MaxSessionAge, AllowCIDRs).
	harden func(*admin.Config)
	// extra registers policies for resources the default gate does not know.
	extra func(*authz.Gate[*User]) error
	// racing runs when the panel authorizes a post update, with the panel's DB.
	racing func(context.Context, *database.DB)
	// pages registers custom pages before Mount.
	pages func(*admin.Panel) error
	// beside registers routes next to the panel, after Mount.
	beside func(*router.Router, *admin.Panel)
	// clock drives the app (sessions) and the panel.
	clock *vukemtest.FakeClock
}

func resources() []admin.AnyResource {
	return []admin.AnyResource{
		admin.NewResource[Post](postSchema).
			Search("title", "body").
			Sortable("id", "title", "created_at").
			Filter("status", "draft", "live").
			Filter("published").
			List("id", "title", "status", "published").
			Column("Words", func(p Post) string { return strconv.Itoa(len(p.Body)) }).
			PerPage(2),
		admin.NewResource[Account](accountSchema),
		admin.NewResource[Note](z.Struct[NoteInput](z.Shape{"text": z.String()})),
	}
}

func newApp(t *testing.T, o options) *vukemtest.App {
	t.Helper()

	build := func(opts ...vukem.Option) (*vukem.App, error) {
		return vukem.New(append(opts, vukem.WithMigrations(migrations), vukem.WithFlags(o.flags...),
			vukem.WithRoutes(func(r *router.Router, app *vukem.App) error {
				if err := vukem.DefaultMiddleware(app, r); err != nil {
					return err
				}

				db, err := app.DB()
				if err != nil {
					return err
				}

				guard := auth.NewSessionGuard("web", auth.NewORMProvider[User](db), hash.Fake(), nil)
				guard.InsecureCookies = true

				r.Get("/login", func(c *web.Ctx) error { return c.String(200, "login") }).Name("login")

				r.Get("/whoami", func(c *web.Ctx) error {
					if !guard.Check(c) {
						return c.String(200, "guest")
					}

					u, err := auth.UserFrom[User](c.Context(), "web")
					if err != nil {
						return err
					}

					return c.String(200, "user="+u.Email)
				})

				cfg := admin.Config{DB: db, Guard: guard, Views: o.views}
				if o.clock != nil {
					cfg.Now = o.clock.Now
				}

				if o.harden != nil {
					o.harden(&cfg)
				}

				if o.flags != nil {
					if cfg.Flags, err = app.Flags(); err != nil {
						return err
					}
				}

				if !o.noGate {
					gate := authz.New(func(ctx context.Context) (*User, error) {
						u, err := auth.UserFrom[User](ctx, "web")
						return &u, err
					})
					gate.Define(admin.AbilityAdmin, func(_ context.Context, u *User) bool {
						return u.Role == "admin" || u.Role == "editor"
					})
					gate.Define(admin.AbilityAudit, func(_ context.Context, u *User) bool { return u.Role == "admin" })
					gate.Define(admin.AbilityFlags, func(_ context.Context, u *User) bool { return u.Role == "admin" })

					if err := gate.Register(
						authz.For[*Post](postPolicy{onUpdate: func(ctx context.Context) {
							if o.racing != nil {
								o.racing(ctx, db)
							}
						}}),
						authz.For[*Account](adminPolicy[*Account]{}),
					); err != nil {
						return err
					}

					if o.extra != nil {
						if err := o.extra(gate); err != nil {
							return err
						}
					}

					cfg.Authorizer = gate
				}

				panel, err := admin.New(cfg)
				if err != nil {
					return err
				}

				rs := resources
				if o.resource != nil {
					rs = o.resource
				}

				if err := panel.Register(rs()...); err != nil {
					return err
				}

				if o.pages != nil {
					if err := o.pages(panel); err != nil {
						return err
					}
				}

				if err := panel.Mount(r); err != nil {
					return err
				}

				if o.beside != nil {
					o.beside(r, panel)
				}

				return nil
			}))...)
	}

	var vo vukemtest.Options
	if o.clock != nil {
		vo.Clock = o.clock.Now
	}

	return vukemtest.New(t, build, vo)
}

func createUser(t *testing.T, app *vukemtest.App, email, role string) User {
	t.Helper()

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	u := User{Email: email, Password: "x", Role: role}
	if err := orm.Create(t.Context(), db, &u); err != nil {
		t.Fatal(err)
	}

	return u
}

// as returns a same-origin browser client logged in as a new user with role.
func as(t *testing.T, app *vukemtest.App, role string) *vukemtest.Client {
	t.Helper()

	u := createUser(t, app, role+"@example.com", role)

	return app.Client(t).WithHeader("Sec-Fetch-Site", "same-origin").ActingAs("web", u)
}

func createPost(t *testing.T, app *vukemtest.App, p Post) {
	t.Helper()

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	if err := orm.Create(t.Context(), db, &p); err != nil {
		t.Fatal(err)
	}
}

func auditLogs(t *testing.T, app *vukemtest.App) []admin.AuditLog {
	t.Helper()

	db, err := app.DB()
	if err != nil {
		t.Fatal(err)
	}

	logs, err := orm.QueryOf[admin.AuditLog](db).OrderBy("id", orm.Asc).Get(t.Context())
	if err != nil {
		t.Fatal(err)
	}

	return logs
}

// PostWithID tries to write the primary key through the form.
type PostWithID struct {
	ID    int64  `form:"id"`
	Title string `form:"title"`
}

var postWithIDSchema = z.Struct[PostWithID](z.Shape{"id": z.Int64(), "title": z.String()})

func newPanel(t *testing.T) *admin.Panel {
	t.Helper()

	db, err := database.Open(t.Context(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}

	t.Cleanup(func() { _ = db.Close() })

	guard := auth.NewSessionGuard("web", auth.NewORMProvider[User](db), hash.Fake(), nil)

	p, err := admin.New(admin.Config{DB: db, Guard: guard})
	if err != nil {
		t.Fatal(err)
	}

	return p
}
