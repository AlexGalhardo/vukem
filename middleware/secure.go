package middleware

import (
	"cmp"
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/AlexGalhardo/vukem/web"
)

// SecureHeadersConfig configures SecureHeaders. Zero values use the secure
// defaults documented in docs/security.md.
type SecureHeadersConfig struct {
	// CSP is the Content-Security-Policy; "{nonce}" is replaced with a
	// per-request nonce. Empty uses DefaultCSP; "-" disables the header.
	CSP string
	// HSTSMaxAge enables Strict-Transport-Security on HTTPS requests.
	// Zero uses two years; negative disables it.
	HSTSMaxAge time.Duration
	// FrameOptions defaults to DENY; "-" disables it.
	FrameOptions string
	// ReferrerPolicy defaults to strict-origin-when-cross-origin.
	ReferrerPolicy string
	// PermissionsPolicy defaults to camera=(), microphone=(), geolocation=().
	PermissionsPolicy string
	// TrustForwardedProto treats X-Forwarded-Proto: https as HTTPS (set it
	// only behind a trusted proxy).
	TrustForwardedProto bool
	// TrustedProxies, when non-empty, limits TrustForwardedProto to
	// requests whose direct peer is in one of these networks, so clients
	// that reach the app around the proxy cannot claim HTTPS.
	TrustedProxies []*net.IPNet
	// DevServer returns the origin of a running asset dev server (the Vite
	// hot URL) or "". When set, the origin is allowed in script-src,
	// style-src, img-src, font-src and connect-src (plus its ws:// form for
	// hot reload). Wire it only outside production.
	DevServer func() string
	// CSPSources adds sources to directives of the policy, e.g. an
	// analytics origin in script-src and connect-src, or the 'sha256-...'
	// hash of a style a library injects. A "-src" directive missing from
	// the policy starts from the default-src sources. Unsafe sources
	// ('unsafe-*' keywords, "*", nonces, scheme-only sources in script
	// directives) are refused: write a full CSP to opt into them.
	CSPSources map[string][]string
	// ReportURI receives CSP violation reports: an absolute http(s) URL or
	// a path. It adds report-uri and report-to directives and a
	// Reporting-Endpoints header naming the "csp-endpoint" group.
	ReportURI string
}

// Validate reports CSPSources and ReportURI values that SecureHeaders
// would drop. Call it at boot: SecureHeaders never fails, it serves the
// policy without the invalid extras (stricter, never weaker).
func (cfg SecureHeadersConfig) Validate() error {
	_, err := cfg.policy()
	return err
}

// DefaultCSP is a strict policy suitable for server-rendered pages with
// nonce'd scripts and styles.
const DefaultCSP = "default-src 'self'; script-src 'self' 'nonce-{nonce}'; style-src 'self' 'nonce-{nonce}'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'self'; " +
	"frame-ancestors 'none'; form-action 'self'"

// CSPNonce returns the per-request CSP nonce, or "" when SecureHeaders did
// not run. Templates expose it as {{ cspNonce }}.
func CSPNonce(ctx context.Context) string { return web.CSPNonce(ctx) }

