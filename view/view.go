// Package view renders html/template views with layouts, partials and
// fragments (single blocks, for htmx), plus request-aware helpers: route,
// asset, csrfField, old, errors, flash and cspNonce.
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
package view

import (
	"context"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"sync"

	"github.com/AlexGalhardo/golang-framework/web"
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
	// Funcs adds custom template functions.
	Funcs template.FuncMap
	// URLs resolves named routes for the "route" function.
	URLs web.URLGenerator
	// AssetPrefix is prepended by "asset" (default "/").
	AssetPrefix string
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
