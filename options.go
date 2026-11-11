package vukem

import (
	"crypto/tls"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/AlexGalhardo/vukem/config"
	"github.com/AlexGalhardo/vukem/inertia"
	"github.com/AlexGalhardo/vukem/router"
	"github.com/AlexGalhardo/vukem/view"
)

// WithRoutes registers routes (and router middleware) after providers
// registered, so handlers can use the services they added. An error stops
// New.
func WithRoutes(fn func(r *router.Router, app *App) error) Option {
	return func(o *options) error { o.routes = append(o.routes, fn); return nil }
}

// WithRoutesAfterBoot registers routes at the end of Boot, after every
// provider's Boot, so handlers can use clients a provider opens in Boot (a
// database or object-store client). The routes exist only once the app
// booted: Run, commands and vukemtest boot first; Handler fails with
// [ErrNotBooted] before that, and Boot fails with [ErrRoutesCompiled] when
// the router was compiled earlier. Global router middleware applies to
// them; groups made in WithRoutes do not. In [KindWarmup], which never
// boots, they run at the end of New so the route table is complete; booted
// services are then still unset (check app.Kind) and a panic is returned
// as an error.
func WithRoutesAfterBoot(fn func(r *router.Router, app *App) error) Option {
	return func(o *options) error { o.bootRoutes = append(o.bootRoutes, fn); return nil }
}

// ErrRoutesCompiled is returned by Boot when routes from
// WithRoutesAfterBoot are pending but the router was already compiled
// (Handler, Router.URL or a request before Boot), so they would never be
// served.
var ErrRoutesCompiled = errors.New("vukem: router compiled before Boot, so routes after boot would be lost; " +
	"call Boot before Handler/Router.URL; avoid URL() inside WithRoutes")

// ErrNotBooted is returned by Handler when routes from WithRoutesAfterBoot
// are pending because Boot has not run: serving then would answer 404 for
// them.
var ErrNotBooted = errors.New("vukem: routes after boot are pending; call Boot before Handler")

// WithClock sets the clock used by sessions and other time-based features
// (tests use vukemtest.FakeClock).
func WithClock(now func() time.Time) Option {
	return func(o *options) error { o.now = now; return nil }
}

// WithStdout sets where commands print (os.Stdout by default).
func WithStdout(w io.Writer) Option {
	return func(o *options) error { o.stdout = w; return nil }
}

// WithStderr sets where commands print diagnostics (os.Stderr by
// default).
func WithStderr(w io.Writer) Option {
	return func(o *options) error { o.stderr = w; return nil }
}

// WithStdin sets where commands read input (os.Stdin by default).
func WithStdin(r io.Reader) Option {
	return func(o *options) error { o.stdin = r; return nil }
}

// WithReady receives the HTTP listen address once the server is listening.
func WithReady(ch chan<- string) Option {
	return func(o *options) error { o.ready = ch; return nil }
}

// WithDevtools enables the development dashboard at /_vukem (same as
// DEVTOOLS=true). New fails when APP_ENV=production.
func WithDevtools() Option {
	return func(o *options) error { o.devtools = true; return nil }
}

// WithoutHTTP disables the HTTP server runner (workers-only processes).
func WithoutHTTP() Option {
	return func(o *options) error { o.noHTTP = true; return nil }
}

// WithBench registers the load-test scenarios run by the bench:run command.
func WithBench(scenarios ...BenchScenario) Option {
	return func(o *options) error { o.benchScenarios = append(o.benchScenarios, scenarios...); return nil }
}

// WithSource uses an already resolved configuration source.
func WithSource(src *config.Source) Option {
	return func(o *options) error { o.source = src; return nil }
}

// WithEnv loads configuration from fsys (.env files) and getenv.
func WithEnv(fsys fs.FS, getenv func(string) (string, bool)) Option {
	return func(o *options) error { o.fsys, o.getenv = fsys, getenv; return nil }
}

// WithEnvDefaults sets configuration values used when a key is unset or
// empty in both the process environment and the .env files: the
// precedence is process environment > .env files > these defaults. Use it
// instead of wrapping WithEnv's getenv, which ranks above .env. APP_ENV
// cannot have a default (it picks the .env files).
func WithEnvDefaults(defaults map[string]string) Option {
	return func(o *options) error {
		if _, ok := defaults["APP_ENV"]; ok {
			return errors.New("vukem: WithEnvDefaults cannot set APP_ENV (it selects the .env files)")
		}

		o.envDefaults = defaults

		return nil
	}
}

