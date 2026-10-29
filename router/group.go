package router

import (
	"net/http"
	"strings"

	"github.com/AlexGalhardo/vukem/web"
)

// Group is a set of routes sharing a path prefix, a name prefix and
// middleware. Groups nest.
type Group struct {
	router *Router
	parent *Group
	prefix string
	name   string
	mw     []Middleware
	// host restricts the group's routes (Router.Host); hostSet tells an
	// empty host, an error, from no host.
	host    string
	hostSet bool
}

// Use appends middleware applied to every route of the group (including
// routes registered before the call and in nested groups).
func (g *Group) Use(mw ...Middleware) *Group {
	g.mw = append(g.mw, mw...)
	return g
}

// Name sets the name prefix of the group ("admin." makes "admin.posts.index").
func (g *Group) Name(prefix string) *Group {
	g.name = prefix
	return g
}

func (g *Group) lineage() []*Group {
	var chain []*Group
	for x := g; x != nil; x = x.parent {
		chain = append([]*Group{x}, chain...)
	}

	return chain
}

func (g *Group) fullPrefix() string {
	var b strings.Builder
	for _, x := range g.lineage() {
		b.WriteString(x.prefix)
	}

	return b.String()
}

func (g *Group) hostRoute() bool {
	for x := g; x != nil; x = x.parent {
		if x.hostSet {
			return true
		}
	}

	return false
}

func (g *Group) routeHost() string {
	for x := g; x != nil; x = x.parent {
		if x.hostSet {
			return x.host
		}
	}

	return ""
}

func (g *Group) namePrefix() string {
	var b strings.Builder
	for _, x := range g.lineage() {
		b.WriteString(x.name)
	}

	return b.String()
}

// Group creates a nested group (see Router.Group for "/" and "").
func (g *Group) Group(prefix string, fn func(g *Group)) *Group {
	child := &Group{router: g.router, parent: g, prefix: cleanPrefix(prefix)}
	if fn != nil {
		fn(child)
	}

	return child
}

func cleanPrefix(p string) string {
	p = strings.TrimSuffix(p, "/")
	if p != "" && !strings.HasPrefix(p, "/") {
		p = "/" + p
	}

	return p
}

func (g *Group) add(method, path string, h http.Handler, hf web.HandlerFunc) *Route {
	full := g.fullPrefix() + path
	if full == "" {
		full = "/"
	}

	// "/" means the root page only (Laravel semantics), in a group too:
	// "/admin/" serves "/admin/" and nothing below it. In ServeMux syntax a
	// trailing "/" is a catch-all, so handler routes add "{$}" (ServeMux
	// still redirects "/admin" to "/admin/"). Handle keeps raw ServeMux
	// semantics for mounting sub-trees.
	if hf != nil && (path == "/" || full == "/") {
		full += "{$}"
	}

	rt := &Route{method: method, host: g.routeHost(), path: full, group: g}
	if hf != nil {
		rt.handler = g.router.adapt(hf, rt)
	} else {
		rt.handler = h
	}

	g.router.routes = append(g.router.routes, rt)

	return rt
}

// Get registers a GET route (it also answers HEAD).
func (g *Group) Get(path string, h web.HandlerFunc) *Route {
	return g.add(http.MethodGet, path, nil, h)
}

// Post registers a POST route.
func (g *Group) Post(path string, h web.HandlerFunc) *Route {
	return g.add(http.MethodPost, path, nil, h)
}

// Put registers a PUT route.
func (g *Group) Put(path string, h web.HandlerFunc) *Route {
	return g.add(http.MethodPut, path, nil, h)
}

// Patch registers a PATCH route.
func (g *Group) Patch(path string, h web.HandlerFunc) *Route {
	return g.add(http.MethodPatch, path, nil, h)
}

// Delete registers a DELETE route.
func (g *Group) Delete(path string, h web.HandlerFunc) *Route {
	return g.add(http.MethodDelete, path, nil, h)
}

// Options registers an OPTIONS route.
func (g *Group) Options(path string, h web.HandlerFunc) *Route {
	return g.add(http.MethodOptions, path, nil, h)
}

// Match registers path for each method.
func (g *Group) Match(methods []string, path string, h web.HandlerFunc) []*Route {
	routes := make([]*Route, 0, len(methods))
	for _, m := range methods {
		routes = append(routes, g.add(strings.ToUpper(m), path, nil, h))
	}

	return routes
}

// Any registers path for every method.
func (g *Group) Any(path string, h web.HandlerFunc) *Route { return g.add("", path, nil, h) }

// Handle mounts an http.Handler; pattern may start with a method.
func (g *Group) Handle(pattern string, h http.Handler) *Route {
	method, path, ok := strings.Cut(pattern, " ")
	if !ok || strings.HasPrefix(pattern, "/") {
		method, path = "", pattern
	}

	return g.add(method, strings.TrimSpace(path), h, nil)
}
