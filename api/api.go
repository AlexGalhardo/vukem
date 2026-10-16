// Package api holds REST helpers on top of web and orm: resource
// transformers, pagination envelopes, allowlisted query conventions
// (filter, sort, fields, include, pagination) and deprecation headers.
// See docs/api.md.
package api

import (
	"net/http"
	"net/url"
	"strconv"

	"github.com/AlexGalhardo/vukem/orm"
	"github.com/AlexGalhardo/vukem/web"
)

// Map transforms every item with fn; resources use it to turn models into
// their wire format: func Products(ps []models.Product) []ProductJSON {
// return api.Map(ps, Product) }.
func Map[T, R any](items []T, fn func(T) R) []R {
	out := make([]R, len(items))
	for i, it := range items {
		out[i] = fn(it)
	}

	return out
}

// Envelope is the JSON body of collections and pages.
type Envelope[R any] struct {
	Data  []R    `json:"data"`
	Meta  *Meta  `json:"meta,omitempty"`
	Links *Links `json:"links,omitempty"`
}

// Meta describes a page.
type Meta struct {
	Page       int    `json:"page,omitempty"`
	PerPage    int    `json:"per_page"`
	Total      *int64 `json:"total,omitempty"`
	LastPage   int    `json:"last_page,omitempty"`
	NextCursor string `json:"next_cursor,omitempty"`
}

// Links point to neighbour pages; nil encodes as null.
type Links struct {
	Self string  `json:"self"`
	Next *string `json:"next"`
	Prev *string `json:"prev"`
}

// Collection answers 200 with {"data": [...]}.
func Collection[T, R any](c *web.Ctx, items []T, fn func(T) R) error {
	return c.JSON(http.StatusOK, Envelope[R]{Data: Map(items, fn)})
}

// Page answers 200 with an offset page: data, meta (page, per_page, total,
// last_page) and links (self, next, prev).
func Page[T, R any](c *web.Ctx, p orm.Page[T], fn func(T) R) error {
	total := p.Total
	links := &Links{Self: withQuery(c, "page", strconv.Itoa(p.Page))}

	if p.Page < p.LastPage {
		links.Next = new(withQuery(c, "page", strconv.Itoa(p.Page+1)))
	}

	if p.Page > 1 {
		links.Prev = new(withQuery(c, "page", strconv.Itoa(min(p.Page-1, p.LastPage))))
	}

	return c.JSON(http.StatusOK, Envelope[R]{
		Data:  Map(p.Items, fn),
		Meta:  &Meta{Page: p.Page, PerPage: p.PerPage, Total: &total, LastPage: p.LastPage},
		Links: links,
	})
}

// Cursor answers 200 with a keyset page: meta.next_cursor and links.next
// (null on the last page).
func Cursor[T, R any](c *web.Ctx, p orm.CursorPage[T], fn func(T) R) error {
	links := &Links{Self: c.Request().URL.RequestURI()}
	if p.NextCursor != "" {
		links.Next = new(withQuery(c, "cursor", p.NextCursor))
	}

	return c.JSON(http.StatusOK, Envelope[R]{
		Data:  Map(p.Items, fn),
		Meta:  &Meta{PerPage: p.PerPage, NextCursor: p.NextCursor},
		Links: links,
	})
}

// withQuery returns the request path and query with key set to value.
func withQuery(c *web.Ctx, key, value string) string {
	u := *c.Request().URL
	q := u.Query()
	q.Set(key, value)
	u.RawQuery = q.Encode()

	return (&url.URL{Path: u.Path, RawQuery: u.RawQuery}).RequestURI()
}
