package web

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/AlexGalhardo/vukem/errs"
	"github.com/AlexGalhardo/vukem/htmx"
	"github.com/AlexGalhardo/vukem/sse"
)

// Renderer renders named views. The view package implements it; ctx gives
// templates access to request-scoped data (session, CSRF token, CSP nonce).
type Renderer interface {
	Render(ctx context.Context, w io.Writer, name string, data any) error
	RenderBlock(ctx context.Context, w io.Writer, name, block string, data any) error
	Has(name string) bool
}

// View is a self-rendering component; templ.Component satisfies it.
type View interface {
	Render(ctx context.Context, w io.Writer) error
}

type renderOpts struct {
	status  int
	block   string
	force   bool
	partial View
}

// RenderOption configures Render.
type RenderOption func(*renderOpts)

// WithStatus sets the response status (default 200).
func WithStatus(code int) RenderOption { return func(o *renderOpts) { o.status = code } }

// Block renders only the named block for htmx partial requests and the full
// page otherwise (template fragments):
//
//	return c.Render("posts/index", data, web.Block("post-list"))
func Block(name string) RenderOption { return func(o *renderOpts) { o.block = name } }

// Partial is Block for View: c.View renders v instead of the page for htmx
// partial requests (templ fragments):
//
//	return c.View(views.PostsIndex(posts), web.Partial(views.PostList(posts)))
func Partial(v View) RenderOption { return func(o *renderOpts) { o.partial = v } }

var errNoViews = errors.New("web: no view engine configured")

// Render renders a view into the response. Output is buffered, so a
// template error becomes a clean 500 instead of a half-written page.
func (c *Ctx) Render(name string, data any, opts ...RenderOption) error {
	o := renderOpts{status: http.StatusOK}
	for _, opt := range opts {
		opt(&o)
	}

	if c.s.Views == nil {
		return errs.Wrap(http.StatusInternalServerError, errNoViews)
	}

	var buf bytes.Buffer

	var err error

	if o.block != "" {
		htmx.NewResponse(c.w) // Vary: HX-Request — fragment and page differ

		if o.force || htmx.IsPartial(c.r) {
			err = c.s.Views.RenderBlock(c.Context(), &buf, name, o.block, data)
		} else {
			err = c.s.Views.Render(c.Context(), &buf, name, data)
		}
	} else {
		err = c.s.Views.Render(c.Context(), &buf, name, data)
	}

	if err != nil {
		return errs.Wrap(http.StatusInternalServerError, err)
	}

	return c.Blob(o.status, "text/html; charset=utf-8", buf.Bytes())
}

// Fragment renders a single block of a view.
func (c *Ctx) Fragment(name, block string, data any, opts ...RenderOption) error {
	return c.Render(name, data, append(opts, Block(block), forcePartial())...)
}

func forcePartial() RenderOption { return func(o *renderOpts) { o.force = true } }

// View renders a component such as a templ.Component.
func (c *Ctx) View(v View, opts ...RenderOption) error {
	o := renderOpts{status: http.StatusOK}
	for _, opt := range opts {
		opt(&o)
	}

	if o.partial != nil {
		htmx.NewResponse(c.w) // Vary: HX-Request — fragment and page differ

		if htmx.IsPartial(c.r) {
			v = o.partial
		}
	}

	var buf bytes.Buffer
	if err := v.Render(c.Context(), &buf); err != nil {
		return errs.Wrap(http.StatusInternalServerError, err)
	}

	return c.Blob(o.status, "text/html; charset=utf-8", buf.Bytes())
}

// SSE streams server-sent events to the client with fn (see package sse):
//
//	return c.SSE(func(s *sse.Stream) error { return hub.Stream(s, "posts") })
func (c *Ctx) SSE(fn func(*sse.Stream) error, opts ...sse.Option) error {
	return sse.Serve(c.w, c.r, fn, opts...)
}

// HTMX returns the htmx response-header builder for this request.
func (c *Ctx) HTMX() *htmx.Response { return htmx.NewResponse(c.w) }

// IsPartial reports whether htmx asked for a fragment.
func (c *Ctx) IsPartial() bool { return htmx.IsPartial(c.r) }