// SecureHeaders sets security headers on every response (A02).
func SecureHeaders(cfg SecureHeadersConfig) Middleware {
	csp, dropped := cfg.policy()
	if dropped != nil {
		cfg.CSPSources, cfg.ReportURI = nil, ""
		csp, _ = cfg.policy()
	}

	var warnOnce sync.Once

	reporting := ""
	if cfg.ReportURI != "" {
		reporting = reportGroup + `="` + cfg.ReportURI + `"`
	}

	hsts := ""
	if cfg.HSTSMaxAge >= 0 {
		age := cfg.HSTSMaxAge
		if age == 0 {
			age = 2 * 365 * 24 * time.Hour
		}

		hsts = "max-age=" + strconv.Itoa(int(age.Seconds())) + "; includeSubDomains"
	}

	frame := orDefault(cfg.FrameOptions, "DENY")
	referrer := orDefault(cfg.ReferrerPolicy, "strict-origin-when-cross-origin")
	permissions := orDefault(cfg.PermissionsPolicy, "camera=(), microphone=(), geolocation=()")

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if dropped != nil {
				// No logger at construction: warn once, on the first request.
				warnOnce.Do(func() {
					log := cmp.Or(web.ServicesFrom(r.Context()).Log, slog.Default())
					log.WarnContext(r.Context(), "secure headers: invalid CSP extras dropped", "err", dropped)
				})
			}

			h := w.Header()
			h.Set("X-Content-Type-Options", "nosniff")
			h.Set("Referrer-Policy", referrer)
			h.Set("Cross-Origin-Opener-Policy", "same-origin")
			h.Set("Permissions-Policy", permissions)

			if frame != "-" {
				h.Set("X-Frame-Options", frame)
			}

			if reporting != "" {
				h.Set("Reporting-Endpoints", reporting)
			}

			https := r.TLS != nil || (cfg.TrustForwardedProto && r.Header.Get("X-Forwarded-Proto") == "https" &&
				fromTrustedPeer(r, cfg.TrustedProxies))
			if hsts != "" && https {
				h.Set("Strict-Transport-Security", hsts)
			}

			if csp != "-" {
				nonce := newNonce()
				policy := strings.ReplaceAll(csp, "{nonce}", nonce)

				if cfg.DevServer != nil {
					policy = allowDevServer(policy, cfg.DevServer())
				}

				h.Set("Content-Security-Policy", policy)
				r = r.WithContext(web.WithCSPNonce(r.Context(), nonce))
			}

			next.ServeHTTP(w, r)
		})
	}
}

// reportGroup is the Reporting-Endpoints group named by report-to.
const reportGroup = "csp-endpoint"

// policy returns the CSP template with CSPSources and ReportURI applied.
// Without either, a custom CSP is returned unchanged.
func (cfg SecureHeadersConfig) policy() (string, error) {
	csp := cmp.Or(cfg.CSP, DefaultCSP)
	if len(cfg.CSPSources) == 0 && cfg.ReportURI == "" {
		return csp, nil
	}

	if csp == "-" {
		return "", errors.New("middleware: CSPSources and ReportURI need a Content-Security-Policy (CSP is \"-\")")
	}

	dirs := parseCSP(csp)

	for _, name := range slices.Sorted(maps.Keys(cfg.CSPSources)) {
		if err := checkCSPSources(name, cfg.CSPSources[name]); err != nil {
			return "", err
		}

		i := slices.IndexFunc(dirs, func(d cspDirective) bool { return d.name == name })
		if i < 0 {
			chain, ok := cspFallback[name]
			if !ok {
				return "", fmt.Errorf("middleware: CSP directive %q is not in the policy; set CSP instead", name)
			}

			dirs = append(dirs, cspDirective{name: name, sources: fallbackSources(dirs, chain)})
			i = len(dirs) - 1
		}

		for _, src := range cfg.CSPSources[name] {
			if !slices.Contains(dirs[i].sources, src) {
				dirs[i].sources = append(dirs[i].sources, src)
			}
		}
	}

	if cfg.ReportURI != "" {
		if err := checkReportURI(cfg.ReportURI); err != nil {
			return "", err
		}

		if slices.ContainsFunc(
			dirs,
			func(d cspDirective) bool { return d.name == "report-uri" || d.name == "report-to" },
		) {
			return "", errors.New("middleware: the CSP already reports; drop ReportURI or its report-uri/report-to")
		}

		dirs = append(dirs,
			cspDirective{name: "report-uri", sources: []string{cfg.ReportURI}},
			cspDirective{name: "report-to", sources: []string{reportGroup}},
		)
	}

	parts := make([]string, len(dirs))
	for i, d := range dirs {
		parts[i] = strings.Join(append([]string{d.name}, d.sources...), " ")
	}

	return strings.Join(parts, "; "), nil
}

