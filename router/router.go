// Package router is a thin layer over net/http.ServeMux (Go 1.22+ patterns)
// adding route groups, named routes with URL generation, resource
// controllers and three middleware stacks:
//
//   - server middleware runs on every request, even when no route matches;
//   - router middleware runs only when a route matches;
//   - route/group middleware runs for the routes it is attached to.
//
// Middleware has the standard signature func(http.Handler) http.Handler, so
// any net/http middleware works.
package router

import (
	"fmt"
	"net/http"
	"net/url"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/AlexGalhardo/golang-framework/errs"
	"github.com/AlexGalhardo/golang-framework/web"
)

// Middleware wraps an http.Handler.
type Middleware = func(http.Handler) http.Handler

// Router collects routes and compiles them into an http.Handler.
type Router struct {
	services *web.Services
	routes   []*Route
	server   []Middleware
	router   []Middleware
	root     *Group

	once     sync.Once
	mux      *http.ServeMux
	names    map[string]*Route
	handler  http.Handler
	buildErr error
}

// New returns an empty Router. services may be nil.
func New(services *web.Services) *Router {
	if services == nil {
		services = &web.Services{}
	}

	r := &Router{services: services}
	r.root = &Group{router: r}

	if services.URLs == nil {
		services.URLs = r
	}

	return r
}

// Services returns the shared services passed to every *web.Ctx.
func (r *Router) Services() *web.Services { return r.services }

// UseServer appends middleware that runs for every request.
func (r *Router) UseServer(mw ...Middleware) { r.server = append(r.server, mw...) }

// Use appends middleware that runs for every matched route.
func (r *Router) Use(mw ...Middleware) { r.router = append(r.router, mw...) }

// Route is a registered route.
type Route struct {
	method  string
	path    string
	name    string
	handler http.Handler
	mw      []Middleware
	group   *Group
}

// Name sets the route name used by URL generation. The names of enclosing
// groups are prefixed when the router is compiled.
func (rt *Route) Name(name string) *Route {
	rt.name = name
	return rt
}

// FullName returns the name including group prefixes, or "".
func (rt *Route) FullName() string {
	if rt.name == "" {
		return ""
	}

	return rt.group.namePrefix() + rt.name
}

// Use appends route middleware.
func (rt *Route) Use(mw ...Middleware) *Route {
	rt.mw = append(rt.mw, mw...)
	return rt
}

// RouteInfo describes a compiled route.
type RouteInfo struct {
	Method     string   `json:"method"`
	Path       string   `json:"path"`
	Name       string   `json:"name,omitempty"`
	Middleware []string `json:"middleware,omitempty"`
}

// Get registers a GET route (it also answers HEAD).
func (r *Router) Get(path string, h web.HandlerFunc) *Route { return r.root.Get(path, h) }

// Post registers a POST route.
func (r *Router) Post(path string, h web.HandlerFunc) *Route { return r.root.Post(path, h) }

// Put registers a PUT route.
func (r *Router) Put(path string, h web.HandlerFunc) *Route { return r.root.Put(path, h) }

// Patch registers a PATCH route.
func (r *Router) Patch(path string, h web.HandlerFunc) *Route { return r.root.Patch(path, h) }

// Delete registers a DELETE route.
func (r *Router) Delete(path string, h web.HandlerFunc) *Route { return r.root.Delete(path, h) }

// Options registers an OPTIONS route.
func (r *Router) Options(path string, h web.HandlerFunc) *Route { return r.root.Options(path, h) }

// Match registers a route for several methods.
func (r *Router) Match(methods []string, path string, h web.HandlerFunc) []*Route {
	return r.root.Match(methods, path, h)
}

// Any registers a route for every method.
func (r *Router) Any(path string, h web.HandlerFunc) *Route { return r.root.Any(path, h) }

// Handle mounts a plain http.Handler. pattern uses ServeMux syntax and may
// start with a method ("GET /metrics").
func (r *Router) Handle(pattern string, h http.Handler) *Route { return r.root.Handle(pattern, h) }

