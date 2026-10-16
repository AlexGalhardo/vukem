// Package htmx provides helpers for htmx 4: reading htmx request headers,
// building response headers, and rendering <hx-partial> and out-of-band
// fragments. See https://four.htmx.org/reference/headers/.
package htmx

import (
	"encoding/json"
	"html/template"
	"net/http"
	"strings"
)

// Request headers sent by htmx 4.
const (
	HeaderRequest        = "HX-Request"
	HeaderRequestType    = "HX-Request-Type"
	HeaderCurrentURL     = "HX-Current-URL"
	HeaderSource         = "HX-Source"
	HeaderTarget         = "HX-Target"
	HeaderBoosted        = "HX-Boosted"
	HeaderHistoryRestore = "HX-History-Restore-Request"
)

// Response headers understood by htmx 4.
const (
	HeaderTrigger    = "HX-Trigger"
	HeaderLocation   = "HX-Location"
	HeaderRedirect   = "HX-Redirect"
	HeaderRefresh    = "HX-Refresh"
	HeaderPushURL    = "HX-Push-Url"
	HeaderReplaceURL = "HX-Replace-Url"
	HeaderRetarget   = "HX-Retarget"
	HeaderReswap     = "HX-Reswap"
	HeaderReselect   = "HX-Reselect"
)

// Is reports whether r was sent by htmx.
func Is(r *http.Request) bool { return r.Header.Get(HeaderRequest) == "true" }

// IsPartial reports whether htmx asked for a fragment (targeted swap). Full
// requests (body swaps, boosted navigation, history restores) need the
// whole page.
func IsPartial(r *http.Request) bool {
	return Is(r) && r.Header.Get(HeaderRequestType) != "full" && !IsHistoryRestore(r)
}

// IsBoosted reports whether the request comes from hx-boost.
func IsBoosted(r *http.Request) bool { return r.Header.Get(HeaderBoosted) == "true" }

// IsHistoryRestore reports a back/forward navigation re-fetch; it must be
// answered with a full page (htmx 4 has no local history cache).
func IsHistoryRestore(r *http.Request) bool { return r.Header.Get(HeaderHistoryRestore) == "true" }

// CurrentURL returns the browser URL when the request started.
func CurrentURL(r *http.Request) string { return r.Header.Get(HeaderCurrentURL) }

// Element is a "tag#id" reference from HX-Source or HX-Target.
type Element struct {
	Tag string
	ID  string
}

func parseElement(v string) Element {
	tag, id, _ := strings.Cut(v, "#")
	return Element{Tag: tag, ID: id}
}

// Source returns the element that triggered the request.
func Source(r *http.Request) Element { return parseElement(r.Header.Get(HeaderSource)) }

// Target returns the target element.
func Target(r *http.Request) Element { return parseElement(r.Header.Get(HeaderTarget)) }

// Response sets htmx response headers on w. Methods chain:
//
//	htmx.NewResponse(w).Trigger("saved", nil).PushURL("/posts/1")
type Response struct {
	h        http.Header
	triggers map[string]any
}

// NewResponse returns a Response writing headers to w. It also adds
// "Vary: HX-Request" so caches keep full pages and fragments apart.
func NewResponse(w http.ResponseWriter) *Response {
	h := w.Header()
	if !strings.Contains(h.Get("Vary"), HeaderRequest) {
		h.Add("Vary", HeaderRequest)
	}

	return &Response{h: h}
}

// Trigger fires a client-side event; detail (may be nil) is sent as JSON.
// Multiple triggers are merged into one header.
func (r *Response) Trigger(event string, detail any) *Response {
	if r.triggers == nil {
		r.triggers = map[string]any{}
	}

	r.triggers[event] = detail

	if b, err := json.Marshal(r.triggers); err == nil {
		r.h.Set(HeaderTrigger, string(b))
	}

	return r
}

// Location navigates without a full reload (like a boosted link).
func (r *Response) Location(path string) *Response { r.h.Set(HeaderLocation, path); return r }

// LocationOptions configures an HX-Location navigation.
type LocationOptions struct {
	Path   string            `json:"path"`
	Target string            `json:"target,omitempty"`
	Swap   string            `json:"swap,omitempty"`
	Select string            `json:"select,omitempty"`
	Values map[string]any    `json:"values,omitempty"`
	Header map[string]string `json:"headers,omitempty"`
}

// LocationWith navigates with options.
func (r *Response) LocationWith(o LocationOptions) *Response {
	if b, err := json.Marshal(o); err == nil {
		r.h.Set(HeaderLocation, string(b))
	}

	return r
}

// Redirect performs a full-page redirect.
func (r *Response) Redirect(url string) *Response { r.h.Set(HeaderRedirect, url); return r }

// Refresh reloads the page.
func (r *Response) Refresh() *Response { r.h.Set(HeaderRefresh, "true"); return r }

// PushURL pushes url into the browser history.
func (r *Response) PushURL(url string) *Response { r.h.Set(HeaderPushURL, url); return r }

// ReplaceURL replaces the current history entry.
func (r *Response) ReplaceURL(url string) *Response { r.h.Set(HeaderReplaceURL, url); return r }

// Retarget changes the swap target (CSS selector).
func (r *Response) Retarget(selector string) *Response { r.h.Set(HeaderRetarget, selector); return r }

// Reswap changes the swap style ("innerHTML", "outerMorph", ...).
func (r *Response) Reswap(style string) *Response { r.h.Set(HeaderReswap, style); return r }

// Reselect picks part of the response (CSS selector).
func (r *Response) Reselect(selector string) *Response { r.h.Set(HeaderReselect, selector); return r }

// Partial wraps content in an <hx-partial> element so one response can
// update several targets. Attribute values are escaped.
func Partial(target, swap string, content template.HTML) template.HTML {
	var b strings.Builder

	b.WriteString(`<hx-partial hx-target="` + template.HTMLEscapeString(target) + `"`)

	if swap != "" {
		b.WriteString(` hx-swap="` + template.HTMLEscapeString(swap) + `"`)
	}

	b.WriteString(">")
	b.WriteString(string(content))
	b.WriteString("</hx-partial>")

	return trustedHTML(b.String())
}

// OOB renders content as an out-of-band swap into the element with id.
func OOB(id, swap string, content template.HTML) template.HTML {
	if swap == "" {
		swap = "true"
	}

	s := `<div id="` + template.HTMLEscapeString(id) + `" hx-swap-oob="` + template.HTMLEscapeString(swap) + `">` +
		string(content) + `</div>`

	return trustedHTML(s)
}

// trustedHTML marks markup whose attributes were escaped above; content is
// HTML the application already rendered.
func trustedHTML(s string) template.HTML {
	return template.HTML(s) //nolint:gosec // G203: attributes escaped by callers
}
