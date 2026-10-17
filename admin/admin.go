// Package admin is an admin panel generated from ORM models: paginated
// lists with search, sort and filters, detail pages, create/edit forms
// built from z schemas, deletes with confirmation, and an audit log of
// every change. The UI is server-rendered html/template with htmx, all
// embedded (no CDN, no inline scripts or styles, so it runs under a strict
// CSP).
//
// Access fails closed: every route requires an authenticated user (the
// guard in Config), the panel ability (default "admin") and the policy
// ability for the action (viewAny, view, create, update, delete) on the
// model type. A missing ability or policy denies.
//
//	panel, err := admin.New(admin.Config{DB: db, Guard: acc.Web, Authorizer: gate})
//	if err != nil { return err }
//	if err := panel.Register(admin.NewResource[models.Post](requests.PostSchema).Search("title")); err != nil {
//		return err
//	}
//	return panel.Mount(r)
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/admin.md.
package admin

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/csrf"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/htmx"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/view"
	"github.com/AlexGalhardo/vukem/web"
)

//go:embed templates
var templatesFS embed.FS

//go:embed assets
var assetsFS embed.FS

// Abilities checked by the panel. Policy abilities are checked on the
// model type ((*T)(nil)) or on the record (*T).
const (
	// AbilityAdmin is the default gate ability required for every page.
	AbilityAdmin = "admin"
	// AbilityAudit is the gate ability required to read the audit log.
	AbilityAudit = "viewAuditLogs"

	AbilityViewAny = "viewAny"
	AbilityView    = "view"
	AbilityCreate  = "create"
	AbilityUpdate  = "update"
	AbilityDelete  = "delete"
)

// FlashKey is the session flash key of the panel's success messages.
const FlashKey = "admin.success"

// strictCSP is set on admin responses when the app sent no policy: the UI
// needs nothing inline.
const strictCSP = "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data:; " +
	"form-action 'self'; frame-ancestors 'none'; base-uri 'none'; object-src 'none'"

// Config configures a Panel.
type Config struct {
	// DB stores the resources and the audit log (required).
	DB *database.DB
	// Guard authenticates admins (required), e.g. the "web" session guard.
	// Guests are redirected to the route named "login" (401 for JSON).
	Guard auth.Checker
	// Authorizer decides the panel and policy abilities, usually an
	// authz.Gate. Nil uses the router's Services.Authorizer; with neither,
	// everything is denied.
	Authorizer web.Authorizer
	// Prefix is the URL prefix (default "/admin").
	Prefix string
	// Ability is the gate ability required for every page (default
	// "admin").
	Ability string
	// Title is shown in the header (default "Admin").
	Title string
	// Views overrides embedded templates by path: layouts/admin.html,
	// index.html, show.html, form.html, delete.html, dashboard.html,
	// audit.html.
	Views fs.FS
}

// Templates returns the embedded templates, to copy one into Config.Views
// and change it.
func Templates() fs.FS {
	sub, _ := fs.Sub(templatesFS, "templates") // the directory is embedded, so Sub cannot fail
	return sub
}

// Panel is an admin panel; build it with New, add resources with Register
// and serve it with Mount.
type Panel struct {
	cfg       Config
	resources []AnyResource
	slugs     map[string]bool
	views     *view.Engine
}

// New validates cfg and returns an empty panel.
func New(cfg Config) (*Panel, error) {
	if cfg.DB == nil {
		return nil, errors.New("admin: Config.DB is required")
	}

	if cfg.Guard == nil {
		return nil, errors.New("admin: Config.Guard is required (the panel never runs without authentication)")
	}

	if cfg.Prefix == "" {
		cfg.Prefix = "/admin"
	}

	cfg.Prefix = "/" + strings.Trim(cfg.Prefix, "/")
	if cfg.Prefix == "/" {
		return nil, errors.New("admin: Config.Prefix cannot be the site root")
	}

	if cfg.Ability == "" {
		cfg.Ability = AbilityAdmin
	}

	if cfg.Title == "" {
		cfg.Title = "Admin"
	}

	sub := Templates()
	fsys := sub
	if cfg.Views != nil {
		fsys = overlay{top: cfg.Views, base: sub}
	}

	views, err := view.New(view.Config{FS: fsys, DefaultLayout: "admin"})
	if err != nil {
		return nil, fmt.Errorf("admin: templates: %w", err)
	}

	return &Panel{cfg: cfg, slugs: map[string]bool{}, views: views}, nil
}