// Group creates a route group under prefix.
func (r *Router) Group(prefix string, fn func(g *Group)) *Group { return r.root.Group(prefix, fn) }

// Resource registers the actions ctrl implements (see Indexer, Storer, ...).
func (r *Router) Resource(name string, ctrl any) *Resource { return r.root.Resource(name, ctrl) }

// APIResource is Resource without the create and edit form routes.
func (r *Router) APIResource(name string, ctrl any) *Resource { return r.root.APIResource(name, ctrl) }

// Handler compiles the routes. It reports pattern conflicts, invalid
// patterns and duplicate names. Routes added afterwards are ignored.
func (r *Router) Handler() (http.Handler, error) {
	r.once.Do(r.build)
	return r.handler, r.buildErr
}

// ServeHTTP compiles on first use and serves; it panics on a build error,
// so call Handler at boot to surface errors early.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	h, err := r.Handler()
	if err != nil {
		panic(err)
	}

	h.ServeHTTP(w, req)
}

func (r *Router) build() {
	r.mux = http.NewServeMux()
	r.names = map[string]*Route{}

	owners := map[string]*Route{}

	for _, rt := range r.routes {
		if name := rt.FullName(); name != "" {
			if prev, dup := r.names[name]; dup {
				r.buildErr = fmt.Errorf("router: duplicate route name %q (%s %s and %s %s)",
					name, prev.method, prev.path, rt.method, rt.path)

				return
			}

			r.names[name] = rt
		}

		pattern := rt.path
		if rt.method != "" {
			pattern = rt.method + " " + rt.path
		}

		h := rt.handler
		for _, mw := range slices.Backward(rt.allMiddleware()) {
			h = mw(h)
		}

		if err := register(r.mux, pattern, h); err != nil {
			r.buildErr = fmt.Errorf("router: %s: %w", describe(rt), conflictWith(err, owners))
			return
		}

		owners[pattern] = rt
	}

	var h http.Handler = http.HandlerFunc(r.dispatch)
	for _, mw := range slices.Backward(r.server) {
		h = mw(h)
	}

	r.handler = h
}

func register(mux *http.ServeMux, pattern string, h http.Handler) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("%v", p)
		}
	}()

	mux.Handle(pattern, h)

	return nil
}

func conflictWith(err error, owners map[string]*Route) error {
	for pattern, rt := range owners {
		if strings.Contains(err.Error(), "\""+pattern+"\"") || strings.Contains(err.Error(), pattern+" ") {
			return fmt.Errorf("%w (conflicts with %s)", err, describe(rt))
		}
	}

	return err
}

func describe(rt *Route) string {
	s := strings.TrimSpace(rt.method + " " + rt.path)
	if name := rt.FullName(); name != "" {
		s += " [" + name + "]"
	}

	return s
}

func (rt *Route) allMiddleware() []Middleware {
	var chain []Middleware

	chain = append(chain, rt.group.router.router...)
	for _, g := range rt.group.lineage() {
		chain = append(chain, g.mw...)
	}

	return append(chain, rt.mw...)
}

// dispatch routes through the mux but renders 404 and 405 with the error
// handler (problem+json, HTML pages) instead of ServeMux's plain text.
func (r *Router) dispatch(w http.ResponseWriter, req *http.Request) {
	if _, pattern := r.mux.Handler(req); pattern != "" {
		r.mux.ServeHTTP(w, req)
		return
	}

	c := web.NewCtx(w, req, r.services)

	allowed := r.allowed(req)
	if len(allowed) == 0 {
		c.HandleError(errs.ErrNotFound)
		return
	}

	c.HandleError(errs.ErrMethodNotAllowed.WithHeader("Allow", strings.Join(allowed, ", ")))
}

var probeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut,
	http.MethodPatch, http.MethodDelete, http.MethodOptions,
}

