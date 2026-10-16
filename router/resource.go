package router

import (
	"net/http"
	"slices"
	"strings"

	"github.com/AlexGalhardo/golang-framework/web"
)

// Resource controller actions. A controller implements any subset; only
// implemented actions are routed.
type (
	// Indexer handles GET /posts.
	Indexer interface{ Index(c *web.Ctx) error }
	// Creator handles GET /posts/create (HTML form).
	Creator interface{ Create(c *web.Ctx) error }
	// Storer handles POST /posts.
	Storer interface{ Store(c *web.Ctx) error }
	// Shower handles GET /posts/{post}.
	Shower interface{ Show(c *web.Ctx) error }
	// Editor handles GET /posts/{post}/edit (HTML form).
	Editor interface{ Edit(c *web.Ctx) error }
	// Updater handles PUT and PATCH /posts/{post}.
	Updater interface{ Update(c *web.Ctx) error }
	// Destroyer handles DELETE /posts/{post}.
	Destroyer interface{ Destroy(c *web.Ctx) error }
)

// Resource is a set of routes generated from a controller.
type Resource struct {
	routes map[string][]*Route
	// parents is the "/posts/{post}" part of nested resources, after the
	// group prefix; Shallow removes it from member routes.
	prefix, parents string
}

// Shallow makes the member routes of a nested resource top-level, as
// their IDs are unique on their own: /posts/{post}/comments stays for
// index/create/store, while show/edit/update/destroy become
// /comments/{comment}. Route names do not change.
func (rs *Resource) Shallow() *Resource {
	if rs.parents == "" {
		return rs
	}

	for _, action := range []string{"show", "edit", "update", "destroy"} {
		for _, rt := range rs.routes[action] {
			rt.path = rs.prefix + strings.TrimPrefix(rt.path, rs.prefix+rs.parents)
		}
	}

	return rs
}

// Use applies middleware to the resource routes (all actions when none are
// given by Only/Except before).
func (rs *Resource) Use(mw ...Middleware) *Resource {
	for _, routes := range rs.routes {
		for _, rt := range routes {
			rt.Use(mw...)
		}
	}

	return rs
}

// Only keeps the named actions and removes the others.
func (rs *Resource) Only(actions ...string) *Resource {
	return rs.keep(func(a string) bool { return slices.Contains(actions, a) })
}

// Except removes the named actions.
func (rs *Resource) Except(actions ...string) *Resource {
	return rs.keep(func(a string) bool { return !slices.Contains(actions, a) })
}

func (rs *Resource) keep(keep func(string) bool) *Resource {
	for action, routes := range rs.routes {
		if keep(action) {
			continue
		}

		for _, rt := range routes {
			rt.group.router.removeRoute(rt)
		}

		delete(rs.routes, action)
	}

	return rs
}

func (r *Router) removeRoute(target *Route) {
	r.routes = slices.DeleteFunc(r.routes, func(rt *Route) bool { return rt == target })
}

// Resource registers RESTful routes for ctrl under /name. Nested resources
// use dots: "posts.comments" → /posts/{post}/comments/{comment}. Route
// names are "<name>.<action>".
func (g *Group) Resource(name string, ctrl any) *Resource {
	return g.resource(name, ctrl, true)
}

// APIResource is Resource without the create and edit form routes.
func (g *Group) APIResource(name string, ctrl any) *Resource {
	return g.resource(name, ctrl, false)
}

func (g *Group) resource(name string, ctrl any, forms bool) *Resource {
	parts := strings.Split(name, ".")

	var base strings.Builder
	for _, p := range parts[:len(parts)-1] {
		base.WriteString("/" + p + "/{" + singular(p) + "}")
	}

	last := parts[len(parts)-1]
	collection := base.String() + "/" + last
	member := collection + "/{" + singular(last) + "}"

	rs := &Resource{routes: map[string][]*Route{}, prefix: g.fullPrefix(), parents: base.String()}
	add := func(action, method, path string, h web.HandlerFunc) {
		rt := g.add(method, path, nil, h).Name(name + "." + action)
		rs.routes[action] = append(rs.routes[action], rt)
	}

	if c, ok := ctrl.(Indexer); ok {
		add("index", http.MethodGet, collection, c.Index)
	}

	if c, ok := ctrl.(Creator); ok && forms {
		add("create", http.MethodGet, collection+"/create", c.Create)
	}

	if c, ok := ctrl.(Storer); ok {
		add("store", http.MethodPost, collection, c.Store)
	}

	if c, ok := ctrl.(Shower); ok {
		add("show", http.MethodGet, member, c.Show)
	}

	if c, ok := ctrl.(Editor); ok && forms {
		add("edit", http.MethodGet, member+"/edit", c.Edit)
	}

	if c, ok := ctrl.(Updater); ok {
		add("update", http.MethodPut, member, c.Update)
		rt := g.add(http.MethodPatch, member, nil, c.Update)
		rs.routes["update"] = append(rs.routes["update"], rt)
	}

	if c, ok := ctrl.(Destroyer); ok {
		add("destroy", http.MethodDelete, member, c.Destroy)
	}

	return rs
}

// singular turns a resource segment into its parameter name with a few
// English rules ("posts" → "post", "categories" → "category").
func singular(s string) string {
	switch {
	case strings.HasSuffix(s, "ies") && len(s) > 3:
		return s[:len(s)-3] + "y"
	case strings.HasSuffix(s, "sses"),
		strings.HasSuffix(s, "shes"),
		strings.HasSuffix(s, "ches"),
		strings.HasSuffix(s, "xes"):
		return s[:len(s)-2]
	case strings.HasSuffix(s, "s") && !strings.HasSuffix(s, "ss"):
		return s[:len(s)-1]
	default:
		return s
	}
}