type cspDirective struct {
	name    string
	sources []string
}

func parseCSP(csp string) []cspDirective {
	var dirs []cspDirective

	for part := range strings.SplitSeq(csp, ";") {
		fields := strings.Fields(part)
		if len(fields) > 0 {
			dirs = append(dirs, cspDirective{name: strings.ToLower(fields[0]), sources: fields[1:]})
		}
	}

	return dirs
}

// cspFallback lists, per fetch directive, the directives a browser falls
// back to when it is missing (CSP Level 3). A new directive starts from
// the first one present, so it never loses the nonce or 'self' it had.
var cspFallback = map[string][]string{
	"script-src-elem": {"script-src", "default-src"},
	"script-src-attr": {"script-src", "default-src"},
	"style-src-elem":  {"style-src", "default-src"},
	"style-src-attr":  {"style-src", "default-src"},
	"worker-src":      {"child-src", "script-src", "default-src"},
	"frame-src":       {"child-src", "default-src"},
	"child-src":       {"default-src"},
	"script-src":      {"default-src"},
	"style-src":       {"default-src"},
	"img-src":         {"default-src"},
	"font-src":        {"default-src"},
	"connect-src":     {"default-src"},
	"media-src":       {"default-src"},
	"manifest-src":    {"default-src"},
	"object-src":      {"default-src"},
}

// fallbackSources copies the sources of the first directive of chain in
// dirs; 'none' is dropped as it cannot be combined with other sources.
func fallbackSources(dirs []cspDirective, chain []string) []string {
	for _, name := range chain {
		for _, d := range dirs {
			if d.name == name {
				return slices.DeleteFunc(slices.Clone(d.sources), func(s string) bool { return s == "'none'" })
			}
		}
	}

	return nil
}

var (
	cspDirectiveName = regexp.MustCompile(`^[a-z][a-z-]*$`)
	cspHash          = regexp.MustCompile(`^'sha(256|384|512)-[A-Za-z0-9+/_=-]+'$`)
	cspScheme        = regexp.MustCompile(`^[a-z][a-z0-9+.-]*:$`)
	cspHost          = regexp.MustCompile(`^(\*\.)?[a-z0-9-]+(\.[a-z0-9-]+)*$`)
	cspPort          = regexp.MustCompile(`^(\*|\d{1,5})$`)
)

// cspSchemes lists the scheme-only sources ("https:") each directive may
// take; any other directive refuses them, as they allow every host.
var cspSchemes = map[string][]string{
	"img-src":     {"https:", "data:", "blob:"},
	"media-src":   {"https:", "data:", "blob:"},
	"font-src":    {"https:", "data:"},
	"connect-src": {"https:", "wss:"},
}

// scriptDirectives load or run code: host wildcards are refused there.
var scriptDirectives = []string{
	"default-src", "script-src", "script-src-elem", "script-src-attr", "object-src", "worker-src", "base-uri",
}

func checkCSPSources(name string, sources []string) error {
	if !cspDirectiveName.MatchString(name) || name == "report-uri" || name == "report-to" {
		return fmt.Errorf("middleware: invalid CSP directive %q in CSPSources", name)
	}

	for _, src := range sources {
		lower := strings.ToLower(src)

		switch {
		case src == "" || strings.ContainsAny(src, ";,\"\\") || strings.ContainsFunc(src, notPrintableASCII):
			return fmt.Errorf("middleware: invalid CSP source %q for %s", src, name)
		case strings.Contains(lower, "unsafe"):
			return fmt.Errorf("middleware: CSP source %s for %s is unsafe; set CSP to opt in", src, name)
		case strings.HasPrefix(src, "'"):
			if !cspHash.MatchString(src) && lower != "'self'" && lower != "'strict-dynamic'" &&
				lower != "'report-sample'" {
				return fmt.Errorf("middleware: CSP source %s for %s is not allowed (nonces are per request)", src, name)
			}
		case cspScheme.MatchString(lower):
			if !slices.Contains(cspSchemes[name], lower) {
				return fmt.Errorf("middleware: scheme-only CSP source %s would allow any host in %s", src, name)
			}
		default:
			if err := checkCSPHost(name, lower); err != nil {
				return err
			}
		}
	}

	return nil
}

