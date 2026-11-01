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
	"cmp"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/auth"
	"github.com/AlexGalhardo/vukem/csrf"
	"github.com/AlexGalhardo/vukem/database"
	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/flags"
	"github.com/AlexGalhardo/vukem/htmx"
	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/session"
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
	// AbilityFlags is the gate ability required to see and change feature
	// flags (Config.Flags).
	AbilityFlags = "manageFlags"

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
	// audit.html, flags.html, action.html, page.html, partials/pager.html.
	Views fs.FS
	// Flags adds a page at <Prefix>/_flags to change the global value of
	// the registered feature flags (app.Flags()); it needs the
	// AbilityFlags gate ability. Without registered flags the page and its
	// routes do not exist. Locked flags are shown read-only. Every change
	// is a "flags.changed" audit event and an audit-log entry; a save that
	// leaves the value as it was records nothing.
	Flags *flags.Flags

	// TwoFactor makes two-factor authentication mandatory for the panel:
	// it reports whether the admin authenticated on ctx (auth.UserFrom)
	// has 2FA enabled. The 2FA state lives in the app (the install:auth
	// scaffold's users.two_factor_enabled), so the panel asks. false is 403
	// with an "admin.2fa_required" security event; an error is 403 too
	// (fail closed). Nil does not require 2FA.
	TwoFactor func(ctx context.Context) (bool, error)
	// MaxSessionAge, when positive, is the oldest a session may be to use
	// the panel, counted from its creation (session.CreatedAt), a stricter
	// limit than the app's SESSION_LIFETIME. An older session is
	// invalidated (signed out of every guard, an "admin.session_expired"
	// security event) and the browser is sent to log in again. A
	// remember-me cookie logs the user back in with a new session: turn
	// remember-me off for admins when the cap must force a password.
	MaxSessionAge time.Duration
	// IdleTimeout, when positive, ends a session that has not touched a
	// guarded admin route for that long. The clock is the session value
	// "admin.idle_at", updated only by Guard (and the routes Mount
	// registers), not by the rest of the app. An idle session is
	// invalidated (an "admin.session_idle" security event) and the browser
	// is sent to log in again. Activity elsewhere does not refresh it,
	// unless those routes use TrackIdle.
	IdleTimeout time.Duration
	// AllowCIDRs, when not empty, limits the panel to client IPs
	// (web.Ctx.IP, trusted-proxy aware) inside one of the prefixes. Others
	// get 403 with an "admin.ip_denied" security event, before any login
	// redirect. The embedded CSS and JS stay public.
	AllowCIDRs []netip.Prefix
	// DenyStatus is the status AllowCIDRs answers outside the list: 0 or
	// 403 (Forbidden), or 404 to hide that the panel exists.
	DenyStatus int
	// TwoFactorRedirect, when set, is a local path ("/settings/2fa") that
	// an admin without 2FA is redirected to (303) instead of the 403. A
	// failing TwoFactor lookup stays 403 (fail closed).
	TwoFactorRedirect string
	// Now is the clock for MaxSessionAge and IdleTimeout (tests); defaults
	// to time.Now.
	Now func() time.Time
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
	pages     []customPage
	slugs     map[string]bool
	views     *view.Engine
	csrf      func(http.Handler) http.Handler
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

	if cfg.MaxSessionAge < 0 {
		return nil, errors.New("admin: Config.MaxSessionAge cannot be negative")
	}

	if cfg.IdleTimeout < 0 {
		return nil, errors.New("admin: Config.IdleTimeout cannot be negative")
	}

	if cfg.DenyStatus != 0 && cfg.DenyStatus != http.StatusForbidden && cfg.DenyStatus != http.StatusNotFound {
		return nil, fmt.Errorf("admin: Config.DenyStatus must be 403 or 404, got %d", cfg.DenyStatus)
	}

	if r := cfg.TwoFactorRedirect; r != "" && (!strings.HasPrefix(r, "/") || strings.HasPrefix(r, "//") ||
		strings.HasPrefix(r, "/\\")) {
		return nil, fmt.Errorf("admin: Config.TwoFactorRedirect must be a local path, got %q", r)
	}

	for _, p := range cfg.AllowCIDRs {
		if !p.IsValid() {
			return nil, fmt.Errorf("admin: Config.AllowCIDRs has an invalid prefix %q", p)
		}
	}

	if cfg.Now == nil {
		cfg.Now = time.Now
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

	csrfMW, err := csrf.Middleware(csrf.Config{TokenFallback: true})
	if err != nil {
		return nil, fmt.Errorf("admin: csrf: %w", err)
	}

	return &Panel{cfg: cfg, slugs: map[string]bool{}, views: views, csrf: csrfMW}, nil
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

type customPage struct {
	slug, label string
	handler     func(*web.Ctx) (template.HTML, error)
}

// AddPage adds a custom page at <Prefix>/<slug>, rendered inside the panel
// layout with the same navigation, title and guard as the resources. The
// handler returns the page body, which is trusted HTML: escape request and
// user data yourself (a template.Must(...).Execute into a buffer does).
// label is the navigation entry and the page heading. The page answers GET
// only; register other methods on the router with Panel.Guard. Call it
// before Mount. Slugs share the namespace of the resources.
func (p *Panel) AddPage(slug, label string, h func(c *web.Ctx) (template.HTML, error)) error {
	switch {
	case !slugRe.MatchString(slug):
		return fmt.Errorf("admin: page slug %q must match %s and not start with _", slug, slugRe)
	case label == "":
		return fmt.Errorf("admin: page %q needs a label", slug)
	case h == nil:
		return fmt.Errorf("admin: page %q needs a handler", slug)
	case p.slugs[slug]:
		return fmt.Errorf("admin: duplicate resource or page %q", slug)
	}

	p.slugs[slug] = true
	p.pages = append(p.pages, customPage{slug: slug, label: label, handler: h})

	return nil
}

func (p *Panel) customPage(pg customPage) func(*web.Ctx) error {
	return func(c *web.Ctx) error {
		body, err := pg.handler(c)
		if err != nil {
			return err
		}

		data := p.base(c, pg.label, pg.slug)
		data.Custom = body

		return p.render(c, http.StatusOK, "page", "", data)
	}
}

// Mount registers the panel's routes on r. Every route requires the guard,
// the panel ability and same-origin (CSRF-checked) unsafe requests.
func (p *Panel) Mount(r *router.Router) error {
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
			// The IP and session-age checks run before the guard: an unlisted
			// client never sees the login redirect, and an expired session is
			// gone before the guard reads it.
			g.Use(p.Guard())

			g.Get("", p.dashboard).Name("dashboard")
			g.Get("/_audit", p.audit).Name("audit")

			if p.hasFlags() {
				g.Get("/"+FlagsResource, p.flagsPage).Name("flags")
				g.Post("/"+FlagsResource+"/{name}", p.flagsUpdate).Name("flags.update")
			}

			for _, res := range p.resources {
				res.routes(p, g)
			}

			for _, pg := range p.pages {
				g.Get("/"+pg.slug, p.customPage(pg)).Name("page." + pg.slug)
			}
		})
	})

	return nil
}

