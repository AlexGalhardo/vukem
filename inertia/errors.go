package inertia

import (
	"encoding/json"

	"github.com/AlexGalhardo/vukem/web"
)

// ErrorPage renders Config.ErrorComponent with the props status and
// message for an Inertia request, so the client shows an error page instead
// of the raw HTML response in a modal. It is a web.ErrorPageFunc: install it
// as web.Services.ErrorPage (vukem.WithInertia does). It writes nothing for
// other requests (full page loads keep the HTML status page), for statuses
// outside 4xx/5xx, or when the page props fail to resolve.
//
// message comes from web.DefaultErrorHandler: the error's own message for
// 4xx, the status text for 5xx (CWE-209).
func (in *Inertia) ErrorPage(c *web.Ctx, status int, message string) bool {
	if !IsInertia(c.Request()) || status < 400 || status > 599 {
		return false
	}

	// Unmatched routes (404/405) never run route middleware.
	st := stateFrom(c.Context())
	if st == nil || st.in != in {
		st = &state{in: in, shared: Props{}}
	}

	p, err := st.page(c, in.cfg.ErrorComponent, Props{"status": status, "message": message})
	if err != nil {
		c.Log().ErrorContext(c.Context(), "inertia: render error page", "status", status, "err", err)
		return false
	}

	// Encoded before any header is set, so a failure falls back cleanly.
	b, err := json.Marshal(p)
	if err != nil {
		c.Log().ErrorContext(c.Context(), "inertia: encode error page", "status", status, "err", err)
		return false
	}

	c.SetHeader(HeaderInertia, "true")
	c.SetHeader("Cache-Control", "no-store")

	if err := c.Blob(status, "application/json", b); err != nil {
		c.Log().DebugContext(c.Context(), "inertia: write error page", "status", status, "err", err)
	}

	return true
}
