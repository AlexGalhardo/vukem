package vukem

import (
	"fmt"
	"html/template"
	"io/fs"

	"github.com/AlexGalhardo/vukem/inertia"
)

// WithViews renders html/template views from fsys (os.DirFS("resources/views")
// or an embed.FS). Views reload on every request outside production.
func WithViews(fsys fs.FS) Option {
	return func(o *options) error { o.views = fsys; return nil }
}

// WithReactEmails renders mail.React contents from fsys: the directory
// that React Email's `email export` writes (os.DirFS("resources/emails")
// or an embed.FS). Pages are html/template views without layouts and get
// the "t" and "locale" functions and WithViewFuncs; they reload on every
// send outside production.
func WithReactEmails(fsys fs.FS) Option {
	return func(o *options) error { o.reactEmails = fsys; return nil }
}

// WithViewFuncs adds template functions to the WithViews engine
// (view.Config.Funcs), for helpers such as money or date formatting. Calls
// accumulate. A name given twice, a nil function, a built-in's name
// ("route", "csrfField", "cspNonce"...) or a missing WithViews is a boot
// error.
func WithViewFuncs(funcs template.FuncMap) Option {
	return func(o *options) error {
		if o.viewFuncs == nil {
			o.viewFuncs = template.FuncMap{}
		}

		for name, fn := range funcs {
			if fn == nil {
				return fmt.Errorf("vukem: WithViewFuncs: %q is nil", name)
			}

			if _, dup := o.viewFuncs[name]; dup {
				return fmt.Errorf("vukem: WithViewFuncs: %q given twice", name)
			}

			o.viewFuncs[name] = fn
		}

		return nil
	}
}

// WithInertia enables the Inertia adapter (app.Inertia); add
// app.Inertia.Middleware to the routes that render pages. Errors on Inertia
// requests render inertia.Config.ErrorComponent (default "Error", see
// inertia.Inertia.ErrorPage): New installs it as
// app.Router.Services().ErrorPage when no hook is set yet, before the
// routes run, so routes may replace it or set it to nil to keep the HTML
// error page. Outside production a web app warns once at boot when the
// project (the WithEnv file system, else the working directory) has
// resources/js/pages but no page for that component. Version
// and the SSR dev endpoint default to app.Vite when WithVite is given.
func WithInertia(cfg inertia.Config) Option {
	return func(o *options) error { o.inertia = &cfg; return nil }
}

// WithVite enables the "vite" and "viteReactRefresh" view functions over the
// public directory fsys (os.DirFS("public") or an embed.FS). The dev server
// "hot" file is honoured only outside production.
func WithVite(fsys fs.FS) Option {
	return func(o *options) error { o.vite = fsys; return nil }
}

// WithTranslations loads the JSON catalogs at the root of fsys
// (os.DirFS("resources/lang") or an embed.FS) into App.I18n, with
// APP_LOCALE (default "en") as the fallback locale, plus the framework's
// built-in messages for keys the catalogs do not define
// (i18n.Bundle.AddBuiltin). Views get the "t" and "locale" functions, and
// DefaultMiddleware installs i18n.Middleware.
func WithTranslations(fsys fs.FS) Option {
	return func(o *options) error { o.lang = fsys; return nil }
}

// inertiaPagesDir is where the starters keep Inertia pages.
const inertiaPagesDir = "resources/js/pages"

// inertiaPageMissing reports whether fsys uses the starters' pages
// directory but has no page for component. Other layouts are not checked.
func inertiaPageMissing(fsys fs.FS, component string) bool {
	if _, err := fs.Stat(fsys, inertiaPagesDir); err != nil {
		return false
	}

	for _, ext := range []string{".tsx", ".jsx", ".vue", ".svelte", ".ts", ".js"} {
		if _, err := fs.Stat(fsys, inertiaPagesDir+"/"+component+ext); err == nil {
			return false
		}
	}

	return true
}