// checkCSPHost checks a host source ([scheme://]host[:port][/path]): no
// "*" host, wildcards only as "*." over at least two labels, and none in
// script directives.
func checkCSPHost(name, src string) error {
	rest := src
	if scheme, after, ok := strings.Cut(src, "://"); ok {
		if !cspScheme.MatchString(scheme + ":") {
			return fmt.Errorf("middleware: invalid CSP source %q for %s", src, name)
		}

		rest = after
	}

	hostPort, _, _ := strings.Cut(rest, "/")
	host, port, hasPort := strings.Cut(hostPort, ":")

	switch {
	case hasPort && !cspPort.MatchString(port), !cspHost.MatchString(host):
		return fmt.Errorf("middleware: invalid or wildcard CSP host in %q for %s", src, name)
	case strings.HasPrefix(host, "*.") && strings.Count(host, ".") < 2:
		return fmt.Errorf("middleware: CSP wildcard %q for %s covers a whole public suffix", src, name)
	case strings.HasPrefix(host, "*.") && slices.Contains(scriptDirectives, name):
		return fmt.Errorf("middleware: CSP wildcard host %q is not allowed in %s", src, name)
	}

	return nil
}

func notPrintableASCII(r rune) bool { return r <= ' ' || r > '~' }

// checkReportURI accepts an https URL without credentials, or a path
// starting with a single "/" (browsers read "\" as "/", so it is refused).
func checkReportURI(raw string) error {
	u, err := url.Parse(raw)

	switch {
	case err != nil || strings.ContainsAny(raw, ";,\"'\\") || strings.ContainsFunc(raw, notPrintableASCII):
	case u.User != nil:
	case u.Scheme == "https" && u.Host != "":
		return nil
	case u.Scheme == "" && u.Host == "" && strings.HasPrefix(raw, "/") && !strings.HasPrefix(raw, "//"):
		return nil
	}

	return fmt.Errorf("middleware: ReportURI %q must be an https URL or a path", raw)
}

// fromTrustedPeer reports whether the direct peer is in proxies; an empty
// list keeps the pre-TrustedProxies behaviour (any peer).
func fromTrustedPeer(r *http.Request, proxies []*net.IPNet) bool {
	if len(proxies) == 0 {
		return true
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}

	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}

	return slices.ContainsFunc(proxies, func(n *net.IPNet) bool { return n.Contains(ip) })
}

// devOrigin matches a bare http(s) origin, so a dev server URL can never
// inject other CSP directives.
var devOrigin = regexp.MustCompile(`^https?://[A-Za-z0-9.\-\[\]:]+$`)

func allowDevServer(policy, origin string) string {
	origin = strings.TrimSuffix(origin, "/")
	if !devOrigin.MatchString(origin) {
		return policy
	}

	ws := "ws" + strings.TrimPrefix(origin, "http")
	extra := map[string]string{
		"script-src": origin, "style-src": origin, "img-src": origin, "font-src": origin,
		"connect-src": origin + " " + ws,
	}

	parts := strings.Split(policy, ";")
	for i, part := range parts {
		name, _, _ := strings.Cut(strings.TrimSpace(part), " ")
		if add, ok := extra[name]; ok {
			parts[i] = strings.TrimRight(part, " ") + " " + add
		}
	}

	return strings.Join(parts, ";")
}

func newNonce() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b) // never fails since Go 1.24

	return base64.RawURLEncoding.EncodeToString(b) // base64url: no characters that need HTML escaping
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}

	return v
}
