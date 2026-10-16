package devtools

import (
	"cmp"
	"crypto/rand"
	"embed"
	"html/template"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/middleware"
	"github.com/AlexGalhardo/vukem/openapi"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/web"
)

//go:embed templates/*.html
var templateFS embed.FS

var pages = template.Must(template.New("").Funcs(template.FuncMap{
	"ms":     millis,
	"join":   strings.Join,
	"clock":  func(t time.Time) string { return t.Format("15:04:05.000") },
	"status": statusClass,
}).ParseFS(templateFS, "templates/*.html"))

// pageCSP is used when the app has no SecureHeaders middleware: no
// scripts, nonce'd styles only.
const pageCSP = "default-src 'none'; style-src 'nonce-{nonce}'; img-src 'self' data:; base-uri 'none'; " +
	"form-action 'self'; frame-ancestors 'none'"

// mailCSP is the mail preview page's policy. The message renders in a
// sandboxed srcdoc iframe, which inherits this policy: inline styles are
// allowed so messages look right, scripts are not allowed at all, and
// remote images (tracking pixels) are blocked.
const mailCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src 'self' data:; frame-src 'self'; " +
	"base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// TimelineItem is one entry of a request trace.
type TimelineItem struct {
	Offset time.Duration
	Kind   string
	Text   string
	Detail string
	Link   string
	Flag   bool
}

// Trace is a request with everything recorded during it, in time order.
type Trace struct {
	Request Request
	Items   []TimelineItem
}

// Trace returns the request with trace id id and its timeline.
func (d *Devtools) Trace(id string) (Trace, bool) {
	var (
		t     Trace
		found bool
	)

	rs := d.requests.items()
	for i := range rs {
		r := &rs[i]
		if r.ID == id {
			t.Request, found = *r, true
			break
		}
	}

	if !found {
		return Trace{}, false
	}

	start := t.Request.Start
	add := func(at time.Time, kind, text, detail, link string, flag bool) {
		t.Items = append(t.Items, TimelineItem{
			Offset: max(at.Sub(start), 0), Kind: kind, Text: text, Detail: detail, Link: link, Flag: flag,
		})
	}

	qs := d.queries.items()
	for i := range qs {
		q := &qs[i]
		if q.TraceID == id {
			add(q.Time, "query", q.SQL, millis(q.Duration)+" "+q.Caller+" "+q.Err, "", q.NPlusOne)
		}
	}

	for _, l := range d.logs.items() {
		if l.TraceID == id {
			add(l.Time, "log", l.Level+" "+l.Message, l.Attrs, "", false)
		}
	}

	js := d.jobs.items()
	for i := range js {
		j := &js[i]
		if j.TraceID == id {
			add(j.Time, "job", j.Kind+" "+j.Status, j.Err, "", j.Err != "")
		}
	}

	for _, e := range d.events.items() {
		if e.TraceID == id {
			add(e.Time, "event", e.Type, strconv.Itoa(e.Listeners)+" listeners "+e.Err, "", e.Err != "")
		}
	}

	ms := d.mails.items()
	for i := range ms {
		m := &ms[i]
		if m.TraceID == id {
			add(m.Time, "mail", m.Subject, strings.Join(m.To, ", "), Path+"/mails/"+m.ID, m.Err != "")
		}
	}

	slices.SortStableFunc(t.Items, func(a, b TimelineItem) int { return cmp.Compare(a.Offset, b.Offset) })

	return t, true
}

func millis(d time.Duration) string {
	return strconv.FormatFloat(float64(d.Microseconds())/1000, 'f', 2, 64) + " ms"
}

type pageData struct {
	Title   string
	Nav     string
	Nonce   string
	Refresh int
	Self    string
	Data    any
}