// Guard is the middleware Mount installs on every panel route: the IP
// allow-list, MaxSessionAge, IdleTimeout, the auth guard, CSRF, the
// no-store headers, two-factor and the panel ability, in that order.
// Put it on routes registered beside the panel (a ban or resolve action
// under the same prefix) so they fail closed the same way. Sessions must
// already be on the router. It does not check a resource policy; the
// handler does that itself.
func (p *Panel) Guard() func(http.Handler) http.Handler {
	return chain(
		p.allowIP, p.maxSessionAge, p.idleTimeout, auth.Required(p.cfg.Guard), p.csrf, p.headers,
		p.requireTwoFactor, p.requireAbility,
	)
}

func chain(mw ...func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		for _, m := range slices.Backward(mw) {
			next = m(next)
		}

		return next
	}
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

func (p *Panel) allowIP(next http.Handler) http.Handler {
	if len(p.cfg.AllowCIDRs) == 0 {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := web.NewCtx(w, r, web.ServicesFrom(r.Context()))

		ip, err := netip.ParseAddr(c.IP())
		if err == nil && slices.ContainsFunc(p.cfg.AllowCIDRs, func(pr netip.Prefix) bool {
			return pr.Contains(ip.Unmap())
		}) {
			next.ServeHTTP(w, r)
			return
		}

		web.SecurityEvent(r, "admin.ip_denied", "area", "admin")
		status := cmp.Or(p.cfg.DenyStatus, http.StatusForbidden)
		c.HandleError(errs.Wrap(status, errors.New("admin: client IP not allowed")))
	})
}

