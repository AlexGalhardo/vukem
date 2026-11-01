// Package view renders html/template views with layouts, partials and
// fragments (single blocks, for htmx), plus request-aware helpers: route,
// asset, csrfField, old, errors, flash, cspNonce, t and locale.
//
// Layout of the views directory:
//
//	layouts/app.html        {{block "content" .}}{{end}} inside the page shell
//	partials/nav.html       included with {{template "partials/nav" .}}
//	components/alert.html   same, by convention for reusable widgets
//	posts/index.html        a page: {{define "content"}}...{{end}}
//
// A page uses the default layout ("app") unless its first line is a
// comment naming another one: {{/* layout: admin */}} or {{/* layout: none */}}.
//
// [New] parses every view at boot into an [Engine], which implements
// web.Renderer: handlers call c.Render("posts/index", data) for a page and
// c.Fragment("posts/index", "items", data) for one block (htmx swaps).
//
// See https://github.com/AlexGalhardo/vukem/blob/main/docs/frontend.md.
package view

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/AlexGalhardo/vukem/analytics"
	"github.com/AlexGalhardo/vukem/i18n"
	"github.com/AlexGalhardo/vukem/vite"
	"github.com/AlexGalhardo/vukem/web"
)

// Config configures an Engine.
type Config struct {
	// FS holds the views (os.DirFS("resources/views") or an embed.FS).
	FS fs.FS
	// Ext is the view file extension (default ".html").
	Ext string
	// DefaultLayout is used by pages without a layout comment (default
	// "app"; ignored when layouts/app.html does not exist).
	DefaultLayout string
	// Reload re-parses views on every render (development).
	Reload bool
	// Funcs adds custom template functions. A name already used by a
	// built-in ("route", "csrfField", "cspNonce"...) is an error.
	Funcs template.FuncMap
	// Analytics backs the "analytics" function, which renders the enabled
	// providers' head tags with the request's CSP nonce (nothing when off).
	Analytics analytics.Config
	// URLs resolves named routes for the "route" function.
	URLs web.URLGenerator
	// AssetPrefix is prepended by "asset" (default "/").
	AssetPrefix string
	// Vite backs the "vite" and "viteReactRefresh" functions; "asset"
	// resolves files Vite processed through it first.
	Vite *vite.Vite
	// I18n backs the "t" and "locale" functions, in the locale that
	// i18n.Middleware stored in the request context (the bundle's default
	// otherwise). Without it, the bundle in the context is used, and "t"
	// returns the key when there is none.
	I18n *i18n.Bundle
}

// Engine renders views; it implements web.Renderer.
type Engine struct {
	cfg Config

	mu    sync.RWMutex
	pages map[string]*template.Template
}

var (
	layoutComment = regexp.MustCompile(`^\s*\{\{/\*\s*layout:\s*([A-Za-z0-9_\-/]+)\s*\*/\}\}`)
	sharedDirs    = []string{"layouts", "partials", "components"}
)

// New parses every view, so syntax errors surface at boot.
func New(cfg Config) (*Engine, error) {
	if cfg.FS == nil {
		return nil, errors.New("view: Config.FS is required")
	}

	if cfg.Ext == "" {
		cfg.Ext = ".html"
	}

	if cfg.DefaultLayout == "" {
		cfg.DefaultLayout = "app"
	}

	if cfg.AssetPrefix == "" {
		cfg.AssetPrefix = "/"
	}

	e := &Engine{cfg: cfg}

	// A custom csrfField, cspNonce or errors would silently drop the CSRF
	// token, the CSP nonce or escaping the built-ins guarantee.
	builtins := e.builtinFuncs(context.Background())
	for _, name := range slices.Sorted(maps.Keys(cfg.Funcs)) {
		if _, taken := builtins[name]; taken {
			return nil, fmt.Errorf("view: Config.Funcs %q replaces a built-in template function", name)
		}
	}

	pages, err := e.parseAll()
	if err != nil {
		return nil, err
	}

	e.pages = pages

	return e, nil
}

