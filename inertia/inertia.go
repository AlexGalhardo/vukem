// Package inertia is a server-side adapter for the Inertia.js v3 protocol
// (https://inertiajs.com/docs/v3/core-concepts/the-protocol).
//
// Install Middleware on the routes that render Inertia pages, then return
// Render from handlers:
//
//	in, _ := inertia.New(inertia.Config{Version: vite.Version})
//	r.Use(in.Middleware)
//
//	r.Get("/events/{id}", func(c *web.Ctx) error {
//		return inertia.Render(c, "Events/Show", inertia.Props{"event": event})
//	})
//
// The first visit renders the root view (RootView, default "app") with the
// page object in RootData.Inertia; later visits made by the Inertia client
// (X-Inertia: true) get the page object as JSON.
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/frontend.md.
package inertia

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"slices"
	"strings"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/web"
)

// Protocol headers.
const (
	HeaderInertia          = "X-Inertia"
	HeaderVersion          = "X-Inertia-Version"
	HeaderLocation         = "X-Inertia-Location"
	HeaderRedirect         = "X-Inertia-Redirect"
	HeaderErrorBag         = "X-Inertia-Error-Bag"
	HeaderPartialComponent = "X-Inertia-Partial-Component"
	HeaderPartialData      = "X-Inertia-Partial-Data"
	HeaderPartialExcept    = "X-Inertia-Partial-Except"
	HeaderReset            = "X-Inertia-Reset"
	HeaderExceptOnceProps  = "X-Inertia-Except-Once-Props"
)

// preserveFragmentKey is the session flash key set by PreserveFragment.
const preserveFragmentKey = "_inertia_preserve_fragment"

// Props are page props. A value of type func(context.Context) (any, error)
// is resolved only when the prop is sent.
type Props map[string]any

// Config configures an Inertia adapter.
type Config struct {
	// RootView is the view rendered on full page visits (default "app").
	// It receives RootData.
	RootView string
	// RootID is the id of the element the client mounts on (default "app").
	RootID string
	// Version returns the current asset version, for example vite's
	// manifest hash. nil means "" (no versioning).
	Version func() string
	// Shared props are added to every page; page props win on conflicts.
	Shared Props
	// EncryptHistory encrypts every page's history state on the client
	// (needs a secure context); EncryptHistory(ctx, false) opts a page out.
	EncryptHistory bool
	// SSR enables server-side rendering (nil: client-side only).
	SSR *SSRConfig
}

// Inertia is the adapter; build it with New.
type Inertia struct {
	cfg Config
}

// Page is the Inertia page object.
type Page struct {
	Component        string                 `json:"component"`
	Props            map[string]any         `json:"props"`
	URL              string                 `json:"url"`
	Version          string                 `json:"version"`
	EncryptHistory   bool                   `json:"encryptHistory,omitempty"`
	ClearHistory     bool                   `json:"clearHistory,omitempty"`
	PreserveFragment bool                   `json:"preserveFragment,omitempty"`
	MergeProps       []string               `json:"mergeProps,omitempty"`
	PrependProps     []string               `json:"prependProps,omitempty"`
	DeepMergeProps   []string               `json:"deepMergeProps,omitempty"`
	MatchPropsOn     []string               `json:"matchPropsOn,omitempty"`
	ScrollProps      map[string]scrollEntry `json:"scrollProps,omitempty"`
	DeferredProps    map[string][]string    `json:"deferredProps,omitempty"`
	RescuedProps     []string               `json:"rescuedProps,omitempty"`
	SharedProps      []string               `json:"sharedProps,omitempty"`
	OnceProps        map[string]onceMeta    `json:"onceProps,omitempty"`
	Flash            map[string]any         `json:"flash,omitempty"`
}

// RootData is the data the root view receives on full page visits.
type RootData struct {
	// Page is the page object.
	Page *Page
	// Inertia is the page object script followed by the mount element.
	Inertia template.HTML
	// Head holds server-rendered head elements (empty without SSR).
	Head template.HTML
}

// New returns an adapter.
func New(cfg Config) (*Inertia, error) {
	if cfg.RootView == "" {
		cfg.RootView = "app"
	}

	if cfg.RootID == "" {
		cfg.RootID = "app"
	}

	if strings.ContainsAny(cfg.RootID, "\"'<>& ") {
		return nil, fmt.Errorf("inertia: invalid RootID %q", cfg.RootID)
	}

	cfg.Shared = maps.Clone(cfg.Shared)

	if cfg.SSR != nil {
		cfg.SSR = cfg.SSR.withDefaults()
	}

	return &Inertia{cfg: cfg}, nil
}