// Register validates and adds resources; configuration mistakes are
// returned here, at boot.
func (p *Panel) Register(resources ...AnyResource) error {
	var errsOut []error

	for _, r := range resources {
		if err := r.check(); err != nil {
			errsOut = append(errsOut, err)
			continue
		}

		slug := r.info().slug
		if p.slugs[slug] {
			errsOut = append(errsOut, fmt.Errorf("admin: duplicate resource %q", slug))
			continue
		}

		p.slugs[slug] = true
		p.resources = append(p.resources, r)
	}

	return errors.Join(errsOut...)
}

// Mount registers the panel's routes on r. Every route requires the guard,
// the panel ability and same-origin (CSRF-checked) unsafe requests.
func (p *Panel) Mount(r *router.Router) error {
	csrfMW, err := csrf.Middleware(csrf.Config{TokenFallback: true})
	if err != nil {
		return err
	}

	assets, err := fs.Sub(assetsFS, "assets")
	if err != nil {
		return err
	}

	r.Group(p.cfg.Prefix, func(g *router.Group) {
		g.Name("admin.")

		for _, name := range []string{"admin.css", "htmx.min.js"} {
			g.Get("/_assets/"+name, func(c *web.Ctx) error {
				c.SetHeader("Cache-Control", "public, max-age=3600")
				return c.File(assets, name)
			})
		}

		g.Group("", func(g *router.Group) {
			g.Use(auth.Required(p.cfg.Guard), csrfMW, p.headers, p.requireAbility)

			g.Get("", p.dashboard).Name("dashboard")
			g.Get("/_audit", p.audit).Name("audit")

			for _, res := range p.resources {
				res.routes(p, g)
			}
		})
	})

	return nil
}

func (p *Panel) url(parts ...string) string {
	return p.cfg.Prefix + "/" + strings.Join(parts, "/")
}

// headers keeps admin pages out of caches and adds a strict CSP when the
// app sent none.
func (p *Panel) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Cache-Control", "no-store")

		if h.Get("Content-Security-Policy") == "" {
			h.Set("Content-Security-Policy", strictCSP)
		}

		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

func (p *Panel) requireAbility(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := web.NewCtx(w, r, web.ServicesFrom(r.Context()))
		if err := p.authorize(c, p.cfg.Ability, nil); err != nil {
			c.HandleError(err)
			return
		}

		next.ServeHTTP(w, c.Request())
	})
}

func (p *Panel) authorizer(c *web.Ctx) web.Authorizer {
	if p.cfg.Authorizer != nil {
		return p.cfg.Authorizer
	}

	return c.Services().Authorizer
}

// authorize checks ability and logs denials as "authz.denied" security
// events. Without an authorizer it denies (fail closed).
func (p *Panel) authorize(c *web.Ctx, ability string, subject any) error {
	err := p.allowed(c.Context(), p.authorizer(c), ability, subject)
	if err != nil {
		web.SecurityEvent(c.Request(), "authz.denied", "ability", ability, "status", errs.Status(err),
			"area", "admin")
	}

	return err
}

func (p *Panel) can(c *web.Ctx, ability string, subject any) bool {
	return p.allowed(c.Context(), p.authorizer(c), ability, subject) == nil
}

func (p *Panel) allowed(ctx context.Context, a web.Authorizer, ability string, subject any) error {
	if a == nil {
		return errs.Wrap(http.StatusForbidden, errors.New("admin: no Authorizer configured"))
	}

	return a.Authorize(ctx, ability, subject)
}

// actor returns the AuthID of the user the guard authenticated.
func (p *Panel) actor(ctx context.Context) string {
	if u, err := auth.UserFrom[auth.Authenticatable](ctx, p.cfg.Guard.Name()); err == nil {
		return u.AuthID()
	}

	return ""
}