func (r *Router) allowed(req *http.Request) []string {
	var methods []string

	for _, m := range probeMethods {
		probe := req.Clone(req.Context())
		probe.Method = m

		if _, pattern := r.mux.Handler(probe); pattern != "" {
			methods = append(methods, m)
		}
	}

	return methods
}

// URL implements web.URLGenerator: params are name/value pairs filling the
// route's {wildcards}. Missing or unknown params are errors.
func (r *Router) URL(name string, params ...any) (string, error) {
	if _, err := r.Handler(); err != nil {
		return "", err
	}

	rt, ok := r.names[name]
	if !ok {
		return "", fmt.Errorf("router: no route named %q", name)
	}

	if len(params)%2 != 0 {
		return "", fmt.Errorf("router: URL(%q) needs name/value pairs", name)
	}

	values := make(map[string]string, len(params)/2)
	for pair := range slices.Chunk(params, 2) {
		k, ok := pair[0].(string)
		if !ok {
			return "", fmt.Errorf("router: URL(%q) param name %v is not a string", name, pair[0])
		}

		values[k] = fmt.Sprint(pair[1])
	}

	return buildPath(rt.path, values, name)
}

func buildPath(pattern string, values map[string]string, name string) (string, error) {
	var b strings.Builder

	used := map[string]bool{}
	rest := pattern

	for {
		start := strings.IndexByte(rest, '{')
		if start < 0 {
			b.WriteString(rest)
			break
		}

		end := strings.IndexByte(rest[start:], '}')
		b.WriteString(rest[:start])

		wild := rest[start+1 : start+end]
		rest = rest[start+end+1:]

		if wild == "$" {
			continue
		}

		key, multi := strings.CutSuffix(wild, "...")

		v, ok := values[key]
		if !ok {
			return "", fmt.Errorf("router: URL(%q) missing param %q", name, key)
		}

		used[key] = true

		if multi {
			segs := strings.Split(v, "/")
			for i, s := range segs {
				segs[i] = url.PathEscape(s)
			}

			b.WriteString(strings.Join(segs, "/"))
		} else {
			b.WriteString(url.PathEscape(v))
		}
	}

	for k := range values {
		if !used[k] {
			return "", fmt.Errorf("router: URL(%q) unknown param %q", name, k)
		}
	}

	return b.String(), nil
}

// Routes lists compiled routes in registration order.
func (r *Router) Routes() []RouteInfo {
	infos := make([]RouteInfo, 0, len(r.routes))

	for _, rt := range r.routes {
		info := RouteInfo{Method: rt.method, Path: rt.path, Name: rt.FullName()}
		if info.Method == "" {
			info.Method = "ANY"
		}

		for _, mw := range rt.allMiddleware() {
			info.Middleware = append(info.Middleware, funcName(mw))
		}

		infos = append(infos, info)
	}

	return infos
}

func funcName(mw Middleware) string {
	pc := reflect.ValueOf(mw).Pointer()

	fn := runtime.FuncForPC(pc)
	if fn == nil {
		return "middleware@" + strconv.FormatUint(uint64(pc), 16)
	}

	name := fn.Name()
	// "github.com/x/y/middleware.Recover.func1" → "middleware.Recover"
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}

	if i := strings.Index(name, ".func"); i >= 0 {
		name = name[:i]
	}

	return name
}

func (r *Router) adapt(h web.HandlerFunc, rt *Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		c := web.NewCtx(w, req, r.services)
		c.SetRouteName(rt.FullName())
		c.Serve(h)
	})
}

// Adapt converts a HandlerFunc into an http.Handler using services.
func Adapt(h web.HandlerFunc, services *web.Services) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		web.NewCtx(w, req, services).Serve(h)
	})
}

// Wrap converts an http.Handler into a HandlerFunc.
func Wrap(h http.Handler) web.HandlerFunc {
	return func(c *web.Ctx) error {
		h.ServeHTTP(c.Writer(), c.Request())
		return nil
	}
}