// Version returns the current asset version.
func (in *Inertia) Version() string {
	if in.cfg.Version == nil {
		return ""
	}

	return in.cfg.Version()
}

type stateKey struct{}

// state is the per-request adapter state stored by Middleware.
type state struct {
	in      *Inertia
	shared  Props
	encrypt *bool
	noSSR   bool
}

func stateFrom(ctx context.Context) *state {
	s, _ := ctx.Value(stateKey{}).(*state)
	return s
}

// Middleware implements the request side of the protocol: Vary, the asset
// version check (409 on GET), 302 → 303 after PUT/PATCH/DELETE and
// fragment redirects.
func (in *Inertia) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Add("Vary", HeaderInertia)
		r = r.WithContext(context.WithValue(r.Context(), stateKey{}, &state{in: in, shared: Props{}}))

		if !IsInertia(r) {
			next.ServeHTTP(&redirectWriter{ResponseWriter: w, r: r}, r)
			return
		}

		if v := in.Version(); r.Method == http.MethodGet && r.Header.Get(HeaderVersion) != v {
			if s, ok := web.SessionFrom(r.Context()).(interface{ Reflash() }); ok {
				s.Reflash()
			}

			w.Header().Set(HeaderLocation, absoluteURL(r))
			w.Header().Set(HeaderVersion, v)
			w.WriteHeader(http.StatusConflict)

			return
		}

		next.ServeHTTP(&redirectWriter{ResponseWriter: w, r: r, inertia: true}, r)
	})
}

// IsInertia reports whether r was sent by the Inertia client.
func IsInertia(r *http.Request) bool { return r.Header.Get(HeaderInertia) == "true" }

// Share adds a prop to every page rendered for the request carrying ctx
// (call it from middleware or a handler before Render).
func Share(ctx context.Context, key string, value any) {
	if s := stateFrom(ctx); s != nil {
		s.shared[key] = value
	}
}

// Location sends the client to url with a full page load: 409 with
// X-Inertia-Location for Inertia requests, a plain redirect otherwise. Use
// it for external URLs and non-Inertia pages.
func Location(c *web.Ctx, url string) error {
	if IsInertia(c.Request()) {
		c.SetHeader(HeaderLocation, url)
		c.Writer().WriteHeader(http.StatusConflict)

		return nil
	}

	return c.Redirect(url)
}

// PreserveFragment keeps the URL fragment of the current request across the
// redirect that follows (page object "preserveFragment").
func PreserveFragment(c *web.Ctx) {
	if s := c.Session(); s != nil {
		s.Flash(preserveFragmentKey, true)
	}
}

var errNoMiddleware = errs.Wrap(http.StatusInternalServerError,
	errors.New("inertia: Render needs inertia Middleware on the route"))

// Render responds with the page component and props: JSON for Inertia
// requests, the root view otherwise.
func Render(c *web.Ctx, component string, props Props) error {
	return RenderStatus(c, http.StatusOK, component, props)
}

// RenderStatus is Render with an HTTP status, for error pages (404, 410,
// 503) or a 422 page. The Inertia client renders any response carrying
// X-Inertia as a page, whatever its status. Statuses without a body (1xx,
// 204, 205, 304), redirects and 409 (the protocol's version and location
// responses) are refused with an error.
func RenderStatus(c *web.Ctx, status int, component string, props Props) error {
	if !pageStatus(status) {
		return errs.Wrap(http.StatusInternalServerError,
			fmt.Errorf("inertia: RenderStatus cannot answer status %d", status))
	}

	st := stateFrom(c.Context())
	if st == nil {
		return errNoMiddleware
	}

	p, err := st.page(c, component, props)
	if err != nil {
		return err
	}

	if IsInertia(c.Request()) {
		c.SetHeader(HeaderInertia, "true")
		return c.JSON(status, p)
	}

	if head, body, ok := st.in.serverRender(c, st, p); ok {
		return c.Render(st.in.cfg.RootView,
			RootData{Page: p, Inertia: trustedHTML(body), Head: trustedHTML(head)}, web.WithStatus(status))
	}

	script, err := pageScript(p)
	if err != nil {
		return err
	}

	html := script + `<div id="` + st.in.cfg.RootID + `"></div>`

	return c.Render(st.in.cfg.RootView, RootData{Page: p, Inertia: trustedHTML(html)}, web.WithStatus(status))
}