// page is the data every template receives.
type page struct {
	Title    string
	Panel    panelView
	Resource *resourceView
	List     *listView
	Record   *recordView
	Form     *formView
	Audit    *auditView
	Cards    []card
}

type panelView struct {
	Title, Home, CSS, JS, AuditURL string
	Nav                            []navItem
}

type navItem struct {
	Label, URL string
	Active     bool
}

type card struct {
	Label, URL string
	Count      int64
}

func (p *Panel) base(c *web.Ctx, title, active string) page {
	pv := panelView{
		Title: p.cfg.Title,
		Home:  p.cfg.Prefix,
		CSS:   p.url("_assets", "admin.css"),
		JS:    p.url("_assets", "htmx.min.js"),
	}

	for _, r := range p.resources {
		b := r.info()
		if p.can(c, AbilityViewAny, r.subject()) {
			pv.Nav = append(pv.Nav, navItem{Label: b.plural, URL: p.url(b.slug), Active: b.slug == active})
		}
	}

	if p.can(c, AbilityAudit, nil) {
		pv.AuditURL = p.url("_audit")
	}

	return page{Title: title, Panel: pv}
}

// render writes a template page, or only block for htmx partial requests.
func (p *Panel) render(c *web.Ctx, status int, name, block string, data page) error {
	var buf bytes.Buffer

	var err error

	if block != "" {
		htmx.NewResponse(c.Writer()) // Vary: HX-Request

		if htmx.IsPartial(c.Request()) {
			err = p.views.RenderBlock(c.Context(), &buf, name, block, data)
		} else {
			err = p.views.Render(c.Context(), &buf, name, data)
		}
	} else {
		err = p.views.Render(c.Context(), &buf, name, data)
	}

	if err != nil {
		return errs.Wrap(http.StatusInternalServerError, err)
	}

	return c.Blob(status, "text/html; charset=utf-8", buf.Bytes())
}

func (p *Panel) dashboard(c *web.Ctx) error {
	data := p.base(c, "Dashboard", "")

	for _, r := range p.resources {
		if !p.can(c, AbilityViewAny, r.subject()) {
			continue
		}

		n, err := r.count(c.Context(), p.cfg.DB)
		if err != nil {
			return err
		}

		b := r.info()
		data.Cards = append(data.Cards, card{Label: b.plural, URL: p.url(b.slug), Count: n})
	}

	return p.render(c, http.StatusOK, "dashboard", "", data)
}

type auditView struct {
	Entries []auditRow
	Pager   pager
}

type auditRow struct {
	When, Actor, Action, Resource, RecordID string
	Changes                                 []changeRow
}

type changeRow struct {
	Column, Before, After string
	Redacted              bool
}

func (p *Panel) audit(c *web.Ctx) error {
	if err := p.authorize(c, AbilityAudit, nil); err != nil {
		return err
	}

	q := orm.QueryOf[AuditLog](p.cfg.DB).OrderBy("id", orm.Desc)
	if res := c.Query("resource"); res != "" && p.slugs[res] {
		q = q.Where("resource", res)
	}

	pg, err := q.Paginate(c.Context(), max(c.QueryInt("page", 1), 1), 50)
	if err != nil {
		return err
	}

	av := &auditView{Pager: newPager(p.url("_audit"), c.Request().URL.Query(), pg.Page, pg.LastPage)}

	for _, e := range pg.Items {
		row := auditRow{
			When: e.CreatedAt.Format(displayTime), Actor: e.ActorID, Action: e.Action,
			Resource: e.Resource, RecordID: e.RecordID,
		}

		for _, col := range sortedKeys(e.Changes.V) {
			ch := e.Changes.V[col]
			row.Changes = append(row.Changes, changeRow{
				Column: col, Before: fmtAny(ch.Before), After: fmtAny(ch.After), Redacted: ch.Redacted,
			})
		}

		av.Entries = append(av.Entries, row)
	}

	data := p.base(c, "Audit log", "")
	data.Audit = av

	return p.render(c, http.StatusOK, "audit", "list", data)
}

func fmtAny(v any) string {
	if v == nil {
		return "∅"
	}

	return truncate(fmt.Sprint(v), listMaxRune)
}
