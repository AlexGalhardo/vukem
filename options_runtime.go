package vukem

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/netip"
	"time"

	"github.com/AlexGalhardo/vukem/config"
)

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

// WithSource uses an already resolved configuration source.
func WithSource(src *config.Source) Option {
	return func(o *options) error { o.source = src; return nil }
}

// WithEnv loads configuration from fsys (.env files) and getenv.
func WithEnv(fsys fs.FS, getenv func(string) (string, bool)) Option {
	return func(o *options) error { o.fsys, o.getenv = fsys, getenv; return nil }
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