func pageStatus(code int) bool {
	switch {
	case code == http.StatusNoContent, code == http.StatusResetContent, code == http.StatusConflict:
		return false
	case code >= 200 && code < 300, code >= 400 && code < 600:
		return true
	default:
		return false
	}
}

func (st *state) page(c *web.Ctx, component string, props Props) (*Page, error) {
	shared := maps.Clone(st.in.cfg.Shared)
	if shared == nil {
		shared = Props{}
	}

	maps.Copy(shared, st.shared)

	all := maps.Clone(shared)
	maps.Copy(all, props)

	req := c.Request()
	rs := &resolver{
		ctx:     c.Context(),
		log:     c.Log(),
		inertia: IsInertia(req),
		partial: IsInertia(req) && req.Header.Get(HeaderPartialComponent) == component,
		only:    splitHeader(req.Header.Get(HeaderPartialData)),
		except:  splitHeader(req.Header.Get(HeaderPartialExcept)),
		reset:   splitHeader(req.Header.Get(HeaderReset)),
		loaded:  splitHeader(req.Header.Get(HeaderExceptOnceProps)),
		prepend: req.Header.Get(HeaderScrollMergeIntent) == "prepend",
	}

	resolved, err := rs.resolveAll(all)
	if err != nil {
		return nil, err
	}

	resolved["errors"] = validationErrors(c)

	sharedKeys := map[string]bool{}
	for k := range shared {
		top, _, _ := strings.Cut(k, ".")
		sharedKeys[top] = true
	}

	p := &Page{
		Component:      component,
		Props:          resolved,
		URL:            req.URL.RequestURI(),
		Version:        st.in.Version(),
		SharedProps:    slices.Sorted(maps.Keys(sharedKeys)),
		MergeProps:     rs.merge,
		PrependProps:   rs.prepended,
		DeepMergeProps: rs.deep,
		MatchPropsOn:   rs.matchOn,
		ScrollProps:    rs.scroll,
		DeferredProps:  rs.deferred,
		RescuedProps:   rs.rescued,
		OnceProps:      rs.onceProps,
	}

	pageFlags(c, st, p)

	return p, nil
}

// validationErrors returns the first flashed message per field, namespaced
// under the request's error bag when it names one.
func validationErrors(c *web.Ctx) map[string]any {
	out := map[string]any{}

	for field, msgs := range c.Errors() {
		if len(msgs) > 0 {
			out[field] = msgs[0]
		}
	}

	if bag := c.Header(HeaderErrorBag); bag != "" && len(out) > 0 {
		return map[string]any{bag: out}
	}

	return out
}

// pageScript returns the page object as a JSON script element. Slashes are
// escaped so prop data can never close the element (</script>).
func pageScript(p *Page) (string, error) {
	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("inertia: encode page: %w", err)
	}

	b = bytes.ReplaceAll(b, []byte("/"), []byte(`\/`))

	return `<script data-page="app" type="application/json">` + string(b) + `</script>`, nil
}

func absoluteURL(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}

	return scheme + "://" + r.Host + r.URL.RequestURI()
}

// redirectWriter rewrites redirects for Inertia requests: 302 → 303 after
// PUT/PATCH/DELETE, and redirects to a URL with a fragment become a 409
// with X-Inertia-Redirect (the browser would drop the fragment).
type redirectWriter struct {
	http.ResponseWriter

	r           *http.Request
	inertia     bool
	wroteHeader bool
	discard     bool
}

func (w *redirectWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}

	w.wroteHeader = true

	if code >= 300 && code < 400 {
		reflash(w.r)
	}

	if !w.inertia {
		w.ResponseWriter.WriteHeader(code)
		return
	}

	if code == http.StatusFound &&
		(w.r.Method == http.MethodPut || w.r.Method == http.MethodPatch || w.r.Method == http.MethodDelete) {
		code = http.StatusSeeOther
	}

	h := w.Header()
	if loc := h.Get("Location"); code >= 300 && code < 400 && strings.Contains(loc, "#") &&
		!IsPrefetch(w.r) {
		h.Del("Location")
		h.Del("Content-Type")
		h.Set(HeaderRedirect, loc)

		code = http.StatusConflict
		w.discard = true
	}

	w.ResponseWriter.WriteHeader(code)
}

func (w *redirectWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}

	if w.discard {
		return len(b), nil
	}

	return w.ResponseWriter.Write(b)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *redirectWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func trustedHTML(s string) template.HTML {
	return template.HTML(s) //nolint:gosec // G203: JSON with escaped "/" and a validated RootID
}