func (e *Engine) parseAll() (map[string]*template.Template, error) {
	var (
		shared []string
		pages  []string
	)

	err := fs.WalkDir(e.cfg.FS, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || path.Ext(p) != e.cfg.Ext {
			return nil
		}

		if slices.Contains(sharedDirs, strings.SplitN(p, "/", 2)[0]) {
			shared = append(shared, p)
		} else {
			pages = append(pages, p)
		}

		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("view: walk: %w", err)
	}

	// Parse time only needs the function names; Render rebinds them to the
	// request context on its clone.
	base := template.New("").Funcs(e.funcs(context.Background()))

	for _, p := range shared {
		if err := e.parseFile(base, p); err != nil {
			return nil, err
		}
	}

	out := make(map[string]*template.Template, len(pages))

	for _, p := range pages {
		name := strings.TrimSuffix(p, e.cfg.Ext)

		t, err := base.Clone()
		if err != nil {
			return nil, err
		}

		src, err := fs.ReadFile(e.cfg.FS, p)
		if err != nil {
			return nil, fmt.Errorf("view: read %s: %w", p, err)
		}

		layout := e.cfg.DefaultLayout
		if m := layoutComment.FindSubmatch(src); m != nil {
			layout = string(m[1])
		}

		root := name
		if layout != "none" && t.Lookup("layouts/"+layout) != nil {
			root = "layouts/" + layout
		} else if m := layoutComment.FindSubmatch(src); m != nil && layout != "none" {
			return nil, fmt.Errorf("view: %s uses unknown layout %q", p, layout)
		}

		if _, err := t.New(name).Parse(string(src)); err != nil {
			return nil, fmt.Errorf("view: parse %s: %w", p, err)
		}

		out[name] = t.Lookup(root)
	}

	return out, nil
}

func (e *Engine) parseFile(t *template.Template, p string) error {
	src, err := fs.ReadFile(e.cfg.FS, p)
	if err != nil {
		return fmt.Errorf("view: read %s: %w", p, err)
	}

	if _, err := t.New(strings.TrimSuffix(p, e.cfg.Ext)).Parse(string(src)); err != nil {
		return fmt.Errorf("view: parse %s: %w", p, err)
	}

	return nil
}

func (e *Engine) lookup(name string) (*template.Template, error) {
	if e.cfg.Reload {
		pages, err := e.parseAll()
		if err != nil {
			return nil, err
		}

		e.mu.Lock()
		e.pages = pages
		e.mu.Unlock()
	}

	e.mu.RLock()
	t, ok := e.pages[name]
	e.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("view: %q not found", name)
	}

	return t, nil
}

// Has implements web.Renderer.
func (e *Engine) Has(name string) bool {
	e.mu.RLock()
	defer e.mu.RUnlock()

	_, ok := e.pages[name]

	return ok
}

// Names lists the page names.
func (e *Engine) Names() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	names := make([]string, 0, len(e.pages))
	for n := range e.pages {
		names = append(names, n)
	}

	slices.Sort(names)

	return names
}

// Render implements web.Renderer: it renders the page (inside its layout).
func (e *Engine) Render(ctx context.Context, w io.Writer, name string, data any) error {
	t, err := e.lookup(name)
	if err != nil {
		return err
	}

	return e.execute(ctx, w, t, t.Name(), data)
}

// RenderBlock implements web.Renderer: it renders a single {{block}} or
// {{define}} of the page, without the layout.
func (e *Engine) RenderBlock(ctx context.Context, w io.Writer, name, block string, data any) error {
	t, err := e.lookup(name)
	if err != nil {
		return err
	}

	if t.Lookup(block) == nil {
		return fmt.Errorf("view: %q has no block %q", name, block)
	}

	return e.execute(ctx, w, t, block, data)
}

func (e *Engine) execute(ctx context.Context, w io.Writer, t *template.Template, name string, data any) error {
	clone, err := t.Clone()
	if err != nil {
		return fmt.Errorf("view: clone: %w", err)
	}

	clone.Funcs(e.funcs(ctx))

	if err := clone.ExecuteTemplate(w, name, data); err != nil {
		return fmt.Errorf("view: render %s: %w", name, err)
	}

	return nil
}
