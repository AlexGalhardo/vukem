package inertia

import (
	"cmp"
	"encoding/json"
	"slices"

	"github.com/AlexGalhardo/vukem/web"
)

// ErrorPage renders Config.ErrorComponent for an Inertia request, so the
// client shows an error page instead of the raw HTML response in a modal;
// with Config.ErrorFullPages it does the same for full page loads.
// The props are status, title and message (e.Detail, or e.Title when it is
// empty), plus the shared props; e.Errors (a 4xx validation error) becomes
// the errors prop, first message per field, over any flashed errors. It is
// a web.ErrorPageFunc: install it as web.Services.ErrorPage
// (vukem.WithInertia does). It writes nothing for other requests (full page
// loads keep the HTML status page), for statuses outside 4xx/5xx, or when
// the page props fail to resolve or encode.
func (in *Inertia) ErrorPage(c *web.Ctx, e web.ErrorPage) bool {
	full := !IsInertia(c.Request())
	if e.Status < 400 || e.Status > 599 || full && !in.cfg.ErrorFullPages {
		return false
	}

	// Unmatched routes (404/405) never run route middleware.
	st := stateFrom(c.Context())
	if st == nil || st.in != in {
		st = &state{in: in, shared: Props{}}
	}

	props := Props{"status": e.Status, "title": e.Title, "message": cmp.Or(e.Detail, e.Title)}

	p, err := st.page(c, in.cfg.ErrorComponent, props)
	if err != nil {
		c.Log().ErrorContext(c.Context(), "inertia: render error page", "status", e.Status, "err", err)
		return false
	}

	if len(e.Errors) > 0 {
		fields, _ := p.Props["errors"].(map[string]any)
		if fields == nil {
			fields = map[string]any{}
		}

		for field, msgs := range e.Errors {
			if len(msgs) > 0 {
				fields[field] = msgs[0]
			}
		}

		p.Props["errors"] = fields
	}

	if full {
		c.SetHeader("Cache-Control", "no-store")

		if err := in.renderRoot(c, st, p, e.Status); err != nil {
			c.Log().ErrorContext(c.Context(), "inertia: render error page", "status", e.Status, "err", err)
			return false
		}

		return true
	}

	// Encoded before any header is set, so a failure falls back cleanly.
	b, err := json.Marshal(p)
	if err != nil {
		c.Log().ErrorContext(c.Context(), "inertia: encode error page", "status", e.Status, "err", err)
		return false
	}

	c.SetHeader(HeaderInertia, "true")
	c.SetHeader("Cache-Control", "no-store")

	// Middleware adds Vary on matched routes; unmatched ones need it here.
	if !slices.Contains(c.Writer().Header().Values("Vary"), HeaderInertia) {
		c.Writer().Header().Add("Vary", HeaderInertia)
	}

	if err := c.Blob(e.Status, "application/json", b); err != nil {
		c.Log().DebugContext(c.Context(), "inertia: write error page", "status", e.Status, "err", err)
	}

	return true
}