// Register adds the dashboard routes under Path, guarded by Allowed.
func (d *Devtools) Register(r *router.Router) {
	r.Group(Path, func(g *router.Group) {
		g.Use(d.guard)
		g.Name("devtools.")

		page := func(path, name, nav, title string, data func(c *web.Ctx) (any, error)) {
			g.Get(path, func(c *web.Ctx) error {
				v, err := data(c)
				if err != nil {
					return err
				}

				return d.render(c, name, pageData{Title: title, Nav: nav, Data: v})
			}).Name(name).Doc(openapi.Skip{})
		}

		page("/", "overview", "overview", "Overview", func(*web.Ctx) (any, error) { return d.overview(), nil })
		page("/requests", "requests", "requests", "Requests", func(*web.Ctx) (any, error) { return d.Requests(), nil })
		page("/requests/{id}", "trace", "requests", "Request", func(c *web.Ctx) (any, error) {
			t, ok := d.Trace(c.Param("id"))
			if !ok {
				return nil, errs.ErrNotFound
			}

			return t, nil
		})
		page("/queries", "queries", "queries", "Queries", func(*web.Ctx) (any, error) { return d.Queries(), nil })
		page("/jobs", "jobs", "jobs", "Jobs", func(*web.Ctx) (any, error) { return d.Jobs(), nil })
		page("/events", "events", "events", "Events", func(*web.Ctx) (any, error) { return d.Events(), nil })
		page("/mails", "mails", "mails", "Mails", func(*web.Ctx) (any, error) { return d.Mails(), nil })
		page("/logs", "logs", "logs", "Logs", func(*web.Ctx) (any, error) { return d.Logs(), nil })
		page("/routes", "routes", "routes", "Routes", func(*web.Ctx) (any, error) { return d.routes(), nil })

		g.Get("/mails/{id}", func(c *web.Ctx) error {
			ms := d.Mails()
			for i := range ms {
				m := &ms[i]
				if m.ID == c.Param("id") {
					c.SetHeader("Content-Security-Policy", mailCSP)
					return d.render(c, "mail", pageData{Title: m.Subject, Nav: "mails", Data: *m})
				}
			}

			return errs.ErrNotFound
		}).Name("mail").Doc(openapi.Skip{})
	})
}

func (d *Devtools) render(c *web.Ctx, name string, p pageData) error {
	h := c.Writer().Header()

	p.Nonce = middleware.CSPNonce(c.Context())
	if p.Nonce == "" {
		p.Nonce = rand.Text()

		if h.Get("Content-Security-Policy") == "" {
			h.Set("Content-Security-Policy", strings.ReplaceAll(pageCSP, "{nonce}", p.Nonce))
		}
	}

	if n := c.QueryInt("refresh", 0); n > 0 {
		p.Refresh = min(max(n, 2), 60)
	}

	p.Self = c.Request().URL.Path

	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex")

	return pages.ExecuteTemplate(c.Writer(), name, p)
}

type overview struct {
	Counts   []count
	Recent   []Request
	NPlusOne []Request
}

type count struct {
	Name, Link string
	N          int
}

func (d *Devtools) overview() overview {
	reqs := d.Requests()
	o := overview{
		Counts: []count{
			{"Requests", "requests", len(reqs)},
			{"Queries", "queries", d.queries.len()},
			{"Jobs", "jobs", d.jobs.len()},
			{"Events", "events", d.events.len()},
			{"Mails", "mails", d.mails.len()},
			{"Logs", "logs", d.logs.len()},
		},
		Recent: reqs[:min(len(reqs), 10)],
	}

	rs := reqs
	for i := range rs {
		r := &rs[i]
		if len(r.NPlusOne) > 0 {
			o.NPlusOne = append(o.NPlusOne, *r)
		}
	}

	return o
}

func (d *Devtools) routes() []router.RouteInfo {
	if d.cfg.Routes == nil {
		return nil
	}

	return slices.DeleteFunc(d.cfg.Routes(), func(rt router.RouteInfo) bool { return isDashboard(rt.Path) })
}

func statusClass(code int) string {
	switch {
	case code >= 500:
		return "err"
	case code >= 400:
		return "warn"
	default:
		return "ok"
	}
}