// idleSessionKey is the unix-second timestamp of the last guarded request.
const idleSessionKey = "admin.idle_at"

func (p *Panel) idleTimeout(next http.Handler) http.Handler { return p.idle(next, false) }

// TrackIdle is middleware that applies IdleTimeout to the routes it wraps
// (or to the whole router) for sessions that have used the panel: their
// activity there refreshes the idle clock, and an idle session is ended
// there too, so an admin keeps one clock across the app. Sessions that
// never reached the panel are left untouched. Without IdleTimeout it does
// nothing.
func (p *Panel) TrackIdle() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler { return p.idle(next, true) }
}

func (p *Panel) idle(next http.Handler, trackedOnly bool) http.Handler {
	if p.cfg.IdleTimeout == 0 {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := web.SessionFrom(r.Context())
		now := p.cfg.Now()

		if s != nil && (!trackedOnly || s.Get(idleSessionKey) != nil) {
			if last, ok := sessionUnix(s.Get(idleSessionKey)); ok && now.Sub(last) > p.cfg.IdleTimeout {
				web.SecurityEvent(r, "admin.session_idle", "area", "admin")
				s.Invalidate()
			} else {
				s.Put(idleSessionKey, now.Unix())
			}
		}

		next.ServeHTTP(w, r)
	})
}

func sessionUnix(v any) (time.Time, bool) {
	var n int64

	switch x := v.(type) {
	case int64:
		n = x
	case int:
		n = int64(x)
	case json.Number:
		var err error

		n, err = x.Int64()
		if err != nil {
			return time.Time{}, false
		}
	case float64:
		if x != float64(int64(x)) {
			return time.Time{}, false
		}

		n = int64(x)
	default:
		return time.Time{}, false
	}

	return time.Unix(n, 0), true
}

func (p *Panel) maxSessionAge(next http.Handler) http.Handler {
	if p.cfg.MaxSessionAge == 0 {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s := web.SessionFrom(r.Context())
		if created, ok := session.CreatedAt(s); ok && p.cfg.Now().Sub(created) > p.cfg.MaxSessionAge {
			web.SecurityEvent(r, "admin.session_expired", "area", "admin")
			s.Invalidate()
		}

		next.ServeHTTP(w, r)
	})
}

func (p *Panel) requireTwoFactor(next http.Handler) http.Handler {
	if p.cfg.TwoFactor == nil {
		return next
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		on, err := p.cfg.TwoFactor(r.Context())
		if err == nil && on {
			next.ServeHTTP(w, r)
			return
		}

		c := web.NewCtx(w, r, web.ServicesFrom(r.Context()))
		web.SecurityEvent(r, "admin.2fa_required", "area", "admin")

		if err != nil {
			c.Log().ErrorContext(r.Context(), "admin: two-factor check failed", "err", err)
		} else if p.cfg.TwoFactorRedirect != "" {
			http.Redirect(w, r, p.cfg.TwoFactorRedirect, http.StatusSeeOther)

			return
		}

		c.HandleError(errs.Wrap(http.StatusForbidden, errors.New("admin: two-factor authentication required")))
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
	Flags    *flagsView
	Action   *actionView
	Cards    []card
	Custom   template.HTML
}

type panelView struct {
	Title, Home, CSS, JS, AuditURL, FlagsURL string
	Nav                                      []navItem
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

	for _, pg := range p.pages {
		pv.Nav = append(pv.Nav, navItem{Label: pg.label, URL: p.url(pg.slug), Active: pg.slug == active})
	}

	if p.can(c, AbilityAudit, nil) {
		pv.AuditURL = p.url("_audit")
	}

	if p.hasFlags() && p.can(c, AbilityFlags, nil) {
		pv.FlagsURL = p.url(FlagsResource)
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
	if res := c.Query("resource"); res != "" && (p.slugs[res] || res == FlagsResource) {
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
			row.Changes = append(row.Changes, changeRows(col, e.Changes.V[col])...)
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