// WithTLSConfig serves HTTPS on PORT with cfg (a clone): HTTP/2 over ALPN
// h2 plus HTTP/1.1. cfg must carry a certificate (Certificates,
// GetCertificate or GetConfigForClient, e.g. from autocert); MinVersion
// defaults to TLS 1.2 and lower is a boot error. TLS_CERT_FILE and
// TLS_KEY_FILE do the same from PEM files and exclude a certificate here.
func WithTLSConfig(cfg *tls.Config) Option {
	return func(o *options) error {
		if cfg == nil {
			return errors.New("vukem: WithTLSConfig: nil config")
		}

		o.tlsConfig = cfg

		return nil
	}
}

// WithLogger sets the logger instead of building one from configuration.
func WithLogger(l *slog.Logger) Option {
	return func(o *options) error { o.logger = l; return nil }
}

// WithLogOutput sets where the default logger writes (stderr by default).
func WithLogOutput(w io.Writer) Option {
	return func(o *options) error { o.logOut = w; return nil }
}

// WithKind sets the runtime kind (web by default).
func WithKind(k Kind) Option {
	return func(o *options) error { o.kind = k; return nil }
}

// WithProviders appends providers, registered and booted in order.
func WithProviders(p ...Provider) Option {
	return func(o *options) error { o.providers = append(o.providers, p...); return nil }
}

// WithRunner adds a long-running component started by Run.
func WithRunner(name string, r Runner) Option {
	return func(o *options) error { o.runners = append(o.runners, namedRunner{name, r}); return nil }
}

// WithVersion sets App.Version, the build version (a tag or commit,
// typically injected with `vukem build -ldflags "-X main.version=..."`),
// and reports it as "version" in GET /up, GET /up?checks=1 and
// ReadyHandler. Without it (the default) the probes report no version, so
// they do not fingerprint the build.
func WithVersion(version string) Option {
	return func(o *options) error { o.version = version; return nil }
}

// WithName sets the application name used when APP_NAME is unset, instead
// of "Vukem". APP_NAME still wins. `vukem new` writes the project name here,
// so a deployment without a .env (CI, containers) does not show "Vukem" in
// page titles, the OpenAPI title or the scheduler lock keys.
func WithName(name string) Option {
	return func(o *options) error { o.name = name; return nil }
}

// WithTrustedProxies trusts these networks as proxies in addition to
// TRUSTED_PROXIES, for ranges that come from code rather than the
// environment, such as a CDN's published list
// (contrib/cloudflare.Ranges). c.IP() then honours their
// X-Forwarded-For or CLIENT_IP_HEADER, and CLIENT_IP_HEADER and
// TRUST_FORWARDED_PROTO count them as configured proxies. An invalid
// prefix is a boot error.
func WithTrustedProxies(prefixes ...netip.Prefix) Option {
	return func(o *options) error {
		for _, p := range prefixes {
			if !p.IsValid() {
				return fmt.Errorf("vukem: WithTrustedProxies: invalid prefix %q", p)
			}

			p = p.Masked()
			o.proxies = append(o.proxies, &net.IPNet{
				IP:   p.Addr().AsSlice(),
				Mask: net.CIDRMask(p.Bits(), p.Addr().BitLen()),
			})
		}

		return nil
	}
}

// WithHealthCheck registers a readiness check at build time, the
// declarative form of [App.AddHealthCheck]: a dependency the instance
// needs in order to serve, reported by [App.ReadyHandler] and
// GET /up?checks=1 and bounded by [DefaultHealthCheckTimeout]. Use
// [App.AddHealthCheckTimeout] from a provider for a check that needs its
// own timeout.
//
// Point the platform's liveness probe at GET /up, which touches nothing,
// and readiness at a path serving ReadyHandler. Pointing liveness at the
// checks turns one slow dependency into a restart loop, and pointing a
// load balancer at them turns it into zero healthy instances.
func WithHealthCheck(name string, check HealthCheck) Option {
	return func(o *options) error {
		o.healthChecks = append(o.healthChecks, namedHealthCheck{name, check})

		return nil
	}
}

// WithViews renders html/template views from fsys (os.DirFS("resources/views")
// or an embed.FS). Views reload on every request outside production.
func WithViews(fsys fs.FS) Option {
	return func(o *options) error { o.views = fsys; return nil }
}

// WithViewPages lists the typed views (view.Page.Decl) that view:check
// verifies against their data types. It does not change rendering, and it
// needs WithViews.
func WithViewPages(decls ...view.Decl) Option {
	return func(o *options) error {
		o.viewPages = append(o.viewPages, decls...)
		return nil
	}
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
